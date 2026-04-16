package data

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

type encryptOptions struct {
	inputPath     string
	outputPath    string
	outputDir     string
	cryptPassword string
	cryptSalt     string
	unsafeLocal   bool
	progress      bool
}

func newEncryptCmd() *cobra.Command {
	opts := &encryptOptions{}

	cmd := &cobra.Command{
		Use:   "encrypt <path>",
		Short: "Encrypt a file or folder with rclone-compatible crypt",
		Long: `Encrypt a local file or folder using the rclone crypt format.

By default, encryption uses a key derived from your control-plane session token,
which provides managed key storage and recovery. This requires an authenticated session.

Use --crypt-password to encrypt with a locally-provided password instead.
In local mode, the key is not managed by the control plane — if you lose your
password, your data cannot be recovered. Credentials are stored in ~/.abc/config.yaml
for reuse in future encryption/decryption operations.

  # Managed (default — requires authenticated session, not yet available)
  abc data encrypt ./data.csv

  # Local password — credentials stored in config for future use
  abc data encrypt ./data.csv --crypt-password "my-secret"

  # Explicit local mode using stored config credentials
  abc data encrypt ./data.csv --unsafe-local`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.inputPath = args[0]
			return runEncrypt(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&opts.outputPath, "output", "", "output file path for single-file encryption")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", "", "output directory for folder encryption")
	cmd.Flags().StringVar(&opts.cryptPassword, "crypt-password", "", "rclone crypt password (stored in config for future use)")
	cmd.Flags().StringVar(&opts.cryptSalt, "crypt-salt", "", "rclone crypt salt / password2 (optional; only used with --crypt-password)")
	cmd.Flags().BoolVar(&opts.unsafeLocal, "unsafe-local", false,
		"use locally-managed crypt credentials from config; if password/salt are provided, they are written to config if missing")
	cmd.Flags().BoolVar(&opts.progress, "progress", true, "show live progress bars for encryption")

	return cmd
}

func runEncrypt(cmd *cobra.Command, opts *encryptOptions) error {
	info, err := os.Stat(opts.inputPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return inputError("path %q does not exist; verify the path and try again", opts.inputPath)
		}
		if errors.Is(err, os.ErrPermission) {
			return inputError("permission denied while accessing %q; check file permissions", opts.inputPath)
		}
		return localIOError("failed to access path %q: %w", opts.inputPath, err)
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
				return inputError(
					"crypt password already exists in config file.\n" +
						"Remove it first (by editing ~/.abc/config.yaml) or use the stored password.")
			}
		} else {
			if ctxErr != nil {
				return inputError(
					"cannot save crypt password without a saved context: %v\n"+
						"Run abc auth login (or add a context) and abc context use <name>, then retry.", ctxErr)
			}
			ctx.Crypt.Password = opts.cryptPassword
			configChanged = true
		}
	}
	if saltProvided {
		if storedSalt != "" {
			if storedSalt != opts.cryptSalt {
				return inputError(
					"crypt salt already exists in config file.\n" +
						"Remove it first (by editing ~/.abc/config.yaml) or use the stored salt.")
			}
		} else {
			if ctxErr != nil {
				return inputError(
					"cannot save crypt salt without a saved context: %v\n"+
						"Run abc auth login (or add a context) and abc context use <name>, then retry.", ctxErr)
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
			return inputError("--unsafe-local requires a saved context; %v", ctxErr)
		}
		if ctx.Crypt.Password == "" {
			return inputError("--crypt-password is required in --unsafe-local mode")
		}
		opts.cryptPassword = ctx.Crypt.Password
		opts.cryptSalt = ctx.Crypt.Salt
	} else if !passwordProvided {
		if ctxErr == nil && ctx.Crypt.Password != "" {
			opts.cryptPassword = ctx.Crypt.Password
			opts.cryptSalt = ctx.Crypt.Salt
		} else {
			return inputError(
				"managed encryption (control-plane key) is not yet available.\n" +
					"To encrypt with a local password, pass --crypt-password <password>.\n" +
					"WARNING: in local mode the key is not managed — losing your password means losing your data.")
		}
	}

	fmt.Fprintln(cmd.ErrOrStderr(),
		"WARNING: local encryption active. Encryption key is NOT managed by the control plane.")
	fmt.Fprintln(cmd.ErrOrStderr(),
		"         If you lose your password, your data cannot be recovered.")
	if opts.outputPath != "" && info.IsDir() {
		return fmt.Errorf("--output can only be used when encrypting a single file")
	}
	if opts.outputDir != "" && !info.IsDir() {
		return fmt.Errorf("--output-dir can only be used when encrypting a directory")
	}

	cryptor, err := newCryptConfig(opts.cryptPassword, opts.cryptSalt, nil)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return encryptDirectory(cmd, opts.inputPath, opts.outputDir, cryptor, opts.progress)
	}
	return encryptSingleFile(cmd, opts.inputPath, opts.outputPath, cryptor, opts.progress)
}

func encryptSingleFile(cmd *cobra.Command, sourcePath, outputPath string, cryptor *cryptConfig, progressEnabled bool) error {
	if outputPath == "" {
		outputPath = sourcePath + rcloneDefaultSuffix
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to access path %q: %w", sourcePath, err)
	}
	progress := newProgressReporter(cmd.OutOrStdout(), progressEnabled, fmt.Sprintf("Encrypting %s", filepath.Base(sourcePath)), info.Size())
	if err := cryptor.encryptToPathWithProgress(cmd.Context(), sourcePath, outputPath, func(n int64) {
		progress.Add(n)
	}); err != nil {
		_ = progress.Complete()
		return fmt.Errorf("failed to encrypt %q: %w", sourcePath, err)
	}
	if doneErr := progress.Complete(); doneErr != nil {
		return fmt.Errorf("failed to render encryption progress: %w", doneErr)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "File encrypted successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", outputPath)
	return nil
}

func encryptDirectory(cmd *cobra.Command, sourceDir, outputDir string, cryptor *cryptConfig, progressEnabled bool) error {
	files, err := collectFiles(sourceDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files found in directory %q", sourceDir)
	}
	if outputDir == "" {
		outputDir = sourceDir + "-encrypted"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Encrypting %d files...\n", len(files))
	for _, file := range files {
		relPath, err := filepath.Rel(sourceDir, file.path)
		if err != nil {
			return fmt.Errorf("failed to resolve path for %q: %w", file.path, err)
		}
		destPath := filepath.Join(outputDir, relPath) + rcloneDefaultSuffix
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create output directory %q: %w", filepath.Dir(destPath), err)
		}
		progress := newProgressReporter(cmd.OutOrStdout(), progressEnabled, fmt.Sprintf("Encrypting %s", relPath), file.size)
		if err := cryptor.encryptToPathWithProgress(cmd.Context(), file.path, destPath, func(n int64) {
			progress.Add(n)
		}); err != nil {
			_ = progress.Complete()
			return fmt.Errorf("failed to encrypt %q: %w", relPath, err)
		}
		if doneErr := progress.Complete(); doneErr != nil {
			return fmt.Errorf("failed to render encryption progress: %w", doneErr)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Encrypted %s\n", relPath)
		fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", destPath)
	}
	return nil
}
