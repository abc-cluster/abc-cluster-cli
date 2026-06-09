package appgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// JobParams carries the platform-resolved inputs the HCL generator needs that
// are not part of the user-authored Spec: the target namespace, datacenters,
// node pool, the MinIO endpoint injected as ABC_MINIO_ENDPOINT, and the
// Vault-minted MinIO service-account credentials.
//
// AWSAccessKey/AWSSecretKey are injected directly as task env in phase 1 (PoC):
// the DataProvisioner mints them just before submission. A later hardening step
// can move them behind a Nomad Variable + `template` stanza (the same pattern
// the pipeline head job uses for nomadVar creds) without changing the service
// block or routing tags.
type JobParams struct {
	Namespace     string
	Datacenters   []string
	NodePool      string
	MinIOEndpoint string
	AWSAccessKey  string
	AWSSecretKey  string
}

// Generate produces the Nomad HCL for an app `service` job from a resolved Spec
// (post-Validate + ApplyDefaults) and platform JobParams. The output is a
// single-replica service job in the abc-apps namespace with a Nomad-native
// service block (provider = "nomad") carrying Traefik routing tags, a
// Nomad-native health check, and the spec's restart/update policies.
//
// Phase 1 emits ONLY the Host router rule + loadbalancer server port tags — no
// stripprefix (apps serve at root), no auth middleware (forward-auth is at the
// Caddy edge), no sticky-cookie / count / spread (single replica). The phase-2
// multi-replica sticky-cookie + spread extension point is marked with a
// comment in the emitted HCL so the change stays localized.
func Generate(s *Spec, p JobParams) string {
	f := hclwrite.NewEmptyFile()
	root := f.Body()

	ns := p.Namespace
	if ns == "" {
		ns = DefaultNamespace
	}

	jobBlock := root.AppendNewBlock("job", []string{s.JobName()})
	jobBody := jobBlock.Body()
	jobBody.SetAttributeValue("namespace", cty.StringVal(ns))
	jobBody.SetAttributeValue("type", cty.StringVal("service"))
	if len(p.Datacenters) > 0 {
		dcs := make([]cty.Value, len(p.Datacenters))
		for i, dc := range p.Datacenters {
			dcs[i] = cty.StringVal(dc)
		}
		jobBody.SetAttributeValue("datacenters", cty.ListVal(dcs))
	}
	if p.NodePool != "" {
		jobBody.SetAttributeValue("node_pool", cty.StringVal(p.NodePool))
	}

	// meta.abc_project — auth-svc resolves the app's project group from the
	// matching job's meta (or the service's Host tag) during the access: team
	// check; the request Host is NOT string-parsed (kebab-case is ambiguous).
	metaBody := jobBody.AppendNewBlock("meta", nil).Body()
	metaBody.SetAttributeValue("abc_project", cty.StringVal(s.Project))
	metaBody.SetAttributeValue("abc_app", cty.StringVal(s.Name))
	metaBody.SetAttributeValue("abc_framework", cty.StringVal(s.NormFramework()))
	metaBody.SetAttributeValue("abc_health", cty.StringVal(s.Health))
	metaBody.SetAttributeValue("abc_url", cty.StringVal(s.URL()))
	metaBody.SetAttributeValue("abc_exposure", cty.StringVal(s.NormExposure()))
	metaBody.SetAttributeValue("abc_cpu", cty.StringVal(fmt.Sprintf("%d", s.Resources.CPU)))
	metaBody.SetAttributeValue("abc_memory", cty.StringVal(fmt.Sprintf("%d", s.Resources.Memory)))
	if b := bucketList(s.Data); b != "" {
		metaBody.SetAttributeValue("abc_data_buckets", cty.StringVal(b))
	}

	// update policy — rolling update on re-deploy.
	updBody := jobBody.AppendNewBlock("update", nil).Body()
	updBody.SetAttributeValue("max_parallel", cty.NumberIntVal(1))
	updBody.SetAttributeValue("min_healthy_time", cty.StringVal("10s"))
	updBody.SetAttributeValue("health_check", cty.StringVal("checks"))

	groupBody := jobBody.AppendNewBlock("group", []string{"app"}).Body()
	groupBody.SetAttributeValue("count", cty.NumberIntVal(int64(s.Replicas)))

	// PHASE 2 (multi-replica): when replicas > 1, also emit
	//   spread { attribute = "${node.unique.id}" }
	// here so allocs distribute across nodes and Traefik load balances across
	// them under the single Nomad service name. Single replica (phase 1) has
	// nothing to spread.

	// restart policy.
	restartBody := groupBody.AppendNewBlock("restart", nil).Body()
	restartBody.SetAttributeValue("attempts", cty.NumberIntVal(3))
	restartBody.SetAttributeValue("delay", cty.StringVal("15s"))
	restartBody.SetAttributeValue("interval", cty.StringVal("5m"))
	restartBody.SetAttributeValue("mode", cty.StringVal("delay"))

	// network — HOST mode + a static host port equal to the container port. The
	// container binds s.Port directly on the host network namespace, so the
	// Traefik `loadbalancer.server.port` tag (= s.Port) routes correctly. A
	// dynamic mapped port (`to`) combined with a hardcoded LB-port tag mismatch
	// → Traefik can't reach the app → 502. (Matches the live working apps:
	// network mode=host, reserved static port, docker network_mode=host.)
	netBody := groupBody.AppendNewBlock("network", nil).Body()
	netBody.SetAttributeValue("mode", cty.StringVal("host"))
	portBody := netBody.AppendNewBlock("port", []string{"http"}).Body()
	portBody.SetAttributeValue("static", cty.NumberIntVal(int64(s.Port)))

	// service — Nomad-native (provider = "nomad"), NOT Consul. Service name
	// matches the job name so `abc app` and Traefik discover it consistently.
	svcBody := groupBody.AppendNewBlock("service", nil).Body()
	svcBody.SetAttributeValue("name", cty.StringVal(s.JobName()))
	svcBody.SetAttributeValue("provider", cty.StringVal("nomad"))
	svcBody.SetAttributeValue("port", cty.StringVal("http"))

	// Traefik routing tags. Phase 1: Host router rule + loadbalancer server
	// port only. No stripprefix (root), no middleware (auth is at Caddy edge).
	//
	// The Host rule is driven by `exposure` (see Spec.Hosts): public → the public
	// edge wildcard host; internal → an off-edge host the public Caddy does not
	// proxy (Tailscale + campus LAN only); both → both, ORed.
	hostParts := make([]string, 0, len(s.Hosts()))
	for _, h := range s.Hosts() {
		hostParts = append(hostParts, fmt.Sprintf("Host(`%s`)", h))
	}
	hostRule := strings.Join(hostParts, " || ")
	tags := []cty.Value{
		cty.StringVal("traefik.enable=true"),
		cty.StringVal(fmt.Sprintf("traefik.http.routers.%s.rule=%s", s.JobName(), hostRule)),
		cty.StringVal(fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", s.JobName())),
		cty.StringVal(fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port=%d", s.JobName(), s.Port)),
		// PHASE 2 (sticky sessions): for stateful frameworks with replicas > 1,
		// append the loadbalancer sticky-cookie tags here, e.g.
		//   traefik.http.services.<job>.loadbalancer.sticky.cookie=true
		//   traefik.http.services.<job>.loadbalancer.sticky.cookie.name=abc_app_lb
		// Stateful classification: streamlit/shiny/dash/voila/panel = yes,
		// pode = no. Single replica (phase 1) needs no balancing, so nothing
		// is emitted.
	}
	svcBody.SetAttributeValue("tags", cty.ListVal(tags))

	// Nomad-native health check.
	checkBody := svcBody.AppendNewBlock("check", nil).Body()
	checkBody.SetAttributeValue("type", cty.StringVal("http"))
	checkBody.SetAttributeValue("path", cty.StringVal(s.Health))
	checkBody.SetAttributeValue("interval", cty.StringVal("10s"))
	checkBody.SetAttributeValue("timeout", cty.StringVal("5s"))

	// task.
	taskBody := groupBody.AppendNewBlock("task", []string{"app"}).Body()
	taskBody.SetAttributeValue("driver", cty.StringVal("docker"))

	cfgBody := taskBody.AppendNewBlock("config", nil).Body()
	cfgBody.SetAttributeValue("image", cty.StringVal(s.Image))
	// Host networking: the container shares the host network namespace and binds
	// the static port reserved above directly. No docker port mapping (`ports`)
	// is used — that is for bridge mode, which would hide the app behind a
	// dynamic host port the Traefik LB-port tag doesn't know about.
	cfgBody.SetAttributeValue("network_mode", cty.StringVal("host"))

	// env — platform-injected vars merged with the user's env. Platform wins
	// on key collision (the reserved ABC_*/AWS_* names below take precedence).
	envBody := taskBody.AppendNewBlock("env", nil).Body()
	env := mergeEnv(s.Env, platformEnv(s, p))
	for _, k := range sortedKeys(env) {
		envBody.SetAttributeValue(k, cty.StringVal(env[k]))
	}

	// resources — hard limits from abc-app.yaml (post-default).
	resBody := taskBody.AppendNewBlock("resources", nil).Body()
	resBody.SetAttributeValue("cpu", cty.NumberIntVal(int64(s.Resources.CPU)))
	resBody.SetAttributeValue("memory", cty.NumberIntVal(int64(s.Resources.Memory)))

	logsBody := taskBody.AppendNewBlock("logs", nil).Body()
	logsBody.SetAttributeValue("max_files", cty.NumberIntVal(5))
	logsBody.SetAttributeValue("max_file_size", cty.NumberIntVal(10))

	return utils.PrettyPrintHCL(string(f.Bytes()))
}

// platformEnv returns the reserved platform-injected env vars. These always win
// over user-declared env on collision (see mergeEnv). No framework-specific
// base-URL args are injected — apps serve at root.
func platformEnv(s *Spec, p JobParams) map[string]string {
	env := map[string]string{
		"ABC_APP_NAME":     s.Name,
		"ABC_APP_URL":      s.URL(),
		"ABC_APP_BASE_URL": "/",
		"ABC_PROJECT":      s.Project,
	}
	if p.MinIOEndpoint != "" {
		env["ABC_MINIO_ENDPOINT"] = p.MinIOEndpoint
	}
	if p.AWSAccessKey != "" {
		env["AWS_ACCESS_KEY_ID"] = p.AWSAccessKey
	}
	if p.AWSSecretKey != "" {
		env["AWS_SECRET_ACCESS_KEY"] = p.AWSSecretKey
	}
	return env
}

// mergeEnv combines user env with platform env. Platform wins on collision —
// the reserved ABC_*/AWS_* contract must not be overridable by app authors.
func mergeEnv(user, platform map[string]string) map[string]string {
	out := make(map[string]string, len(user)+len(platform))
	for k, v := range user {
		out[k] = v
	}
	for k, v := range platform {
		out[k] = v // platform wins
	}
	return out
}

// bucketList joins declared data buckets as a comma-separated string for the
// job meta (so `abc app show` can display them without re-reading abc-app.yaml).
// Each entry is `bucket(access)` to preserve the access mode.
func bucketList(data []DataMount) string {
	if len(data) == 0 {
		return ""
	}
	parts := make([]string, 0, len(data))
	for _, d := range data {
		acc := d.Access
		if acc == "" {
			acc = AccessRead
		}
		parts = append(parts, d.Bucket+"("+acc+")")
	}
	return strings.Join(parts, ",")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
