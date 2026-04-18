package config

import "testing"

func TestAbcNodesStorageCLIEnv_PrefersS3KeysOverMinioRoot(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			ABCNodes: &AdminABCNodes{
				S3AccessKey:       "s3-ak",
				S3SecretKey:       "s3-sk",
				MinioRootUser:     "minio-u",
				MinioRootPassword: "minio-p",
				S3Region:          "eu-west-1",
				S3Endpoint:        "http://127.0.0.1:9000",
			},
		},
	}
	m := ctx.AbcNodesStorageCLIEnv()
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

func TestAbcNodesStorageCLIEnv_FallsBackToMinioRoot(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{
			ABCNodes: &AdminABCNodes{
				MinioRootUser:     "root-u",
				MinioRootPassword: "root-p",
			},
		},
	}
	m := ctx.AbcNodesStorageCLIEnv()
	if m["AWS_ACCESS_KEY_ID"] != "root-u" || m["AWS_SECRET_ACCESS_KEY"] != "root-p" {
		t.Fatalf("expected minio root as AWS keys, got %#v", m)
	}
}

func TestAbcNodesStorageCLIEnv_NotABCNodes(t *testing.T) {
	ctx := Context{
		ClusterType: ClusterTypeABCCluster,
		Admin: Admin{
			ABCNodes: &AdminABCNodes{S3AccessKey: "x"},
		},
	}
	if ctx.AbcNodesStorageCLIEnv() != nil {
		t.Fatal("expected nil when cluster is not abc-nodes")
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
