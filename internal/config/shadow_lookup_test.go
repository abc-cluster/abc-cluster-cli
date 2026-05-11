package config

import "testing"

func TestAdminFloorFieldValue_LocalCredSourceMap(t *testing.T) {
	ctx := Context{
		Admin: Admin{
			Services: AdminServices{
				MinIO: &AdminFloorService{
					CredSource: &AdminFloorCredSource{
						Local: map[string]string{"user": "alice", "password": "s3cret"},
					},
				},
			},
		},
	}
	v, ok := AdminFloorFieldValue(ctx, "minio", "local", "user")
	if !ok || v != "alice" {
		t.Errorf("local.user = (%q, %v); want (alice, true)", v, ok)
	}
	v, ok = AdminFloorFieldValue(ctx, "minio", "local", "password")
	if !ok || v != "s3cret" {
		t.Errorf("local.password = (%q, %v); want (s3cret, true)", v, ok)
	}
}

func TestAdminFloorFieldValue_LocalFallsBackToInlineField(t *testing.T) {
	ctx := Context{
		Admin: Admin{
			Services: AdminServices{
				MinIO: &AdminFloorService{
					User: "inline-alice",
					// no cred_source.local map
				},
			},
		},
	}
	v, ok := AdminFloorFieldValue(ctx, "minio", "local", "user")
	if !ok || v != "inline-alice" {
		t.Errorf("local.user with inline fallback = (%q, %v); want (inline-alice, true)", v, ok)
	}
}

func TestAdminFloorFieldValue_NomadReturnsReferenceUnresolved(t *testing.T) {
	ctx := Context{
		Admin: Admin{
			Services: AdminServices{
				MinIO: &AdminFloorService{
					CredSource: &AdminFloorCredSource{
						Nomad: map[string]string{
							"user": "nomad+var@abc-services/minio#root_user",
						},
					},
				},
			},
		},
	}
	v, ok := AdminFloorFieldValue(ctx, "minio", "nomad", "user")
	if !ok || v != "nomad+var@abc-services/minio#root_user" {
		t.Errorf("nomad.user should return raw ref; got (%q, %v)", v, ok)
	}
	if !IsReferenceString(v) {
		t.Errorf("IsReferenceString(%q) = false; want true", v)
	}
}

func TestAdminFloorFieldValue_VaultReturnsReferenceUnresolved(t *testing.T) {
	ctx := Context{
		Admin: Admin{
			Services: AdminServices{
				MinIO: &AdminFloorService{
					CredSource: &AdminFloorCredSource{
						Vault: map[string]string{
							"password": "vault+kv2@kv/data/secrets/minio#root_password",
						},
					},
				},
			},
		},
	}
	v, ok := AdminFloorFieldValue(ctx, "minio", "vault", "password")
	if !ok || v != "vault+kv2@kv/data/secrets/minio#root_password" {
		t.Errorf("vault.password should return raw ref; got (%q, %v)", v, ok)
	}
	if !IsReferenceString(v) {
		t.Errorf("IsReferenceString(%q) = false; want true", v)
	}
}

func TestAdminFloorFieldValue_UnknownService(t *testing.T) {
	ctx := Context{}
	_, ok := AdminFloorFieldValue(ctx, "bogus-service", "local", "user")
	if ok {
		t.Error("unknown service should return ok=false")
	}
}

func TestContextDirectFieldValue_CryptPassword(t *testing.T) {
	ctx := Context{Crypt: ContextCrypt{Password: "secret-pw", Salt: "abc"}}
	v, ok := ContextDirectFieldValue(ctx, "crypt.password")
	if !ok || v != "secret-pw" {
		t.Errorf("crypt.password = (%q, %v); want (secret-pw, true)", v, ok)
	}
	v, ok = ContextDirectFieldValue(ctx, "crypt.salt")
	if !ok || v != "abc" {
		t.Errorf("crypt.salt = (%q, %v); want (abc, true)", v, ok)
	}
}

func TestContextDirectFieldValue_UploadFields(t *testing.T) {
	ctx := Context{
		UploadEndpoint: "https://up.example",
		UploadToken:    "uptok",
	}
	v, _ := ContextDirectFieldValue(ctx, "upload_endpoint")
	if v != "https://up.example" {
		t.Errorf("upload_endpoint = %q; want https://up.example", v)
	}
	v, _ = ContextDirectFieldValue(ctx, "upload_token")
	if v != "uptok" {
		t.Errorf("upload_token = %q; want uptok", v)
	}
}

func TestContextDirectFieldValue_UnknownPath(t *testing.T) {
	ctx := Context{Crypt: ContextCrypt{Password: "x"}}
	_, ok := ContextDirectFieldValue(ctx, "totally.unknown.path")
	if ok {
		t.Error("unknown path should return ok=false")
	}
}

func TestIsReferenceString(t *testing.T) {
	cases := map[string]bool{
		"":                                 false,
		"alice":                            false,
		"nomad+var@ns/path#key":            true,
		"vault+kv2@kv/data/secrets#field":  true,
		"some random string":               false,
		"  nomad+var@ns/path#key  ":        true, // trimmed
	}
	for v, want := range cases {
		if got := IsReferenceString(v); got != want {
			t.Errorf("IsReferenceString(%q) = %v; want %v", v, got, want)
		}
	}
}
