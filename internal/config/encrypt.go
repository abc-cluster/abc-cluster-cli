// Package config provides encryption support for secrets managed via 'abc secrets' command.
//
// Encryption is password-based (local password mode) and uses the same
// salt/password as the data encryption/decryption module. Users must explicitly
// invoke 'abc secrets' with --unsafe-local flag to encrypt/decrypt secrets.
//
// Environment Variables:
//
//	ABC_CRYPT_PASSWORD  — encryption password (required for --unsafe-local operations)
//	ABC_CRYPT_SALT      — optional salt for key derivation (same as data encryption)
//
// Secrets are stored in the config file under the 'secrets' section:
//
//	secrets:
//	  my-api-key: "ENC[AES256_GCM,data:...,iv:...,tag:...,type:str]"
//	  db-password: "ENC[AES256_GCM,data:...,iv:...,tag:...,type:str]"
//
// This module exports functions for use by cmd/secrets/ subcommand:
// - EncryptField: Encrypt a single value
// - DecryptField: Decrypt a single value
// - DeriveKey: Derive a key from password/salt
package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/scrypt"
	"gopkg.in/yaml.v3"
)

// EncryptedFields defines which config fields should be encrypted.
var EncryptedFields = map[string]bool{
	"access_token": true,
}

// IsEncrypted checks if a field should be encrypted.
func IsEncrypted(fieldName string) bool {
	return EncryptedFields[fieldName]
}

// EncryptionConfig holds encryption settings.
type EncryptionConfig struct {
	Password string // encryption password
	Salt     string // optional salt for key derivation
	Enabled  bool   // whether encryption is active
}

// DefaultSalt is the default salt used for key derivation.
// Same as data encryption/decryption module.
var defaultSalt = []byte{0xA8, 0x0D, 0xF4, 0x3A, 0x8F, 0xBD, 0x03, 0x08, 0xA7, 0xCA, 0xB8, 0x3E, 0x58, 0x1F, 0x86, 0xB1}

// GetEncryptionConfig reads encryption settings from environment.
func GetEncryptionConfig() *EncryptionConfig {
	password := os.Getenv("ABC_CRYPT_PASSWORD")
	salt := os.Getenv("ABC_CRYPT_SALT")

	return &EncryptionConfig{
		Password: password,
		Salt:     salt,
		Enabled:  password != "",
	}
}

// DeriveKey derives an encryption key from password and salt using scrypt.
// Uses same parameters as the data encryption module (16384, 8, 1).
func DeriveKey(password, salt string) ([]byte, error) {
	if password == "" {
		return nil, fmt.Errorf("password is required for encryption")
	}

	saltBytes := defaultSalt
	if salt != "" {
		saltBytes = []byte(salt)
	}

	// Derive 32 bytes for AES-256
	key, err := scrypt.Key([]byte(password), saltBytes, 16384, 8, 1, 32)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	return key, nil
}

// EncryptField encrypts a single plaintext value using AES-256-GCM.
// Returns base64-encoded ciphertext with nonce prepended.
func EncryptField(plaintext, password, salt string) (string, error) {
	key, err := DeriveKey(password, salt)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// Encrypt
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Return base64-encoded
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptField decrypts a previously encrypted field value.
// Expects base64-encoded ciphertext with nonce prepended.
func DecryptField(encryptedBase64, password, salt string) (string, error) {
	key, err := DeriveKey(password, salt)
	if err != nil {
		return "", err
	}

	// Decode base64
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	// Extract nonce and encrypted data
	nonce := ciphertext[:nonceSize]
	encryptedData := ciphertext[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

func encryptNomadTokenInNomadMap(nomad map[string]interface{}, password, salt string) (bool, error) {
	if nomad == nil {
		return false, nil
	}
	token, ok := nomad["nomad_token"].(string)
	if !ok || token == "" {
		return false, nil
	}
	encrypted, err := EncryptField(token, password, salt)
	if err != nil {
		return false, fmt.Errorf("encrypt nomad_token: %w", err)
	}
	nomad["nomad_token"] = encrypted
	return true, nil
}

func encryptNomadTokenFieldsInContext(ctx map[string]interface{}, password, salt string) (bool, error) {
	has := false
	if admin, ok := ctx["admin"].(map[string]interface{}); ok {
		if svc, ok := admin["services"].(map[string]interface{}); ok {
			if nomad, ok := svc["nomad"].(map[string]interface{}); ok {
				ok2, err := encryptNomadTokenInNomadMap(nomad, password, salt)
				if err != nil {
					return false, err
				}
				has = has || ok2
			}
		}
	}
	if serv, ok := ctx["services"].(map[string]interface{}); ok {
		if nomad, ok := serv["nomad"].(map[string]interface{}); ok {
			ok2, err := encryptNomadTokenInNomadMap(nomad, password, salt)
			if err != nil {
				return false, err
			}
			has = has || ok2
		}
	}
	if token, ok := ctx["nomad_token"].(string); ok && token != "" {
		encrypted, err := EncryptField(token, password, salt)
		if err != nil {
			return has, fmt.Errorf("encrypt nomad_token: %w", err)
		}
		ctx["nomad_token"] = encrypted
		has = true
	}
	return has, nil
}

func decryptNomadTokenInNomadMap(nomad map[string]interface{}, password, salt string) {
	if nomad == nil {
		return
	}
	if token, ok := nomad["nomad_token"].(string); ok && token != "" {
		if decrypted, err := DecryptField(token, password, salt); err == nil {
			nomad["nomad_token"] = decrypted
		}
	}
}

func decryptNomadTokenFieldsInContext(ctx map[string]interface{}, password, salt string) {
	if admin, ok := ctx["admin"].(map[string]interface{}); ok {
		if svc, ok := admin["services"].(map[string]interface{}); ok {
			if nomad, ok := svc["nomad"].(map[string]interface{}); ok {
				decryptNomadTokenInNomadMap(nomad, password, salt)
			}
		}
	}
	if serv, ok := ctx["services"].(map[string]interface{}); ok {
		if nomad, ok := serv["nomad"].(map[string]interface{}); ok {
			decryptNomadTokenInNomadMap(nomad, password, salt)
		}
	}
	if token, ok := ctx["nomad_token"].(string); ok && token != "" {
		if decrypted, err := DecryptField(token, password, salt); err == nil {
			ctx["nomad_token"] = decrypted
		}
	}
}

// EncryptConfigFields encrypts all marked fields in a config YAML string.
// Adds SOPS metadata to mark encrypted regions.
func EncryptConfigFields(configYAML string, password, salt string) (string, error) {
	var data map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &data); err != nil {
		return "", fmt.Errorf("parse YAML: %w", err)
	}

	hasEncrypted := false

	// Encrypt access_token and nomad_token (all supported YAML shapes) in all contexts.
	if contexts, ok := data["contexts"].(map[string]interface{}); ok {
		for _, ctxInterface := range contexts {
			if ctx, ok := ctxInterface.(map[string]interface{}); ok {
				if token, ok := ctx["access_token"].(string); ok && token != "" {
					encrypted, err := EncryptField(token, password, salt)
					if err != nil {
						return "", fmt.Errorf("encrypt access_token: %w", err)
					}
					ctx["access_token"] = encrypted
					hasEncrypted = true
				}
				okNomad, err := encryptNomadTokenFieldsInContext(ctx, password, salt)
				if err != nil {
					return "", err
				}
				hasEncrypted = hasEncrypted || okNomad
			}
		}
	}

	// Add SOPS metadata if anything was encrypted
	if hasEncrypted {
		if sopsData, ok := data["sops"].(map[string]interface{}); !ok {
			data["sops"] = map[string]interface{}{
				"encrypted_regex": "^(contexts\\..*\\.access_token|contexts\\..*\\.nomad_token|contexts\\..*\\.services\\.nomad\\.nomad_token|contexts\\..*\\.admin\\.services\\.nomad\\.nomad_token)$",
				"version":         "3.7.0",
			}
		} else {
			sopsData["encrypted_regex"] = "^(contexts\\..*\\.access_token|contexts\\..*\\.nomad_token|contexts\\..*\\.services\\.nomad\\.nomad_token|contexts\\..*\\.admin\\.services\\.nomad\\.nomad_token)$"
			sopsData["version"] = "3.7.0"
		}
	}

	// Marshal back to YAML
	encryptedYAML, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal YAML: %w", err)
	}

	return string(encryptedYAML), nil
}

// DecryptConfigFields decrypts all encrypted fields in a config YAML string.
// Removes SOPS metadata after decryption.
func DecryptConfigFields(configYAML string, password, salt string) (string, error) {
	var data map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &data); err != nil {
		return "", fmt.Errorf("parse YAML: %w", err)
	}

	// Try to decrypt access_token and nomad_token fields in all contexts (all shapes).
	if contexts, ok := data["contexts"].(map[string]interface{}); ok {
		for _, ctxInterface := range contexts {
			if ctx, ok := ctxInterface.(map[string]interface{}); ok {
				if token, ok := ctx["access_token"].(string); ok && token != "" {
					// Try to decrypt; if it fails, it might be plaintext
					decrypted, err := DecryptField(token, password, salt)
					if err == nil {
						ctx["access_token"] = decrypted
					}
					// Silently ignore decryption errors (token might be plaintext)
				}
				decryptNomadTokenFieldsInContext(ctx, password, salt)
			}
		}
	}

	// Remove SOPS metadata
	delete(data, "sops")

	// Marshal back to YAML
	decryptedYAML, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal YAML: %w", err)
	}

	return string(decryptedYAML), nil
}

// EncryptionStatus returns whether encryption is enabled and why.
func EncryptionStatus() (enabled bool, reason string) {
	// Check password-based encryption
	if password := os.Getenv("ABC_CRYPT_PASSWORD"); password != "" {
		return true, "ABC_CRYPT_PASSWORD is set (local password-based encryption)"
	}

	// Check external SOPS integrations
	if os.Getenv("SOPS_AGE_RECIPIENTS") != "" {
		return true, "SOPS_AGE_RECIPIENTS is set (age-based encryption)"
	}

	if os.Getenv("SOPS_KMS_ARN") != "" {
		return true, "SOPS_KMS_ARN is set (AWS KMS encryption)"
	}

	if os.Getenv("SOPS_GCP_KMS") != "" {
		return true, "SOPS_GCP_KMS is set (GCP KMS encryption)"
	}

	return false, "no encryption configured (set ABC_CRYPT_PASSWORD for local encryption)"
}

// RedactSensitiveFields redacts tokens for display purposes.
func RedactSensitiveFields(key, value string) (string, bool) {
	if strings.Contains(key, "access_token") {
		if value == "" {
			return "", true
		}
		if len(value) <= 8 {
			return strings.Repeat("•", len(value)), true
		}
		return value[:8] + strings.Repeat("•", len(value)-8), true
	}
	return value, false
}
