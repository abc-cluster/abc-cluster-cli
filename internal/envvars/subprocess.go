package envvars

import (
	"os"
	"os/exec"
	"strings"
)

// ToolKind identifies which downstream binary the CLI is about to shell
// out to. The InjectVendor call constructs the right set of vendor env
// vars for each kind from the resolver's view of the active context.
type ToolKind int

const (
	ToolNomad ToolKind = iota
	ToolVault
	ToolRclone
	ToolS5cmd
	ToolNextflow
	ToolNodeProbe
	// ToolMC: MinIO Client (`mc`) — reads MC_HOST_<alias>, MC_INSECURE,
	// AWS_* for direct-S3 fallback.
	ToolMC
	// ToolPulumi: Pulumi CLI invoked with the @pulumi/minio provider —
	// reads MINIO_SERVER, MINIO_USER, MINIO_PASSWORD (NOT AWS_*), and
	// also NOMAD_* if the stack provisions Nomad jobs.
	ToolPulumi
)

// Resolved holds the constructed vendor env values for subprocess
// injection. Callers populate it from the active context (or from a
// Resolver) and pass it to InjectVendor.
//
// Unlike Resolver.Resolve which deals in ABC_* names, this struct deals
// in vendor-namespace values because that's what the subprocess
// understands. The caller is responsible for the mapping (typically: read
// ABC_API_ADDR via Resolver, derive NomadAddr from the controller's
// /endpoints response or cached context field).
type Resolved struct {
	NomadAddr      string
	NomadToken     string
	NomadRegion    string
	NomadNamespace string

	VaultAddr      string
	VaultToken     string
	VaultNamespace string

	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSEndpointURL     string
	AWSRegion          string

	// AWS extended — grove+ short-lived creds and MinIO compatibility.
	AWSDefaultRegion              string
	AWSSessionToken               string
	AWSCABundle                   string
	S3ForcePathStyle              bool
	AWSRequestChecksumCalculation string // typically "when_required" for MinIO compat

	// MinIO Client (mc) — modern env-var config (no ~/.mc/config.json needed).
	// MCHostAlias names the connection alias used in MC_HOST_<alias>; defaults
	// to "local" if empty. MCHostURL is the full URL including embedded
	// user:password (e.g. "https://user:pass@host:port").
	MCHostAlias string
	MCHostURL   string
	MCInsecure  bool

	// MinIO Pulumi provider — the @pulumi/minio provider reads these
	// three (NOT the AWS_* family) for its admin operations.
	MinIOServer   string // host:port (scheme stripped)
	MinIOUser     string
	MinIOPassword string

	// MinIO server-side root (operator passthrough to `mc admin` flows).
	MinIORootUser     string
	MinIORootPassword string

	// rclone config path (CLI generates a fresh conf file per invocation).
	RcloneConfig string
}

// SudoOptOuts is set by the caller (typically from cobra flags or env
// vars) when ABC_CLI_SUDO=1 is active AND the operator wants specific
// vendor env vars to leak through from the parent shell rather than be
// overwritten. Each field, when true, suppresses InjectVendor's setting
// of the matching vendor variable.
//
// Inspect with envvars.SudoOptOutsFromEnv to read the standard
// ABC_CLI_NO_DERIVE_<VAR>=1 set.
type SudoOptOuts struct {
	NomadAddr      bool
	NomadToken     bool
	NomadRegion    bool
	NomadNamespace bool
	VaultAddr      bool
	VaultToken     bool
	VaultNamespace bool
	AWSAccessKey   bool
	AWSEndpointURL bool
	AWSRegion      bool

	// Storage-extended opt-outs.
	AWSSessionToken               bool
	AWSCABundle                   bool
	S3ForcePathStyle              bool
	AWSRequestChecksumCalculation bool
	MCHost                        bool // covers MC_HOST_<alias>
	MCInsecure                    bool
	MinIOServer                   bool
	MinIOUser                     bool
	MinIOPassword                 bool
	MinIORootUser                 bool
	MinIORootPassword             bool
	RcloneConfig                  bool
}

// SudoOptOutsFromEnv reads the standard ABC_CLI_NO_DERIVE_<VAR>=1 set
// from env (via the given lookup) and returns a populated SudoOptOuts.
// Pass envvars.OSEnv in production.
func SudoOptOutsFromEnv(env EnvLookup) SudoOptOuts {
	is1 := func(name string) bool {
		v, ok := env(name)
		if !ok {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y", "on":
			return true
		}
		return false
	}
	return SudoOptOuts{
		NomadAddr:                     is1("ABC_CLI_NO_DERIVE_NOMAD_ADDR"),
		NomadToken:                    is1("ABC_CLI_NO_DERIVE_NOMAD_TOKEN"),
		NomadRegion:                   is1("ABC_CLI_NO_DERIVE_NOMAD_REGION"),
		NomadNamespace:                is1("ABC_CLI_NO_DERIVE_NOMAD_NAMESPACE"),
		VaultAddr:                     is1("ABC_CLI_NO_DERIVE_VAULT_ADDR"),
		VaultToken:                    is1("ABC_CLI_NO_DERIVE_VAULT_TOKEN"),
		VaultNamespace:                is1("ABC_CLI_NO_DERIVE_VAULT_NAMESPACE"),
		AWSAccessKey:                  is1("ABC_CLI_NO_DERIVE_AWS_ACCESS_KEY"),
		AWSEndpointURL:                is1("ABC_CLI_NO_DERIVE_AWS_ENDPOINT_URL"),
		AWSRegion:                     is1("ABC_CLI_NO_DERIVE_AWS_REGION"),
		AWSSessionToken:               is1("ABC_CLI_NO_DERIVE_AWS_SESSION_TOKEN"),
		AWSCABundle:                   is1("ABC_CLI_NO_DERIVE_AWS_CA_BUNDLE"),
		S3ForcePathStyle:              is1("ABC_CLI_NO_DERIVE_S3_FORCE_PATH_STYLE"),
		AWSRequestChecksumCalculation: is1("ABC_CLI_NO_DERIVE_AWS_REQUEST_CHECKSUM_CALCULATION"),
		MCHost:                        is1("ABC_CLI_NO_DERIVE_MC_HOST"),
		MCInsecure:                    is1("ABC_CLI_NO_DERIVE_MC_INSECURE"),
		MinIOServer:                   is1("ABC_CLI_NO_DERIVE_MINIO_SERVER"),
		MinIOUser:                     is1("ABC_CLI_NO_DERIVE_MINIO_USER"),
		MinIOPassword:                 is1("ABC_CLI_NO_DERIVE_MINIO_PASSWORD"),
		MinIORootUser:                 is1("ABC_CLI_NO_DERIVE_MINIO_ROOT_USER"),
		MinIORootPassword:             is1("ABC_CLI_NO_DERIVE_MINIO_ROOT_PASSWORD"),
		RcloneConfig:                  is1("ABC_CLI_NO_DERIVE_RCLONE_CONFIG"),
	}
}

// InjectVendor builds the vendor env-var slice for the given tool kind
// and appends it to cmd.Env. Existing entries in cmd.Env are preserved
// (so callers can pre-populate it with non-vendor entries first). If
// cmd.Env is nil, it is initialised from os.Environ() with vendor names
// filtered OUT — i.e., the parent shell's NOMAD_ADDR etc. do NOT leak
// through unless an opt-out is set.
//
// Opt-outs (set via SudoOptOuts) cause InjectVendor to leave the
// matching vendor var alone, allowing the parent shell's value through
// when present.
func InjectVendor(cmd *exec.Cmd, kind ToolKind, r Resolved, opts SudoOptOuts) {
	if cmd.Env == nil {
		cmd.Env = filteredParentEnv(kind, opts)
	}

	add := func(key, value string, optedOut bool) {
		if optedOut || value == "" {
			return
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	addBool := func(key string, value, optedOut bool) {
		if optedOut || !value {
			return
		}
		cmd.Env = append(cmd.Env, key+"=1")
	}

	switch kind {
	case ToolNomad:
		add("NOMAD_ADDR", r.NomadAddr, opts.NomadAddr)
		add("NOMAD_TOKEN", r.NomadToken, opts.NomadToken)
		add("NOMAD_REGION", r.NomadRegion, opts.NomadRegion)
		add("NOMAD_NAMESPACE", r.NomadNamespace, opts.NomadNamespace)

	case ToolVault:
		add("VAULT_ADDR", r.VaultAddr, opts.VaultAddr)
		add("VAULT_TOKEN", r.VaultToken, opts.VaultToken)
		add("VAULT_NAMESPACE", r.VaultNamespace, opts.VaultNamespace)

	case ToolRclone, ToolS5cmd:
		injectAWS(cmd, r, opts)
		add("RCLONE_CONFIG", r.RcloneConfig, opts.RcloneConfig)

	case ToolNextflow:
		// Nextflow itself reads AWS_* for S3 work directory access AND
		// may read NOMAD_* if a Nomad executor plugin is configured.
		// Inject both families.
		injectAWS(cmd, r, opts)
		add("NOMAD_ADDR", r.NomadAddr, opts.NomadAddr)
		add("NOMAD_TOKEN", r.NomadToken, opts.NomadToken)
		add("NOMAD_REGION", r.NomadRegion, opts.NomadRegion)
		add("NOMAD_NAMESPACE", r.NomadNamespace, opts.NomadNamespace)

	case ToolNodeProbe:
		// abc-node-probe accepts NOMAD_* when run as a periodic Nomad
		// sysbatch but otherwise has no vendor env-var dependencies.
		add("NOMAD_ADDR", r.NomadAddr, opts.NomadAddr)
		add("NOMAD_TOKEN", r.NomadToken, opts.NomadToken)

	case ToolMC:
		// MinIO Client reads MC_HOST_<alias> for connection config + flags.
		// AWS_* are not used by mc directly but are kept available for
		// any S3-aware helpers the operator might invoke.
		alias := r.MCHostAlias
		if alias == "" {
			alias = "local"
		}
		add("MC_HOST_"+alias, r.MCHostURL, opts.MCHost)
		addBool("MC_INSECURE", r.MCInsecure, opts.MCInsecure)
		injectAWS(cmd, r, opts)
		add("MINIO_ROOT_USER", r.MinIORootUser, opts.MinIORootUser)
		add("MINIO_ROOT_PASSWORD", r.MinIORootPassword, opts.MinIORootPassword)

	case ToolPulumi:
		// @pulumi/minio provider — needs the three MINIO_* vars (NOT AWS_*)
		// for its admin/data-source operations. Pulumi-driven Nomad jobs
		// also need NOMAD_*; the stack typically declares which.
		add("MINIO_SERVER", r.MinIOServer, opts.MinIOServer)
		add("MINIO_USER", r.MinIOUser, opts.MinIOUser)
		add("MINIO_PASSWORD", r.MinIOPassword, opts.MinIOPassword)
		add("NOMAD_ADDR", r.NomadAddr, opts.NomadAddr)
		add("NOMAD_TOKEN", r.NomadToken, opts.NomadToken)
		add("NOMAD_REGION", r.NomadRegion, opts.NomadRegion)
		add("NOMAD_NAMESPACE", r.NomadNamespace, opts.NomadNamespace)
	}
}

// injectAWS writes the full AWS_*/S3_* family to cmd.Env honouring opts.
// Shared by ToolRclone, ToolS5cmd, ToolNextflow, ToolMC.
func injectAWS(cmd *exec.Cmd, r Resolved, opts SudoOptOuts) {
	add := func(key, value string, optedOut bool) {
		if optedOut || value == "" {
			return
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	addBool := func(key string, value, optedOut bool) {
		if optedOut || !value {
			return
		}
		cmd.Env = append(cmd.Env, key+"=true")
	}
	add("AWS_ACCESS_KEY_ID", r.AWSAccessKeyID, opts.AWSAccessKey)
	add("AWS_SECRET_ACCESS_KEY", r.AWSSecretAccessKey, opts.AWSAccessKey)
	add("AWS_ENDPOINT_URL", r.AWSEndpointURL, opts.AWSEndpointURL)
	add("AWS_REGION", r.AWSRegion, opts.AWSRegion)
	add("AWS_DEFAULT_REGION", r.AWSDefaultRegion, opts.AWSRegion)
	add("AWS_SESSION_TOKEN", r.AWSSessionToken, opts.AWSSessionToken)
	add("AWS_CA_BUNDLE", r.AWSCABundle, opts.AWSCABundle)
	addBool("AWS_S3_FORCE_PATH_STYLE", r.S3ForcePathStyle, opts.S3ForcePathStyle)
	addBool("S3_FORCE_PATH_STYLE", r.S3ForcePathStyle, opts.S3ForcePathStyle)
	add("AWS_REQUEST_CHECKSUM_CALCULATION", r.AWSRequestChecksumCalculation, opts.AWSRequestChecksumCalculation)
}

// filteredParentEnv returns os.Environ() with the vendor variables for
// the given tool kind removed (unless the matching opt-out is set, in
// which case the parent value is kept). Used when cmd.Env is nil so that
// the subprocess starts from a clean vendor slate.
func filteredParentEnv(kind ToolKind, opts SudoOptOuts) []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:eq]
		if shouldFilter(kind, key, opts) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// shouldFilter reports whether key should be stripped from the parent
// environment when starting a subprocess of the given kind. Returns
// false (keep the var) when the matching opt-out is set.
func shouldFilter(kind ToolKind, key string, opts SudoOptOuts) bool {
	switch key {
	case "NOMAD_ADDR":
		return !opts.NomadAddr && touchesNomad(kind)
	case "NOMAD_TOKEN":
		return !opts.NomadToken && touchesNomad(kind)
	case "NOMAD_REGION":
		return !opts.NomadRegion && touchesNomad(kind)
	case "NOMAD_NAMESPACE":
		return !opts.NomadNamespace && touchesNomad(kind)
	case "VAULT_ADDR":
		return !opts.VaultAddr && kind == ToolVault
	case "VAULT_TOKEN":
		return !opts.VaultToken && kind == ToolVault
	case "VAULT_NAMESPACE":
		return !opts.VaultNamespace && kind == ToolVault
	case "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY":
		return !opts.AWSAccessKey && touchesAWS(kind)
	case "AWS_ENDPOINT_URL":
		return !opts.AWSEndpointURL && touchesAWS(kind)
	case "AWS_REGION", "AWS_DEFAULT_REGION":
		return !opts.AWSRegion && touchesAWS(kind)
	case "AWS_SESSION_TOKEN":
		return !opts.AWSSessionToken && touchesAWS(kind)
	case "AWS_CA_BUNDLE":
		return !opts.AWSCABundle && touchesAWS(kind)
	case "AWS_S3_FORCE_PATH_STYLE", "S3_FORCE_PATH_STYLE":
		return !opts.S3ForcePathStyle && touchesAWS(kind)
	case "AWS_REQUEST_CHECKSUM_CALCULATION":
		return !opts.AWSRequestChecksumCalculation && touchesAWS(kind)
	case "MC_INSECURE":
		return !opts.MCInsecure && kind == ToolMC
	case "MINIO_SERVER":
		return !opts.MinIOServer && kind == ToolPulumi
	case "MINIO_USER":
		return !opts.MinIOUser && kind == ToolPulumi
	case "MINIO_PASSWORD":
		return !opts.MinIOPassword && kind == ToolPulumi
	case "MINIO_ROOT_USER":
		return !opts.MinIORootUser && kind == ToolMC
	case "MINIO_ROOT_PASSWORD":
		return !opts.MinIORootPassword && kind == ToolMC
	case "RCLONE_CONFIG":
		return !opts.RcloneConfig && (kind == ToolRclone || kind == ToolS5cmd)
	}
	// MC_HOST_<alias>: dynamic prefix — filter when ToolMC and not opted out.
	if !opts.MCHost && kind == ToolMC && strings.HasPrefix(key, "MC_HOST_") {
		return true
	}
	return false
}

func touchesNomad(k ToolKind) bool {
	return k == ToolNomad || k == ToolNextflow || k == ToolNodeProbe || k == ToolPulumi
}

func touchesAWS(k ToolKind) bool {
	return k == ToolRclone || k == ToolS5cmd || k == ToolNextflow || k == ToolMC
}
