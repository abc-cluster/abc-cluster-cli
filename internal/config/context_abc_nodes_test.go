package config

import "testing"

func TestAbcNodesMinioStorageCLIEnv_PrefersS3KeysOverMinioRoot(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			Services: AdminServices{
				MinIO: &AdminFloorService{Endpoint: "http://127.0.0.1:9000"},
			},
			ABCNodes: &AdminABCNodes{
				S3AccessKey:       "s3-ak",
				S3SecretKey:       "s3-sk",
				MinioRootUser:     "minio-u",
				MinioRootPassword: "minio-p",
				S3Region:          "eu-west-1",
			},
		},
	}
	m := ctx.AbcNodesMinioStorageCLIEnv()
	if m["AWS_ACCESS_KEY_ID"] != "s3-ak" || m["AWS_SECRET_ACCESS_KEY"] != "s3-sk" {
		t.Fatalf("expected s3 keys, got %#v", m)
	}
	if m["AWS_DEFAULT_REGION"] != "eu-west-1" {
		t.Fatalf("region: %v", m["AWS_DEFAULT_REGION"])
	}
	if m["AWS_ENDPOINT_URL"] != "http://127.0.0.1:9000" {
		t.Fatalf("endpoint: %v", m["AWS_ENDPOINT_URL"])
	}
}

func TestAbcNodesMinioStorageCLIEnv_LegacyS3EndpointField(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			ABCNodes: &AdminABCNodes{
				S3AccessKey: "ak",
				S3SecretKey: "sk",
				S3Endpoint:  "http://legacy:9000",
			},
		},
	}
	m := ctx.AbcNodesMinioStorageCLIEnv()
	if m["AWS_ENDPOINT_URL"] != "http://legacy:9000" {
		t.Fatalf("endpoint: %v", m["AWS_ENDPOINT_URL"])
	}
}

func TestAbcNodesMinioStorageCLIEnv_FallsBackToMinioRoot(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			ABCNodes: &AdminABCNodes{
				MinioRootUser:     "root-u",
				MinioRootPassword: "root-p",
			},
		},
	}
	m := ctx.AbcNodesMinioStorageCLIEnv()
	if m["AWS_ACCESS_KEY_ID"] != "root-u" || m["AWS_SECRET_ACCESS_KEY"] != "root-p" {
		t.Fatalf("expected minio root as AWS keys, got %#v", m)
	}
}

func TestAbcNodesMinioStorageCLIEnv_PrefersFloorAccessKeys(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			Services: AdminServices{
				MinIO: &AdminFloorService{
					Endpoint:  "http://minio:9000",
					AccessKey: "floor-ak",
					SecretKey: "floor-sk",
				},
			},
			ABCNodes: &AdminABCNodes{
				S3AccessKey: "node-ak",
				S3SecretKey: "node-sk",
			},
		},
	}
	m := ctx.AbcNodesMinioStorageCLIEnv()
	if m["AWS_ACCESS_KEY_ID"] != "floor-ak" || m["AWS_SECRET_ACCESS_KEY"] != "floor-sk" {
		t.Fatalf("expected floor keys, got %#v", m)
	}
}

func TestAbcNodesRustfsStorageCLIEnv_UsesRustfsEndpoint(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			Services: AdminServices{
				MinIO:  &AdminFloorService{Endpoint: "http://minio:9000"},
				Rustfs: &AdminFloorService{Endpoint: "http://rust:8080"},
			},
			ABCNodes: &AdminABCNodes{
				S3AccessKey: "ak",
				S3SecretKey: "sk",
			},
		},
	}
	m := ctx.AbcNodesRustfsStorageCLIEnv()
	if m["AWS_ENDPOINT_URL"] != "http://rust:8080" {
		t.Fatalf("rustfs env should use rustfs endpoint, got %q", m["AWS_ENDPOINT_URL"])
	}
}

func TestAbcNodesMinioStorageCLIEnv_NotABCNodes(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCCluster,
		Admin: Admin{
			ABCNodes: &AdminABCNodes{S3AccessKey: "x"},
		},
	}
	if ctx.AbcNodesMinioStorageCLIEnv() != nil {
		t.Fatal("expected nil when cluster is not abc-nodes")
	}
}

func TestMigrateAbcNodesLegacyS3Endpoint(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			ABCNodes: &AdminABCNodes{
				S3Endpoint:        "http://old:9000",
				MinioRootUser:     "u",
				MinioRootPassword: "p",
			},
		},
	}
	migrateAbcNodesLegacyS3Endpoint(&ctx)
	if ctx.Admin.ABCNodes.S3Endpoint != "" {
		t.Fatalf("expected legacy s3_endpoint cleared, got %q", ctx.Admin.ABCNodes.S3Endpoint)
	}
	ep, ok := GetAdminFloorField(&ctx.Admin.Services, "minio", "endpoint")
	if !ok || ep != "http://old:9000" {
		t.Fatalf("minio endpoint: ok=%v ep=%q", ok, ep)
	}
}

func TestAbcNodesNomadNamespaceForCLI(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			ABCNodes: &AdminABCNodes{NomadNamespace: "  staging  "},
		},
	}
	if got := ctx.AbcNodesNomadNamespaceForCLI(); got != "staging" {
		t.Fatalf("namespace: got %q want staging", got)
	}
}
