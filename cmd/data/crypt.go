package data

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/nacl/secretbox"
	"golang.org/x/crypto/scrypt"
)

const (
	rcloneFileMagic       = "RCLONE\x00\x00"
	rcloneFileNonceSize   = 24
	rcloneFileHeaderSize  = len(rcloneFileMagic) + rcloneFileNonceSize
	rcloneBlockDataSize   = 64 * 1024
	rcloneBlockHeaderSize = secretbox.Overhead
	rcloneDefaultSuffix   = ".encrypted"
)

var defaultSalt = []byte{0xA8, 0x0D, 0xF4, 0x3A, 0x8F, 0xBD, 0x03, 0x08, 0xA7, 0xCA, 0xB8, 0x3E, 0x58, 0x1F, 0x86, 0xB1}

type cryptConfig struct {
	dataKey [32]byte
	rand    io.Reader
}

type nonce [rcloneFileNonceSize]byte

func (n *nonce) pointer() *[rcloneFileNonceSize]byte {
	return (*[rcloneFileNonceSize]byte)(n)
}

func (n *nonce) increment() {
	for i := range n {
		n[i]++
		if n[i] != 0 {
			break
		}
	}
}

func newCryptConfig(password, salt string, randSource io.Reader) (*cryptConfig, error) {
	keySize := 32 + 32 + 16
	var saltBytes = defaultSalt
	if salt != "" {
		saltBytes = []byte(salt)
	}
	var key []byte
	if password == "" {
		key = make([]byte, keySize)
	} else {
		derived, err := scrypt.Key([]byte(password), saltBytes, 16384, 8, 1, keySize)
		if err != nil {
			return nil, err
		}
		key = derived
	}
	var dataKey [32]byte
	copy(dataKey[:], key[:32])
	if randSource == nil {
		randSource = rand.Reader
	}
	return &cryptConfig{dataKey: dataKey, rand: randSource}, nil
}

func uploadCryptConfig(password, salt string) (*cryptConfig, error) {
	if password == "" && salt == "" {
		return nil, nil
	}
	if password == "" {
		return nil, fmt.Errorf("crypt-password is required when crypt-salt is set")
	}
	return newCryptConfig(password, salt, nil)
}

func encryptForUpload(sourcePath string, cryptor *cryptConfig, onProgress func(int64)) (string, func() error, error) {
	if cryptor == nil {
		return sourcePath, nil, nil
	}
	return cryptor.encryptToTempFileWithProgress(sourcePath, onProgress)
}

func encryptedSize(plainSize int64) int64 {
	if plainSize <= 0 {
		return int64(rcloneFileHeaderSize)
	}
	blocks := (plainSize + rcloneBlockDataSize - 1) / rcloneBlockDataSize
	return int64(rcloneFileHeaderSize) + plainSize + blocks*int64(rcloneBlockHeaderSize)
}

func (c *cryptConfig) encryptToTempFile(sourcePath string) (string, func() error, error) {
	return c.encryptToTempFileWithProgress(sourcePath, nil)
}

func (c *cryptConfig) encryptToTempFileWithProgress(sourcePath string, onProgress func(int64)) (string, func() error, error) {
	tmp, err := os.CreateTemp("", "abc-crypt-*")
	if err != nil {
		return "", nil, err
	}
	tmpPath := tmp.Name()
	cleanup := func() error {
		return os.Remove(tmpPath)
	}
	if err := c.encryptToWriterWithProgress(sourcePath, tmp, onProgress); err != nil {
		tmp.Close()
		_ = cleanup()
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	return tmpPath, cleanup, nil
}

func (c *cryptConfig) encryptToPath(sourcePath, destPath string) error {
	return c.encryptToPathWithProgress(sourcePath, destPath, nil)
}

func (c *cryptConfig) encryptToPathWithProgress(sourcePath, destPath string, onProgress func(int64)) error {
	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := c.encryptToWriterWithProgress(sourcePath, out, onProgress); err != nil {
		out.Close()
		_ = os.Remove(destPath)
		return err
	}
	return out.Close()
}

func (c *cryptConfig) decryptToPath(sourcePath, destPath string) error {
	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := c.decryptToWriter(sourcePath, out); err != nil {
		out.Close()
		_ = os.Remove(destPath)
		return err
	}
	return out.Close()
}

func (c *cryptConfig) encryptToWriter(sourcePath string, out io.Writer) error {
	return c.encryptToWriterWithProgress(sourcePath, out, nil)
}

func (c *cryptConfig) encryptToWriterWithProgress(sourcePath string, out io.Writer, onProgress func(int64)) error {
	in, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer in.Close()
	_, err = c.encryptStreamWithProgress(out, in, onProgress)
	return err
}

func (c *cryptConfig) decryptToWriter(sourcePath string, out io.Writer) error {
	in, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer in.Close()
	_, err = c.decryptStream(out, in)
	return err
}

func (c *cryptConfig) encryptStream(dst io.Writer, src io.Reader) (int64, error) {
	return c.encryptStreamWithProgress(dst, src, nil)
}

func (c *cryptConfig) encryptStreamWithProgress(dst io.Writer, src io.Reader, onProgress func(int64)) (int64, error) {
	var n nonce
	if _, err := io.ReadFull(c.rand, n[:]); err != nil {
		return 0, err
	}
	written := int64(0)
	header := make([]byte, 0, rcloneFileHeaderSize)
	header = append(header, rcloneFileMagic...)
	header = append(header, n[:]...)
	nw, err := writeAll(dst, header)
	written += int64(nw)
	if err != nil {
		return written, err
	}
	buf := make([]byte, rcloneBlockDataSize)
	for {
		read, err := io.ReadFull(src, buf)
		if err == io.EOF {
			break
		}
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return written, err
		}
		if read > 0 {
			if onProgress != nil {
				onProgress(int64(read))
			}
			sealed := secretbox.Seal(nil, buf[:read], n.pointer(), &c.dataKey)
			nw, err := writeAll(dst, sealed)
			written += int64(nw)
			if err != nil {
				return written, err
			}
			n.increment()
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
	}
	return written, nil
}

func (c *cryptConfig) decryptStream(dst io.Writer, src io.Reader) (int64, error) {
	header := make([]byte, rcloneFileHeaderSize)
	if _, err := io.ReadFull(src, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, fmt.Errorf("invalid encrypted file: missing header")
		}
		return 0, err
	}
	if string(header[:len(rcloneFileMagic)]) != rcloneFileMagic {
		return 0, fmt.Errorf("invalid encrypted file: unexpected header")
	}

	var n nonce
	copy(n[:], header[len(rcloneFileMagic):])

	block := make([]byte, rcloneBlockDataSize+rcloneBlockHeaderSize)
	written := int64(0)
	for {
		read, err := io.ReadFull(src, block)
		if err == io.EOF {
			break
		}
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return written, err
		}
		if read > 0 {
			plain, ok := secretbox.Open(nil, block[:read], n.pointer(), &c.dataKey)
			if !ok {
				return written, fmt.Errorf("decrypt failed: invalid password/salt or corrupted data")
			}
			nw, err := writeAll(dst, plain)
			written += int64(nw)
			if err != nil {
				return written, err
			}
			n.increment()
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
	}
	return written, nil
}

func writeAll(dst io.Writer, data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		n, err := dst.Write(data)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
		data = data[n:]
	}
	return total, nil
}
