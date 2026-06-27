package data

// upload_encrypt.go — client-side encryption for `abc data upload`, on the age
// envelope (ADR-0067), replacing the retired rclone-crypt path. Two modes:
//
//   --encrypt          → MANAGED: encrypt to the ACTIVE context's group recipient
//                        (native age X25519; recoverable via the broker). Default
//                        on a broker cred tier. Same key as `abc data encrypt`.
//   --crypt-password   → PASSPHRASE: age scrypt recipient (stock-`age` decryptable;
//                        BYO — you must remember it). Works off a broker tier.
//
// The encryption group is always the active context's group, and (rec 2) it must
// match the `--group` destination bucket — the key and the bucket move together,
// so you can never encrypt for group A but store in group B's bucket.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filippo.io/age"
	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/abccrypt"
	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
)

// ageObjectSuffix is appended to the stored object name when an upload is
// encrypted (after any .zst), matching `abc data encrypt` output (= ".age").
var ageObjectSuffix = abccrypt.Suffix

// uploadEncryptor holds the resolved age recipient(s) for an upload. A nil
// *uploadEncryptor means the upload is not encrypted.
type uploadEncryptor struct {
	recipients []age.Recipient
	mode       string // "managed" | "passphrase"
	group      string // managed only: the group name (== active context's group)
	version    int    // managed only: the group key version used (for the object stamp)
}

// buildUploadEncryptor resolves the encryption mode for an upload:
//   - neither --encrypt nor --crypt-password → nil (raw upload, unchanged)
//   - --crypt-password                       → passphrase (age scrypt)
//   - --encrypt (broker tier, no password)   → managed (active group, age X25519)
func buildUploadEncryptor(cmd *cobra.Command, cfg *abccfg.Config, opts *uploadOptions) (*uploadEncryptor, error) {
	passwordProvided := strings.TrimSpace(opts.cryptPassword) != ""
	if !opts.encrypt && !passwordProvided {
		return nil, nil
	}

	// BYO passphrase wins if provided (also the only option off a broker tier).
	if passwordProvided {
		rcpt, err := abccrypt.PassphraseRecipient(opts.cryptPassword)
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(cmd.ErrOrStderr(),
			"Upload encryption: passphrase (age scrypt; stock-age decryptable; NOT managed — remember it).")
		return &uploadEncryptor{recipients: []age.Recipient{rcpt}, mode: "passphrase"}, nil
	}

	// Managed: --encrypt to the active context's group recipient.
	if cfg == nil || !isBrokerCredSource(cfg) {
		return nil, fmt.Errorf(
			"--encrypt needs a managed (broker) context (cred_source: seedling/v1).\n" +
				"  Use --crypt-password <p> for a portable passphrase instead.")
	}
	prov, err := newGroupKeyProvider(cmd, cfg)
	if err != nil {
		return nil, err
	}
	rcpt, gk, err := prov.Recipient()
	if err != nil {
		return nil, err
	}
	encGroup := strings.TrimPrefix(gk.KekID, "group:")

	// rec 2 — the key and the destination bucket must match. Refuse to encrypt
	// for one group while uploading into another's bucket.
	if dest := strings.TrimSpace(opts.group); dest != "" && dest != encGroup {
		return nil, fmt.Errorf(
			"refusing to encrypt for group %q but upload to group %q's bucket — the key and the\n"+
				"  destination must match. Switch context with 'abc context use' so both move together,\n"+
				"  or drop --group to upload to your own group.", encGroup, dest)
	}

	// Keep ~/.abc/age/{recipients,identity}.txt current (stock-age recovery), as
	// `abc data encrypt` does.
	materializeAgeKeyFiles(cmd, gk)
	fmt.Fprintf(cmd.ErrOrStderr(),
		"Upload encryption: managed, group %s (native age X25519; recoverable via the broker).\n", gk.KekID)
	return &uploadEncryptor{recipients: []age.Recipient{rcpt}, mode: "managed", group: encGroup, version: gk.Version}, nil
}

// stampManagedMeta records the group + key version on a managed-encrypted upload's
// metadata. It flows as tusd metadata → the mover writes it as S3 user metadata
// (x-amz-meta-abc-group / abc-key-version), so the STORE can answer "which group
// key + version" per object WITHOUT the key. This is what lets an old file (after
// a group key rotation) be matched to its specific — possibly backed-up — key
// version. No-op for raw / passphrase uploads (native age files are anonymous).
func stampManagedMeta(metadata map[string]string, enc *uploadEncryptor) {
	if enc == nil || enc.mode != "managed" {
		return
	}
	metadata["abc-enc"] = "age-x25519-managed"
	metadata["abc-group"] = enc.group
	metadata["abc-key-version"] = strconv.Itoa(enc.version)
}

// ageEncryptForUpload encrypts sourcePath to a temp .age file when enc is set,
// returning the path to upload + a cleanup func. When enc is nil the original
// path is returned unchanged (mirrors the old encryptForUpload contract).
func ageEncryptForUpload(ctx context.Context, sourcePath string, enc *uploadEncryptor, onProgress func(int64)) (string, func() error, error) {
	if enc == nil || len(enc.recipients) == 0 {
		return sourcePath, nil, nil
	}
	tmpDir, err := uploadTempDir()
	if err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(tmpDir, "abc-upload-*.age")
	if err != nil {
		return "", nil, fmt.Errorf("create encrypt temp file: %w", err)
	}
	tmp := f.Name()
	_ = f.Close()
	cleanup := func() error { return os.Remove(tmp) }
	if err := ageEncryptToPath(ctx, sourcePath, tmp, enc.recipients, onProgress); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	return tmp, cleanup, nil
}

// uploadTempDir returns the directory to use for encrypted upload temp files.
// It respects ABC_CLI_TMPDIR; if unset it defaults to $HOME/.abc/tmpdir. The
// directory is created with 0700 permissions if it does not already exist.
func uploadTempDir() (string, error) {
	if dir := envvars.Get("ABC_CLI_TMPDIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", fmt.Errorf("create upload temp dir %q: %w", dir, err)
		}
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".abc", "tmpdir")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create upload temp dir %q: %w", dir, err)
	}
	return dir, nil
}
