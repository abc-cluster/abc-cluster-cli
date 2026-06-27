package data

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func TestBuildUploadEncryptor(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetErr(io.Discard)
	cmd.SetOut(io.Discard)

	// No --encrypt and no --crypt-password → no encryption (raw upload).
	if enc, err := buildUploadEncryptor(cmd, &abccfg.Config{}, &uploadOptions{}); err != nil || enc != nil {
		t.Fatalf("no flags: want (nil, nil); got (%v, %v)", enc, err)
	}

	// --crypt-password → passphrase mode (age scrypt), one recipient.
	enc, err := buildUploadEncryptor(cmd, &abccfg.Config{}, &uploadOptions{cryptPassword: "pw"})
	if err != nil || enc == nil || enc.mode != "passphrase" || len(enc.recipients) != 1 {
		t.Fatalf("passphrase: got (%+v, %v)", enc, err)
	}

	// --encrypt without a broker context → clear error.
	_, err = buildUploadEncryptor(cmd, &abccfg.Config{}, &uploadOptions{encrypt: true})
	if err == nil || !strings.Contains(err.Error(), "broker") {
		t.Fatalf("--encrypt off a broker tier should error about a broker context; got: %v", err)
	}
}
