package config

import "testing"

func TestSetAdminFloorField_PreservesSisterFields(t *testing.T) {
	var s AdminServices
	if err := SetAdminFloorField(&s, "minio", "access_key", "my-ak"); err != nil {
		t.Fatal(err)
	}
	if err := SetAdminFloorField(&s, "minio", "secret_key", "my-sk"); err != nil {
		t.Fatal(err)
	}
	if err := SetAdminFloorField(&s, "minio", "endpoint", "http://127.0.0.1:9000"); err != nil {
		t.Fatal(err)
	}
	v, ok := GetAdminFloorField(&s, "minio", "access_key")
	if !ok || v != "my-ak" {
		t.Fatalf("access_key: ok=%v v=%q", ok, v)
	}
	v, ok = GetAdminFloorField(&s, "minio", "secret_key")
	if !ok || v != "my-sk" {
		t.Fatalf("secret_key: ok=%v v=%q", ok, v)
	}
	v, ok = GetAdminFloorField(&s, "minio", "endpoint")
	if !ok || v != "http://127.0.0.1:9000" {
		t.Fatalf("endpoint: ok=%v v=%q", ok, v)
	}
}

func TestAdminFloorService_IsEmpty_RespectsCredentials(t *testing.T) {
	a := &AdminFloorService{AccessKey: "x"}
	if a.IsEmpty() {
		t.Fatal("non-empty due to access_key")
	}
}

func TestAdminFloorService_IsEmpty_RespectsCredSource(t *testing.T) {
	a := &AdminFloorService{
		CredSource: &AdminFloorCredSource{
			Local: map[string]string{"access_key": "x"},
		},
	}
	if a.IsEmpty() {
		t.Fatal("non-empty due to cred_source.local")
	}
}

func TestUnsetAdminFloorField_RemovesBlockWhenOnlyCredential(t *testing.T) {
	var s AdminServices
	_ = SetAdminFloorField(&s, "minio", "access_key", "x")
	_ = UnsetAdminFloorField(&s, "minio", "access_key")
	if s.MinIO != nil {
		t.Fatalf("want nil block, got %+v", s.MinIO)
	}
}

func TestGetAdminFloorField_CredSourceLocalWhenTopEmpty(t *testing.T) {
	s := AdminServices{
		MinIO: &AdminFloorService{
			CredSource: &AdminFloorCredSource{
				Local: map[string]string{"endpoint": "http://local:9000"},
			},
		},
	}
	v, ok := GetAdminFloorField(&s, "minio", "endpoint")
	if !ok || v != "http://local:9000" {
		t.Fatalf("endpoint ok=%v v=%q", ok, v)
	}
}
