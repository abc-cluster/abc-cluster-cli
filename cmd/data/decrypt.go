package data

import (
	"fmt"
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
}

func newDecryptCmd() *cobra.Command {
	opts := &decryptOptions{}

	cmd := &cobra.Command{
		Use:   "decrypt <path>",
		Short: "Decrypt a file or folder produced by abc data encrypt",
		Long: `Decrypt a local file or folder produced by rclone-compatible crypt encryption.

By default, decryption uses a key derived from your control-plane session token
(matching the managed encryption path). This requires an authenticated session.

Use --unsafe-local to decrypt with a locally-provided password and salt — required when
the file was encrypted with abc data encrypt --unsafe-local.

  # Managed (default — requires authenticated session, not yet available)
  abc data decrypt ./data.csv.bin

  # Local password — must match the password used during --unsafe-local encryption
  abc data decrypt ./data.csv.bin --unsafe-local --crypt-password "my-secret"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.inputPath = args[0]
			return runDecrypt(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&opts.outputPath, "output", "", "output file path for single-file decryption")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", "", "output directory for folder decryption")
	cmd.Flags().StringVar(&opts.cryptPassword, "crypt-password", "", "rclone crypt password (requires --unsafe-local)")
	cmd.Flags().StringVar(&opts.cryptSalt, "crypt-salt", "", "rclone crypt salt / password2 (requires --unsafe-local)")
	cmd.Flags().BoolVar(&opts.unsafeLocal, "unsafe-local", false,
		"use a locally-provided password instead of the control-plane managed key")

	return cmd
}

func runDecrypt(cmd *cobra.Command, opts *decryptOptions) error {
	info, err := os.Stat(opts.inputPath)
	if err != nil {
		return fmt.Errorf("failed to access path %q: %w", opts.inputPath, err)
	}

	if !opts.unsafeLocal {
		// Managed decryption path — not yet implemented.
		return fmt.Errorf(
			"managed decryption (control-plane key) is not yet available.\n" +
			"To decrypt with a local password, pass --unsafe-local --crypt-password <password>.")
	}

	fmt.Fprintln(cmd.ErrOrStderr(),
		"WARNING: --unsafe-local mode active. Decrypting with locally-provided password (no key management).")

	if opts.cryptPassword == "" {
		return fmt.Errorf("--crypt-password is required in --unsafe-local mode")
	}
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
		return decryptDirectory(cmd, opts.inputPath, opts.outputDir, cryptor)
	}
	return decryptSingleFile(cmd, opts.inputPath, opts.outputPath, cryptor)
}

func decryptSingleFile(cmd *cobra.Command, sourcePath, outputPath string, cryptor *cryptConfig) error {
	if outputPath == "" {
		outputPath = defaultDecryptedPath(sourcePath)
		if _, err := os.Stat(outputPath); err == nil {
			outputPath += ".dec"
		}
	}
	if err := cryptor.decryptToPath(sourcePath, outputPath); err != nil {
		return fmt.Errorf("failed to decrypt %q: %w", sourcePath, err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "File decrypted successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", outputPath)
	return nil
}

func decryptDirectory(cmd *cobra.Command, sourceDir, outputDir string, cryptor *cryptConfig) error {
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
		destPath := filepath.Join(outputDir, defaultDecryptedPath(relPath))
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create output directory %q: %w", filepath.Dir(destPath), err)
		}
		if err := cryptor.decryptToPath(file.path, destPath); err != nil {
			return fmt.Errorf("failed to decrypt %q: %w", relPath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Decrypted %s\n", relPath)
		fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", destPath)
	}
	return nil
}

func defaultDecryptedPath(path string) string {
	if strings.HasSuffix(path, rcloneDefaultSuffix) {
		trimmed := strings.TrimSuffix(path, rcloneDefaultSuffix)
		if trimmed != "" {
			return trimmed
		}
	}
	return path + ".dec"
}
