// Command age-plugin-abc is the age plugin for abc managed (broker-KEK)
// encryption (ADR-0067 managed mode). It lets stock `age` produce and open the
// SAME files as `abc data encrypt/decrypt`, so users can choose either tool:
//
//	# decrypt a managed file (plugin self-configures from ~/.abc/config.yaml)
//	age -d -j abc file.age
//
//	# encrypt to your group (recipient materialized by the abc CLI)
//	age -e -R ~/.abc/age/recipients.txt file
//
// The crypto + the broker /keys/get client are the SAME packages the abc CLI
// uses (internal/abccrypt + internal/keysource), so there is no second
// implementation and no drift: a file is a file, whichever tool wrote it.
//
// Protocol plumbing (recipient-v1 / identity-v1 state machines, the AGE-PLUGIN
// framing, the age1abc1…/AGE-PLUGIN-ABC-1… encodings) is handled by
// filippo.io/age/plugin — this binary only maps a recipient/identity to the
// shared abc recipient/identity backed by a config-loaded KEK provider.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"filippo.io/age"
	"filippo.io/age/plugin"

	"github.com/abc-cluster/abc-cluster-cli/internal/abccrypt"
	"github.com/abc-cluster/abc-cluster-cli/internal/keysource"
)

func main() {
	p, err := plugin.New("abc")
	if err != nil {
		fmt.Fprintln(os.Stderr, "age-plugin-abc:", err)
		os.Exit(1)
	}
	p.RegisterFlags(flag.CommandLine)

	// Recipient: age1abc1<kek_id>. The data is the canonical kek_id ("group:<g>")
	// the file should be wrapped to. The broker still re-derives + authorizes the
	// caller's own group at /keys/get, so this is a target label, not authority.
	p.HandleRecipient(func(data []byte) (age.Recipient, error) {
		kekID := string(data)
		prov, perr := keysource.FromActiveContext(context.Background())
		if perr != nil {
			return nil, perr
		}
		return abccrypt.NewABCRecipient(kekID, prov)
	})

	// Identity: AGE-PLUGIN-ABC-1… or `-j abc` (empty data). Either way the abc
	// identity needs no embedded secret — it reads the broker endpoint + opaque
	// token from ~/.abc and unwraps using the kek_id+version the file's stanza
	// names. So empty data is the normal, expected case.
	p.HandleIdentity(func(data []byte) (age.Identity, error) {
		prov, perr := keysource.FromActiveContext(context.Background())
		if perr != nil {
			return nil, perr
		}
		return abccrypt.NewABCIdentity(prov), nil
	})

	// Identity-as-recipient: `age -e -j abc <file>` encrypts to the caller's OWN
	// group — fully keyless, no recipients file. The recipient's kek_id is the
	// caller's own group, resolved from the broker (OwnKekID).
	p.HandleIdentityAsRecipient(func(data []byte) (age.Recipient, error) {
		prov, perr := keysource.FromActiveContext(context.Background())
		if perr != nil {
			return nil, perr
		}
		kekID, perr := prov.OwnKekID()
		if perr != nil {
			return nil, perr
		}
		return abccrypt.NewABCRecipient(kekID, prov)
	})

	flag.Parse()
	os.Exit(p.Main())
}
