package utils

import "testing"

func TestNomadNodeReleasePlatform(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		node     *NomadNode
		wantOS   string
		wantArch string
		wantErr  bool
	}{
		{
			name:     "explicit linux",
			node:     &NomadNode{Name: "n1", Attributes: map[string]string{"os.name": "linux", "cpu.arch": "amd64"}},
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name:     "ubuntu maps to linux",
			node:     &NomadNode{Name: "n2", Attributes: map[string]string{"os.name": "ubuntu", "cpu.arch": "amd64"}},
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name: "kernel linux plus distro os.name",
			node: &NomadNode{Name: "n3", Attributes: map[string]string{
				"kernel.name": "linux",
				"os.name":     "ubuntu",
				"cpu.arch":    "arm64",
			}},
			wantOS: "linux", wantArch: "arm64",
		},
		{
			name:     "debian",
			node:     &NomadNode{Name: "n4", Attributes: map[string]string{"os.name": "debian", "cpu.arch": "amd64"}},
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name:     "amzn maps to linux",
			node:     &NomadNode{Name: "n5", Attributes: map[string]string{"os.name": "amzn", "cpu.arch": "amd64"}},
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name:     "x86_64 normalized",
			node:     &NomadNode{Name: "n6", Attributes: map[string]string{"os.name": "linux", "cpu.arch": "x86_64"}},
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name: "darwin client",
			node: &NomadNode{Name: "n7", Attributes: map[string]string{
				"kernel.name": "darwin",
				"os.name":     "darwin",
				"cpu.arch":    "arm64",
			}},
			wantOS: "darwin", wantArch: "arm64",
		},
		{
			name:    "missing attrs",
			node:    &NomadNode{Name: "n8", Attributes: map[string]string{}},
			wantErr: true,
		},
		{
			name:    "unsupported kernel",
			node:    &NomadNode{Name: "n9", Attributes: map[string]string{"kernel.name": "zos", "os.name": "linux", "cpu.arch": "amd64"}},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			goos, goarch, err := NomadNodeReleasePlatform(tc.node)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if goos != tc.wantOS || goarch != tc.wantArch {
				t.Fatalf("got %s/%s want %s/%s", goos, goarch, tc.wantOS, tc.wantArch)
			}
		})
	}
}
