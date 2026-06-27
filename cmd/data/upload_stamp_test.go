package data

import "testing"

func TestStampManagedMeta(t *testing.T) {
	// managed → stamps group + version + scheme
	m := map[string]string{}
	stampManagedMeta(m, &uploadEncryptor{mode: "managed", group: "mbhg-tbgenomics", version: 3})
	if m["abc-group"] != "mbhg-tbgenomics" || m["abc-key-version"] != "3" || m["abc-enc"] != "age-x25519-managed" {
		t.Fatalf("managed stamp wrong: %+v", m)
	}
	// passphrase → no stamp (native age files are anonymous)
	m2 := map[string]string{}
	stampManagedMeta(m2, &uploadEncryptor{mode: "passphrase"})
	if len(m2) != 0 {
		t.Fatalf("passphrase should not stamp: %+v", m2)
	}
	// nil (raw upload) → no stamp, no panic
	m3 := map[string]string{}
	stampManagedMeta(m3, nil)
	if len(m3) != 0 {
		t.Fatalf("raw should not stamp: %+v", m3)
	}
}
