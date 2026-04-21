package utils

import (
	"context"
	"reflect"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func TestParseAdminServiceCLIArgs_ConfigAndBinary(t *testing.T) {
	cfg, bin, pass, err := ParseAdminServiceCLIArgs([]string{
		"--config", "vault",
		"--binary-location=/tmp/mc",
		"--",
		"alias", "list",
	}, true)
	if err != nil {
		t.Fatalf("ParseAdminServiceCLIArgs: %v", err)
	}
	if cfg != CredConfigVault || bin != "/tmp/mc" {
		t.Fatalf("cfg=%q bin=%q", cfg, bin)
	}
	if !reflect.DeepEqual(pass, []string{"alias", "list"}) {
		t.Fatalf("passthrough=%#v", pass)
	}
}

func TestParseAdminServiceCLIArgs_VaultServiceDisallowsVaultSelection(t *testing.T) {
	_, _, _, err := ParseAdminServiceCLIArgs([]string{"--config=vault"}, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveAdminFloorField_LocalPrecedence(t *testing.T) {
	svc := &config.AdminFloorService{
		Endpoint: "http://top:9000",
		CredSource: &config.AdminFloorCredSource{
			Local: map[string]string{
				"endpoint":   "http://local:9000",
				"access_key": "local-ak",
			},
		},
	}
	v, err := ResolveAdminFloorField(context.Background(), config.Context{}, svc, "minio", CredConfigLocal, "endpoint")
	if err != nil || v != "http://local:9000" {
		t.Fatalf("endpoint err=%v v=%q", err, v)
	}
	v, err = ResolveAdminFloorField(context.Background(), config.Context{}, svc, "minio", CredConfigLocal, "access_key")
	if err != nil || v != "local-ak" {
		t.Fatalf("access_key err=%v v=%q", err, v)
	}
}

func TestResolveAdminFloorField_NomadHybridFallsBackLocalThenTop(t *testing.T) {
	svc := &config.AdminFloorService{
		Endpoint:  "http://top:9000",
		SecretKey: "top-sk",
		CredSource: &config.AdminFloorCredSource{
			Local: map[string]string{"endpoint": "http://local:9000"},
			Nomad: map[string]string{},
		},
	}
	endpoint, err := ResolveAdminFloorField(context.Background(), config.Context{}, svc, "minio", CredConfigNomad, "endpoint")
	if err != nil || endpoint != "http://local:9000" {
		t.Fatalf("endpoint err=%v v=%q", err, endpoint)
	}
	secret, err := ResolveAdminFloorField(context.Background(), config.Context{}, svc, "minio", CredConfigNomad, "secret_key")
	if err != nil || secret != "top-sk" {
		t.Fatalf("secret_key err=%v v=%q", err, secret)
	}
}
