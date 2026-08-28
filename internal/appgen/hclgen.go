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
	// AppsDoors carries the per-deployment ingress door hostnames + IP forms
	// (from the active context's admin.services.apps block). Used to compose
	// abc_url / abc_url_ip in the job meta. The zero value renders abc_url as
	// the bare /apps/<app>/ path and omits abc_url_ip — back-compat for callers
	// that don't populate this field yet.
	AppsDoors AppsDoors
	// ContentDigest is the sha256 of an uploaded `content:` payload, resolved
	// by the deploy command after upload. Empty for image-based apps.
	ContentDigest string
	// ContentIsFile records that the payload was a single file, which is
	// fetched as one object rather than as a prefix.
	ContentIsFile bool
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
	metaBody.SetAttributeValue("abc_url", cty.StringVal(s.URL(p.AppsDoors)))
	// abc_url_ip — bare-IP form of abc_url for private/shared apps. Users
	// without a campus DNS / hosts-file entry for the door use this. Omitted
	// for public-only apps and for any plane whose *DoorIP isn't configured
	// in the active context's admin.services.apps block.
	if u := s.URLIP(p.AppsDoors); u != "" {
		metaBody.SetAttributeValue("abc_url_ip", cty.StringVal(u))
	}
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

	// network — BRIDGE mode + a dynamic host port mapped to the app's declared
	// container port (`to = s.Port`). Each app runs in its own network namespace,
	// so every app may use its framework's default container port (e.g. 8501 for
	// every Streamlit) with NO host-port collision: Nomad allocates a unique
	// dynamic host port per alloc, and Traefik discovers it from the service
	// registration (we deliberately OMIT `loadbalancer.server.port` from the tags
	// so Traefik uses the registered dynamic port, not a hardcoded one). This
	// supersedes the host-net + static-port model, which collided whenever two
	// same-framework apps shared a container port on one node.
	netBody := groupBody.AppendNewBlock("network", nil).Body()
	portBody := netBody.AppendNewBlock("port", []string{"http"}).Body()
	portBody.SetAttributeValue("to", cty.NumberIntVal(int64(s.Port)))

	// service — Nomad-native (provider = "nomad"), NOT Consul. Service name
	// matches the job name so `abc app` and Traefik discover it consistently.
	svcBody := groupBody.AppendNewBlock("service", nil).Body()
	svcBody.SetAttributeValue("name", cty.StringVal(s.JobName()))
	svcBody.SetAttributeValue("provider", cty.StringVal("nomad"))
	svcBody.SetAttributeValue("port", cty.StringVal("http"))

	// Traefik routing tags, one router per network-reach plane (see Spec.Planes).
	// No `loadbalancer.server.port` — bridge networking gives the app a dynamic host
	// port; omitting the tag lets Traefik route to the port the service registered.
	//
	//   public  → router <job>-public:   Host(<app>.apps.seedling…)  on entrypoint web
	//   private → router <job>-internal:  PathPrefix(/apps/<app>)     on entrypoint private
	//   shared  → router <job>-internal:  PathPrefix(/apps/<app>)     on entrypoint shared
	//
	// The app keeps the SAME name across planes (public subdomain == private/shared
	// path segment). private+shared share one PathPrefix router with both entrypoints;
	// the plane an app is reachable on is decided by which door (entrypoint) the
	// request entered — sovereignty by routing.
	tags := []cty.Value{cty.StringVal("traefik.enable=true")}
	job := s.JobName()

	if s.HasPlane(ExposePublic) {
		r := job + "-public"
		tags = append(tags,
			cty.StringVal(fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", r, s.PublicHost())),
			cty.StringVal(fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", r)),
		)
	}

	internalEps := make([]string, 0, 2)
	if s.HasPlane(ExposePrivate) {
		internalEps = append(internalEps, ExposePrivate)
	}
	if s.HasPlane(ExposeShared) {
		internalEps = append(internalEps, ExposeShared)
	}
	if len(internalEps) > 0 {
		r := job + "-internal"
		tags = append(tags,
			cty.StringVal(fmt.Sprintf("traefik.http.routers.%s.rule=PathPrefix(`%s`)", r, s.AppPath())),
			cty.StringVal(fmt.Sprintf("traefik.http.routers.%s.entrypoints=%s", r, strings.Join(internalEps, ","))),
		)
		// stripPrefix middleware: when the container serves at `/` (typical for
		// `framework: custom` BYOI images), Traefik must strip the
		// `/apps/<project>-<name>` prefix before forwarding, else the upstream
		// 404s on every request. Framework presets (streamlit/shiny/pode) set
		// the prefix-aware mode natively (--server.baseUrlPath etc.) and would
		// double-prefix if stripped. Default is framework-derived; overridable
		// via `strip_prefix:` in abc-app.yaml. See Spec.StripPrefix docs.
		if s.StripPrefix != nil && *s.StripPrefix {
			mw := job + "-strip"
			tags = append(tags,
				cty.StringVal(fmt.Sprintf("traefik.http.middlewares.%s.stripprefix.prefixes=%s", mw, s.AppPath())),
				cty.StringVal(fmt.Sprintf("traefik.http.routers.%s.middlewares=%s@nomad-%s", r, mw, p.Namespace)),
			)
		}
		// PHASE 2 (sticky sessions): for stateful frameworks with replicas > 1,
		// append loadbalancer sticky-cookie tags on the <job> service here.
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

	// A `content:` app runs no application of its own: the platform supplies a
	// file server and Nomad fetches the content as an artifact before start, so
	// nothing is ever baked into an image. Caddy takes its root as an argument,
	// which is why it is used here — nginx would need a mounted config, and a
	// mounted config would reintroduce the image build this avoids.
	if strings.TrimSpace(s.Content) != "" {
		cfgBody.SetAttributeValue("image", cty.StringVal(StaticServerImage))
		cfgBody.SetAttributeValue("command", cty.StringVal("caddy"))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal("file-server"),
			cty.StringVal("--root"),
			// Absolute: the docker driver mounts the allocation's task directory
			// at /local inside the container, and caddy's working directory is
			// not that path, so a relative "local" resolves to nothing.
			cty.StringVal("/local"),
			cty.StringVal("--listen"),
			cty.StringVal(fmt.Sprintf(":%d", s.Port)),
		}))
	} else {
		cfgBody.SetAttributeValue("image", cty.StringVal(s.Image))
	}
	// Bridge networking: Nomad maps the allocated dynamic host port to the
	// container's declared port via the group `network` port "http" (`to = s.Port`).
	// The app binds 0.0.0.0:s.Port inside its own namespace; no host-port collision.
	cfgBody.SetAttributeValue("ports", cty.ListVal([]cty.Value{cty.StringVal("http")}))

	// artifact — Nomad fetches the content payload onto the node before the task
	// starts, into local/ where the file server is rooted. Credentials come from
	// the AWS_* vars platformEnv injects below.
	if src := contentArtifactSource(s, p); src != "" {
		artBody := taskBody.AppendNewBlock("artifact", nil).Body()
		artBody.SetAttributeValue("source", cty.StringVal(src))
		artBody.SetAttributeValue("destination", cty.StringVal("local/"))
		// The artifact getter runs as a separate subprocess and does NOT inherit
		// the task's env, so the AWS_* vars in the env block below are invisible
		// to it. Without credentials here it falls back to EC2 IMDS and fails
		// with "no EC2 IMDS role found". These are the same credentials already
		// present in the env block, so this exposes nothing new.
		if p.AWSAccessKey != "" {
			optBody := artBody.AppendNewBlock("options", nil).Body()
			optBody.SetAttributeValue("aws_access_key_id", cty.StringVal(p.AWSAccessKey))
			optBody.SetAttributeValue("aws_access_key_secret", cty.StringVal(p.AWSSecretKey))
		}
	}

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
		"ABC_APP_URL":      s.URL(p.AppsDoors),
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

// contentArtifactSource resolves where a static app's content is fetched from.
// A remote `content: s3://…` is served straight from that prefix — typically
// the results prefix a pipeline just wrote — so nothing is downloaded and
// re-uploaded. A local path is served from the digest the CLI uploaded.
func contentArtifactSource(s *Spec, p JobParams) string {
	c := strings.TrimSpace(s.Content)
	if c == "" {
		return ""
	}
	if IsRemoteContent(c) {
		return RemoteArtifactSource(p.MinIOEndpoint, c)
	}
	if strings.TrimSpace(p.ContentDigest) == "" {
		return ""
	}
	return ContentArtifactSource(p.MinIOEndpoint, s.Project, s.Name, p.ContentDigest, p.ContentIsFile)
}
