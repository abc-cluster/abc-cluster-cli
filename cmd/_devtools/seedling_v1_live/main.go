// devtool: exercise SeedlingV1CredSource against the live aither broker.
//
//	go run ./cmd/_devtools/seedling_v1_live --opaque abco_… [--endpoint https://…]
//
// Mints + tears down nothing; the caller must have already flipped a slot
// onto seedling/v1 and obtained the opaque (e.g. via the cred-source flip
// admin endpoint). Default endpoint is the seedling tier.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/credsource"
)

func main() {
	opaque := flag.String("opaque", "", "bare opaque token (abco_…)")
	endpoint := flag.String("endpoint", "https://nomad.seedling.abc-cluster.cloud",
		"cluster endpoint (for deriving broker URL)")
	authEndpoint := flag.String("auth-endpoint", "",
		"override broker exchange URL (default: derived as auth.<rest>/auth/exchange)")
	flag.Parse()
	if *opaque == "" {
		fmt.Fprintln(os.Stderr, "--opaque is required")
		os.Exit(2)
	}

	ctx := abccfg.Context{Endpoint: *endpoint, AccessToken: *opaque, CredSource: "seedling/v1"}
	cs, err := credsource.NewSeedlingV1(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewSeedlingV1: %v\n", err)
		os.Exit(1)
	}
	if *authEndpoint != "" {
		cs.ExchangeURL = *authEndpoint
	}
	fmt.Printf("[devtool] exchange URL: %s\n", cs.ExchangeURL)

	t0 := time.Now()
	creds, err := cs.Resolve(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Resolve: %v\n", err)
		os.Exit(1)
	}
	d1 := time.Since(t0)

	fmt.Printf("[devtool] resolve #1 (cache miss): %s\n", d1)
	fmt.Printf("  Source       = %s\n", creds.Source)
	fmt.Printf("  Whoami       = %s\n", creds.Whoami)
	fmt.Printf("  Nomad.Addr   = %s\n", creds.Nomad.Addr)
	fmt.Printf("  Nomad.NS     = %s\n", creds.Nomad.Namespace)
	fmt.Printf("  Nomad.Token  = %s…(len=%d)\n", creds.Nomad.Token[:8], len(creds.Nomad.Token))
	fmt.Printf("  Nomad.DCs    = %v\n", creds.Nomad.Datacenters)
	fmt.Printf("  Nomad.Pools  = (head=%s, worker=%s)\n", creds.Nomad.HeadPool, creds.Nomad.WorkerPool)
	fmt.Printf("  Minio.URL    = %s\n", creds.Minio.Endpoint)
	fmt.Printf("  Minio.AK     = %s\n", creds.Minio.AccessKey)
	fmt.Printf("  Minio.SK     = (len=%d)\n", len(creds.Minio.SecretKey))

	t0 = time.Now()
	_, err = cs.Resolve(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "second Resolve: %v\n", err)
		os.Exit(1)
	}
	d2 := time.Since(t0)
	fmt.Printf("[devtool] resolve #2 (should be cache hit): %s\n", d2)
	if d2 > d1/2 {
		fmt.Println("[devtool] WARNING: cache hit was slow — cache may not be wired up correctly")
	}

	cs.InvalidateCache()
	t0 = time.Now()
	_, err = cs.Resolve(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "post-invalidate Resolve: %v\n", err)
		os.Exit(1)
	}
	d3 := time.Since(t0)
	fmt.Printf("[devtool] resolve #3 (post InvalidateCache, broker hit): %s\n", d3)

	fmt.Println("[devtool] ✓ ALL ASSERTIONS PASS")
}
