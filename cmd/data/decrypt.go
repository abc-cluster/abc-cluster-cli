package data

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type decryptOptions struct {
	inputPath     string
	outputPath    string
	outputDir     string
	cryptPassword string
	cryptSalt     string
	unsafeLocal   bool
	force         bool
	removeSource  bool
}

func newDecryptCmd() *cobra.Command {
	opts := &decryptOptions{}

	cmd := &cobra.Command{
		Use:   "decrypt <path>",
		Short: "Decrypt a file or folder produced by abc data encrypt",
		Long: `Decrypt a local file or folder produced by rclone-compatible crypt encryption.

By default, decryption uses a key derived from your control-plane session token
(matching the managed encryption path). This requires an authenticated session.

Use --crypt-password to decrypt with a locally-provided password — required when
the file was encrypted with abc data encrypt --crypt-password. Credentials are
stored in ~/.abc/config.yaml for reuse in future encryption/decryption operations.

  # Managed (default — requires authenticated session, not yet available)
  abc data decrypt ./data.csv.bin

  # Local password — credentials stored in config for future use
  abc data decrypt ./data.csv.bin --crypt-password "my-secret"

  # Explicit local mode using stored config credentials
  abc data decrypt ./data.csv.bin --unsafe-local`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.inputPath = args[0]
			return runDecrypt(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&opts.outputPath, "output", "", "output file path for single-file decryption")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", "", "output directory for folder decryption")
	cmd.Flags().StringVar(&opts.cryptPassword, "crypt-password", "", "rclone crypt password (stored in config for future use)")
	cmd.Flags().StringVar(&opts.cryptSalt, "crypt-salt", "", "rclone crypt salt / password2 (optional; only used with --crypt-password)")
	cmd.Flags().BoolVar(&opts.unsafeLocal, "unsafe-local", false,
		"use locally-managed crypt credentials from config; if password/salt are provided, they are written to config if missing")
	cmd.Flags().BoolVarP(&opts.force, "force", "f", false,
		"overwrite the output file if it already exists — sha256-compares old vs new; identical content is a no-op (default: refuse)")
	cmd.Flags().BoolVar(&opts.removeSource, "remove-source", false,
		"remove the source file after successful decryption (frees disk; the encrypted source is gone)")

	return cmd
}

func runDecrypt(cmd *cobra.Command, opts *decryptOptions) error {
	info, err := os.Stat(opts.inputPath)
	if err != nil {
		return fmt.Errorf("failed to access path %q: %w", opts.inputPath, err)
	}

	// Load config to manage crypt credentials
	cfg, err := loadOrCreateConfig()
	if err != nil {
		return err
	}

	passwordProvided := opts.cryptPassword != ""
	saltProvided := opts.cryptSalt != ""

	ctxName, ctx, ctxErr := cfg.ContextForSecrets()
	storedPW, storedSalt := "", ""
	if ctxErr == nil {
		storedPW, storedSalt = ctx.Crypt.Password, ctx.Crypt.Salt
	}

	configChanged := false
	if passwordProvided {
		if storedPW != "" {
			if storedPW != opts.cryptPassword {
				return fmt.Errorf(
					"a different crypt password is already stored in the config file.\n" +
						"  - to use the stored one: rerun without --crypt-password\n" +
						"  - to switch: edit ~/.abc/config.yaml under contexts.<ctx>.crypt.password,\n" +
						"    then rerun with the new --crypt-password.")
			}
		} else {
			if ctxErr != nil {
				return fmt.Errorf(
					"cannot save crypt password without a saved context: %w\n"+
						"Run abc auth login (or add a context) and abc context use <name>, then retry", ctxErr)
			}
			ctx.Crypt.Password = opts.cryptPassword
			configChanged = true
		}
	}
	if saltProvided {
		if storedSalt != "" {
			if storedSalt != opts.cryptSalt {
				return fmt.Errorf(
					"a different crypt salt is already stored in the config file.\n" +
						"  - to use the stored one: rerun without --crypt-salt\n" +
						"  - to switch: edit ~/.abc/config.yaml under contexts.<ctx>.crypt.salt,\n" +
						"    then rerun with the new --crypt-salt.")
			}
		} else {
			if ctxErr != nil {
				return fmt.Errorf(
					"cannot save crypt salt without a saved context: %w\n"+
						"Run abc auth login (or add a context) and abc context use <name>, then retry", ctxErr)
			}
			ctx.Crypt.Salt = opts.cryptSalt
			configChanged = true
		}
	}
	if configChanged {
		cfg.Contexts[ctxName] = ctx
		if err := cfg.Save(); err != nil {
			return err
		}
	}

	if opts.unsafeLocal {
		if ctxErr != nil {
			return fmt.Errorf("--unsafe-local requires a saved context: %w", ctxErr)
		}
		if ctx.Crypt.Password == "" {
			return fmt.Errorf("--crypt-password is required in --unsafe-local mode")
		}
		opts.cryptPassword = ctx.Crypt.Password
		opts.cryptSalt = ctx.Crypt.Salt
	} else if !passwordProvided {
		if ctxErr == nil && ctx.Crypt.Password != "" {
			opts.cryptPassword = ctx.Crypt.Password
			opts.cryptSalt = ctx.Crypt.Salt
		} else {
			return fmt.Errorf(
				"managed decryption (control-plane key) is not yet available.\n" +
					"To decrypt with a local password, pass --crypt-password <password>.")
		}
	}

	fmt.Fprintln(cmd.ErrOrStderr(),
		"WARNING: local decryption active. Decrypting with locally-provided password (no key management).")
	if opts.outputPath != "" && info.IsDir() {
		return fmt.Errorf("--output can only be used when decrypting a single file")
	}
	if opts.outputDir != "" && !info.IsDir() {
		return fmt.Errorf("--output-dir can only be used when decrypting a directory")
	}

	cryptor, err := newCryptConfig(opts.cryptPassword, opts.cryptSalt, nil)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return decryptDirectory(cmd, opts.inputPath, opts.outputDir, cryptor, opts.force, opts.removeSource)
	}
	return decryptSingleFile(cmd, opts.inputPath, opts.outputPath, cryptor, opts.force, opts.removeSource)
}

// resolveDecryptOutput chooses the output path for a decrypt:
//   - if outputPath was passed explicitly → use it verbatim
//   - else strip the .encrypted suffix from sourcePath → the clean restored name
//   - else error (no silent ".dec" fallback; devon B1)
func resolveDecryptOutput(sourcePath, outputPath string) (string, error) {
	if outputPath != "" {
		return outputPath, nil
	}
	clean, ok := defaultDecryptedPath(sourcePath)
	if !ok {
		return "", fmt.Errorf(
			"cannot determine output path: %q has no recognised crypt suffix (expected %q)\n"+
				"  pass --output <path> to specify where to write the decrypted file",
			sourcePath, rcloneDefaultSuffix)
	}
	return clean, nil
}

func decryptSingleFile(cmd *cobra.Command, sourcePath, outputPath string, cryptor *cryptConfig, force, removeSource bool) error {
	outputPath, err := resolveDecryptOutput(sourcePath, outputPath)
	if err != nil {
		return err
	}
	err = writeWithCollisionCheck(outputPath, force, cmd.ErrOrStderr(),
		func(p string) error { return cryptor.decryptToPath(sourcePath, p) })
	if err != nil {
		return fmt.Errorf("failed to decrypt %q: %w", sourcePath, err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "File decrypted successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", outputPath)
	if removeSource {
		if err := os.Remove(sourcePath); err != nil {
			return fmt.Errorf("decrypt succeeded but failed to --remove-source %q: %w", sourcePath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Removed source: %s\n", sourcePath)
	}
	return nil
}

func decryptDirectory(cmd *cobra.Command, sourceDir, outputDir string, cryptor *cryptConfig, force, removeSource bool) error {
	files, err := collectFiles(sourceDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files found in directory %q", sourceDir)
	}
	if outputDir == "" {
		outputDir = sourceDir + "-decrypted"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Decrypting %d files...\n", len(files))
	for _, file := range files {
		relPath, err := filepath.Rel(sourceDir, file.path)
		if err != nil {
			return fmt.Errorf("failed to resolve path for %q: %w", file.path, err)
		}
		// Strip the suffix per-file; if a file in the tree lacks the suffix,
		// keep its name (no ".dec" — we do not invent a destination).
		baseName := relPath
		if clean, ok := defaultDecryptedPath(relPath); ok {
			baseName = clean
		}
		destPath := filepath.Join(outputDir, baseName)
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create output directory %q: %w", filepath.Dir(destPath), err)
		}
		if err := writeWithCollisionCheck(destPath, force, cmd.ErrOrStderr(),
			func(p string) error { return cryptor.decryptToPath(file.path, p) }); err != nil {
			return fmt.Errorf("failed to decrypt %q: %w", relPath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Decrypted %s\n", relPath)
		fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", destPath)
		if removeSource {
			if err := os.Remove(file.path); err != nil {
				return fmt.Errorf("decrypt succeeded but failed to --remove-source %q: %w", file.path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  Removed source: %s\n", file.path)
		}
	}
	return nil
}

// writeWithCollisionCheck writes a file via `writer(path)` with content-aware
// overwrite handling:
//
//   - If destPath doesn't exist → call writer(destPath) directly.
//   - If destPath exists and !force → error (refuse silent clobber; devon B1+B2).
//   - If destPath exists and force → write to a sibling temp file, sha256-hash
//     both old and new, then:
//     • identical → keep the existing file (no-op; remove temp)
//     • different → atomic os.Rename(temp, destPath) and emit a stderr line
//     showing both hashes so the destructive overwrite is auditable
//     (devon: "do a hashsum, not just rely on the name").
//
// The temp file lives next to destPath so the atomic rename is on the same
// filesystem. Failures clean up the temp.
func writeWithCollisionCheck(destPath string, force bool, warnOut io.Writer, writer func(path string) error) error {
	st, err := os.Stat(destPath)
	if err != nil {
		if os.IsNotExist(err) {
			return writer(destPath)
		}
		return err
	}
	if !force {
		return fmt.Errorf(
			"refusing to overwrite existing file: %s\n"+
				"  pass --output <other-path> to write somewhere else, or --force to overwrite\n"+
				"  (--force also sha256-compares old vs new; identical content is a no-op)",
			destPath)
	}

	// Force path: temp-write then content-compare.
	tmpPath := destPath + ".abc-write.tmp"
	_ = os.Remove(tmpPath) // best-effort leftover cleanup from a prior crash
	if err := writer(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	oldHash, hErr := sha256File(destPath)
	if hErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("hash existing %s: %w", destPath, hErr)
	}
	newHash, hErr := sha256File(tmpPath)
	if hErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("hash new content: %w", hErr)
	}
	if oldHash == newHash {
		_ = os.Remove(tmpPath)
		if warnOut != nil {
			fmt.Fprintf(warnOut, "  [no-op] %s already has this content (sha256: %s) — kept existing\n",
				destPath, oldHash)
		}
		_ = st // existing file untouched
		return nil
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic overwrite %s: %w", destPath, err)
	}
	if warnOut != nil {
		fmt.Fprintf(warnOut, "  [overwrote] %s\n    old sha256: %s\n    new sha256: %s\n",
			destPath, oldHash, newHash)
	}
	return nil
}

// sha256File returns the hex sha256 digest of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// defaultDecryptedPath strips the rclone-crypt default suffix to restore the
// original name. Returns ("", false) when the input has no recognisable
// suffix — callers must require an explicit --output in that case rather than
// invent one (no silent ".dec" fallback; devon B1).
func defaultDecryptedPath(path string) (string, bool) {
	if !strings.HasSuffix(path, rcloneDefaultSuffix) {
		return "", false
	}
	trimmed := strings.TrimSuffix(path, rcloneDefaultSuffix)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}
