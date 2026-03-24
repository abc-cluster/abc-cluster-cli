package data

import (
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
}

func newEncryptCmd() *cobra.Command {
	opts := &encryptOptions{}

	cmd := &cobra.Command{
		Use:   "encrypt <path>",
		Short: "Encrypt a file or folder with rclone-compatible crypt",
		Long: `Encrypt a local file or folder using the rclone crypt format.

The output is compatible with rclone's crypt backend when configured with the same password and salt.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.inputPath = args[0]
			return runEncrypt(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&opts.outputPath, "output", "", "output file path for single-file encryption")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", "", "output directory for folder encryption")
	cmd.Flags().StringVar(&opts.cryptPassword, "crypt-password", "", "rclone crypt password for client-side encryption")
	cmd.Flags().StringVar(&opts.cryptSalt, "crypt-salt", "", "rclone crypt salt (password2) for client-side encryption")

	return cmd
}

func runEncrypt(cmd *cobra.Command, opts *encryptOptions) error {
	info, err := os.Stat(opts.inputPath)
	if err != nil {
		return fmt.Errorf("failed to access path %q: %w", opts.inputPath, err)
	}
	if opts.cryptPassword == "" {
		return fmt.Errorf("crypt-password is required for encryption")
	}
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
		return encryptDirectory(cmd, opts.inputPath, opts.outputDir, cryptor)
	}
	return encryptSingleFile(cmd, opts.inputPath, opts.outputPath, cryptor)
}

func encryptSingleFile(cmd *cobra.Command, sourcePath, outputPath string, cryptor *cryptConfig) error {
	if outputPath == "" {
		outputPath = sourcePath + rcloneDefaultSuffix
	}
	if err := cryptor.encryptToPath(sourcePath, outputPath); err != nil {
		return fmt.Errorf("failed to encrypt %q: %w", sourcePath, err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "File encrypted successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", outputPath)
	return nil
}

func encryptDirectory(cmd *cobra.Command, sourceDir, outputDir string, cryptor *cryptConfig) error {
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
		if err := cryptor.encryptToPath(file.path, destPath); err != nil {
			return fmt.Errorf("failed to encrypt %q: %w", relPath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Encrypted %s\n", relPath)
		fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", destPath)
	}
	return nil
}
