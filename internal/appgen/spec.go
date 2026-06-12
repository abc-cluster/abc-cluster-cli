// Package appgen models the `abc-app.yaml` scientific-app deployment
// descriptor, validates it, resolves framework defaults, and generates the
// Nomad `service` job HCL that `abc app deploy` submits.
//
// Spec: abc-universe specs/active/abc-app-deploy.md.
//
// Phase 1 scope: BYOI (image-only), frameworks pode/streamlit/shiny/custom,
// access: team only, single replica, seedling tier only. Subdomain routing
// (apps served at root, no path prefix), Caddy edge forward-auth, Traefik
// Nomad-provider discovery (no Consul). The CLI emits Traefik routing tags on
// the Nomad service block; it makes no out-of-band route-registration call.
package appgen

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// AppsDomain is the wildcard parent domain apps are served under. Each app is
// reachable at https://<project>-<name>.<AppsDomain> at the container root.
const AppsDomain = "apps.seedling.abc-cluster.cloud"

// DefaultNamespace is the Nomad namespace researcher-deployed apps run in.
// Overridable via the deploy command's --namespace flag.
const DefaultNamespace = "abc-apps"

// nameRe is the allowed character class for app names and projects.
var nameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// AccessRead / AccessReadWrite are the two supported data-access modes.
const (
	AccessRead      = "read"
	AccessReadWrite = "read-write"
)

// Exposure modes — the network-reach axis, orthogonal to Access (auth). It
// selects which Traefik Host zone the app's router rule uses:
//   - public:   under the public edge wildcard (*.apps.seedling…) → internet-reachable
//   - internal: off the public edge (under InternalAppsDomain) → Tailscale + campus LAN only
//   - both:     both host rules
//
// See abc-universe brainstorms/abc-scientific-apps/2026-06-07-app-exposure-internal-public-sovereignty.md.
const (
	ExposurePublic   = "public"
	ExposureInternal = "internal"
	ExposureBoth     = "both"
)

// Exposure planes — the `expose:` set (network-reach axis, orthogonal to access).
// Each plane is realised by a distinct routing target / Traefik entrypoint, and the
// app keeps the SAME stable name (Subdomain()) across all of them:
//   - public:  Host(<app>.apps.seedling…)  on entrypoint `web`     → GCP edge (internet)
//   - private: PathPrefix(/apps/<app>)      on entrypoint `private` → campus-IP:443 door
//   - shared:  PathPrefix(/apps/<app>)      on entrypoint `shared`  → overlay-VPN (Tailscale Serve)
// Changing `expose:` re-points routers; it never renames the app. See abc-universe
// brainstorms/abc-scientific-apps/2026-06-10-lan-only-app-reverse-proxy-port-constraint.md.
const (
	ExposePublic  = "public"
	ExposeShared  = "shared"
	ExposePrivate = "private"
)

// AppsPathPrefix is the parent path for private/shared apps: `/apps/<app>`. The
// private (campus-IP:443) and shared (Tailscale Serve) doors forward `/apps/*` to
// Traefik, which routes by PathPrefix — Host-agnostic, so it works on a bare IP.
const AppsPathPrefix = "/apps"

// AppsDoors carries the per-plane ingress door hostnames + IP forms the URL
// composers in this package use. EVERY field is supplied by the caller (from
// the active context's admin.services.apps block — see config.AppsService); no
// cluster-specific defaults are baked into source. Empty fields cause the URL
// composers to fall through to the next-priority plane or to the bare path.
//
// Populate via:
//
//	doors := appgen.AppsDoors{
//	    PublicDomain:  ctx.AppsPublicDomain(),
//	    PrivateDoor:   ctx.AppsPrivateDoor(),
//	    PrivateDoorIP: ctx.AppsPrivateDoorIP(),
//	    SharedDoor:    ctx.AppsSharedDoor(),
//	    SharedDoorIP:  ctx.AppsSharedDoorIP(),
//	}
type AppsDoors struct {
	PublicDomain  string
	PrivateDoor   string
	PrivateDoorIP string
	SharedDoor    string
	SharedDoorIP  string
}

// InternalAppsDomain is the parent domain for internal-only app hosts.
// Deliberately NOT under AppsDomain (*.apps.seedling…): the public edge only
// proxies that wildcard, so anything here has no public route by construction.
// Internal resolution (Tailscale MagicDNS / campus DNS) is operator infra — see
// abc-deployments/abc-seedling-prod/docs/internal-app-exposure.md. `.internal` is
// the ICANN-reserved private-use TLD.
const InternalAppsDomain = "apps.internal"

// CurrentSpecVersion is the current abc-app.yaml schema version. It is written
// first in the descriptor (mirroring the config.yaml `version` convention).
// Empty or the legacy "1" normalise to it; a different value is rejected so a
// newer-schema file is not silently mis-parsed by an older CLI.
const CurrentSpecVersion = "1.0"

// DataMount is one entry in the `data:` list — a MinIO bucket the app reads
// (or writes). Apps access buckets via injected AWS_* credentials, never a
// filesystem mount, so `path` is rejected.
type DataMount struct {
	Bucket string `yaml:"bucket"`
	Access string `yaml:"access,omitempty"`
	// Path is rejected in phase 1 — present only so a stray `path:` produces a
	// clear validation error rather than being silently ignored.
	Path string `yaml:"path,omitempty"`
}

// Spec is the parsed `abc-app.yaml`. Fields map 1:1 to the documented schema.
// Version is first (written first on save — see MarshalCanonical), mirroring
// config.yaml. The descriptor is parsed with KnownFields(true), so every field
// the user may set must appear here.
type Spec struct {
	Version       string            `yaml:"version,omitempty"`
	Name          string            `yaml:"name"`
	Image         string            `yaml:"image"`
	Project       string            `yaml:"project"`
	Framework     string            `yaml:"framework"`
	Port          int               `yaml:"port,omitempty"`
	Health        string            `yaml:"health,omitempty"`
	HealthTimeout string            `yaml:"health_timeout,omitempty"` // e.g. "3m"; overrides the deploy default
	Access        string            `yaml:"access,omitempty"`
	Expose        ExposePlanes      `yaml:"expose,omitempty"`   // network-reach planes: [public|shared|private]
	Exposure      string            `yaml:"exposure,omitempty"` // DEPRECATED legacy scalar (public|internal|both); maps to Expose
	// StripPrefix controls whether Traefik strips the `/apps/<project>-<name>`
	// PathPrefix before forwarding to the container, for apps using the private
	// or shared planes. Defaults are framework-derived in ApplyDefaults:
	//   custom  → true  (containers typically serve at `/`; the prefix would 404)
	//   others  → false (streamlit/shiny/pode/etc. are configured to serve under
	//                    the prefix via --server.baseUrlPath / H2O_WAVE_BASE_URL
	//                    / ABC_APP_URL and need the prefix to remain).
	// Set explicitly to override the framework default. No effect on apps using
	// only the public plane (Host-rule routing, no prefix).
	StripPrefix   *bool             `yaml:"strip_prefix,omitempty"`
	Replicas      int               `yaml:"replicas,omitempty"`
	Env           map[string]string `yaml:"env,omitempty"`
	Data          []DataMount       `yaml:"data,omitempty"`
	Resources     Resources         `yaml:"resources,omitempty"`

	// Source is rejected in phase 1 (no cluster-side build path). Declared so a
	// stray `source:` produces a clear error instead of being ignored.
	Source string `yaml:"source,omitempty"`
}

// ExposePlanes is the network-reach plane set parsed from abc-app.yaml's
// `expose:` key. Underlying representation is []string so every consumer
// (range loops, len, conversion to []string for normPlanes) works unchanged.
//
// The custom UnmarshalYAML accepts either a YAML scalar (one plane) or a
// YAML sequence (multiple planes) — both forms are equivalent and idiomatic:
//
//	expose: private              # scalar — one plane
//	expose: [private, shared]    # sequence — many planes
//	expose: public               # scalar
//	expose:                      # block sequence — also valid
//	  - public
//	  - shared
//
// MarshalYAML renders the canonical form back: a scalar when len == 1, a
// flow-style sequence otherwise. The order-preserving canonical marshal in
// spec_yaml_ordered.go decides serialization for `abc app spec show` etc.;
// this MarshalYAML is the fallback for any direct yaml.Marshal call.
type ExposePlanes []string

// UnmarshalYAML implements yaml.Unmarshaler. See ExposePlanes for the accepted
// shapes. An empty / null value yields a nil slice.
func (e *ExposePlanes) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		// yaml.v3 represents an explicit `null` / `~` as a scalar with tag
		// !!null — yaml.Decode into a string then yields "" for that case.
		var s string
		if err := node.Decode(&s); err != nil {
			return fmt.Errorf("expose: invalid scalar at line %d: %w", node.Line, err)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*e = nil
			return nil
		}
		*e = ExposePlanes{s}
		return nil
	case yaml.SequenceNode:
		var arr []string
		if err := node.Decode(&arr); err != nil {
			return fmt.Errorf("expose: invalid sequence at line %d: %w", node.Line, err)
		}
		*e = ExposePlanes(arr)
		return nil
	default:
		return fmt.Errorf("expose: must be a string or a list of strings (line %d)", node.Line)
	}
}

// MarshalYAML implements yaml.Marshaler. Single-entry plane sets serialize as
// a scalar; multi-entry sets serialize as a flow sequence. nil/empty serializes
// as a nil value (so the `omitempty` tag on the field works).
func (e ExposePlanes) MarshalYAML() (any, error) {
	switch len(e) {
	case 0:
		return nil, nil
	case 1:
		return e[0], nil
	default:
		return []string(e), nil
	}
}

// Resources holds the declared hard resource limits applied to the Nomad task.
type Resources struct {
	CPU    int `yaml:"cpu,omitempty"`    // MHz; default 500
	Memory int `yaml:"memory,omitempty"` // MiB; default 1024
}

// frameworkDefault carries the port + health-path defaults for a framework and
// whether it is stateful (phase-2 sticky-cookie classification, documented but
// not emitted in phase 1).
type frameworkDefault struct {
	port      int
	health    string
	stateful  bool
	supported bool // false → recognised but not supported in phase 1
}

// frameworkDefaults is the framework table from the spec. dash/panel/voila are
// recognised (so they reject with a precise "not yet supported in phase 1"
// message) but unsupported in phase 1.
var frameworkDefaults = map[string]frameworkDefault{
	"streamlit": {port: 8501, health: "/_stcore/health", stateful: true, supported: true},
	"shiny":     {port: 3838, health: "/", stateful: true, supported: true},
	"pode":      {port: 8085, health: "/health/live", stateful: false, supported: true},
	"dash":      {port: 8050, health: "/", stateful: true, supported: false},
	"panel":     {port: 5006, health: "/", stateful: true, supported: false},
	"voila":     {port: 8866, health: "/", stateful: true, supported: false},
	// custom has no defaults — port + health are mandatory.
	"custom": {supported: true},
}

const (
	defaultCPU    = 500
	defaultMemory = 1024
	maxNameLen    = 48
)

// Validate checks the spec against all phase-1 rules and returns the first
// violation as a human-readable error. It does NOT touch the network (bucket
// existence is checked separately by the DataProvisioner before any Nomad or
// Vault side effects).
//
// Format-only: it validates field shape and the phase-1 value set; it does not
// verify that the project is a group the user belongs to (that gate lives at
// the auth-svc /validate edge) — only that `project` is present and well-formed.
func (s *Spec) Validate() error {
	// version — schema version, written first (mirrors config.yaml). Empty and
	// the legacy "1" are accepted (normalised to CurrentSpecVersion); any other
	// value is rejected so a newer-schema descriptor is not silently mis-parsed.
	switch strings.TrimSpace(s.Version) {
	case "", "1", CurrentSpecVersion:
		// ok
	default:
		return fmt.Errorf("`version` %q is not supported by this CLI (supports %s); upgrade abc or check the abc-app.yaml schema version", s.Version, CurrentSpecVersion)
	}

	// name
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("`name` is required")
	}
	if len(s.Name) > maxNameLen {
		return fmt.Errorf("`name` is too long (%d chars); max %d", len(s.Name), maxNameLen)
	}
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("`name` %q is invalid; must match [a-z0-9-]+", s.Name)
	}

	// source — rejected before image so the migration hint is shown.
	if strings.TrimSpace(s.Source) != "" {
		return fmt.Errorf("`source:` is not yet supported (use `image:`); there is no cluster-side build path in phase 1")
	}

	// image
	if strings.TrimSpace(s.Image) == "" {
		return fmt.Errorf("`image` is required and must be a fully-qualified OCI image reference (e.g. ghcr.io/org/app:tag); there is no source-based or script-based deploy path")
	}
	if !looksLikeImageRef(s.Image) {
		return fmt.Errorf("`image` %q does not look like a fully-qualified OCI image reference (expected registry/repo[:tag])", s.Image)
	}

	// project
	if strings.TrimSpace(s.Project) == "" {
		return fmt.Errorf("`project` is required; it is the key for the `access: team` group check and data-bucket scoping (e.g. mtb-resistotyper-ml, or abc-platform for platform tooling)")
	}
	if !nameRe.MatchString(s.Project) {
		return fmt.Errorf("`project` %q is invalid; must match [a-z0-9-]+", s.Project)
	}

	// framework
	fw := strings.ToLower(strings.TrimSpace(s.Framework))
	if fw == "" {
		return fmt.Errorf("`framework` is required; one of: streamlit, shiny, pode, custom (phase 1)")
	}
	def, known := frameworkDefaults[fw]
	if !known {
		return fmt.Errorf("`framework` %q is not recognised; valid values: streamlit, shiny, dash, panel, voila, pode, custom", s.Framework)
	}
	if !def.supported {
		return fmt.Errorf("`framework: %s` is not yet supported in phase 1; supported: streamlit, shiny, pode, custom", fw)
	}
	if fw == "custom" {
		if s.Port == 0 {
			return fmt.Errorf("`framework: custom` requires an explicit `port`")
		}
		if strings.TrimSpace(s.Health) == "" {
			return fmt.Errorf("`framework: custom` requires an explicit `health` path")
		}
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("`port` %d is out of range (1-65535)", s.Port)
	}

	// access
	access := strings.ToLower(strings.TrimSpace(s.Access))
	if access == "" {
		access = "team"
	}
	switch access {
	case "team":
		// ok
	case "cluster", "public":
		return fmt.Errorf("`access: %s` is not yet supported in phase 1 (only `team`); cluster and public are reserved for phase 2", access)
	default:
		return fmt.Errorf("`access` %q is invalid; phase 1 supports only `team`", s.Access)
	}

	// exposure (DEPRECATED legacy scalar) — kept for back-compat; maps to `expose`.
	switch strings.ToLower(strings.TrimSpace(s.Exposure)) {
	case "", ExposurePublic, ExposureInternal, ExposureBoth:
		// ok
	default:
		return fmt.Errorf("`exposure` %q is invalid; use `internal`, `public`, or `both` (or the newer `expose:` set)", s.Exposure)
	}
	// expose — the network-reach plane set, orthogonal to access. Each entry must be
	// public/shared/private. Cannot be combined with the legacy `exposure:` scalar.
	if len(s.Expose) > 0 {
		if strings.TrimSpace(s.Exposure) != "" {
			return fmt.Errorf("set either `expose:` (the plane list) or the legacy `exposure:`, not both")
		}
		for _, p := range s.Expose {
			switch strings.ToLower(strings.TrimSpace(p)) {
			case ExposePublic, ExposeShared, ExposePrivate:
				// ok
			default:
				return fmt.Errorf("`expose` value %q is invalid; use `public`, `shared`, and/or `private`", p)
			}
		}
	}

	// replicas
	if s.Replicas < 0 {
		return fmt.Errorf("`replicas` %d is invalid", s.Replicas)
	}
	if s.Replicas > 1 {
		return fmt.Errorf("`replicas: %d` is not yet supported in phase 1 (single replica only); multi-replica load balancing is phase 2", s.Replicas)
	}

	// resources
	if s.Resources.CPU < 0 {
		return fmt.Errorf("`resources.cpu` %d is invalid", s.Resources.CPU)
	}
	if s.Resources.Memory < 0 {
		return fmt.Errorf("`resources.memory` %d is invalid", s.Resources.Memory)
	}

	// data
	for i, d := range s.Data {
		if strings.TrimSpace(d.Bucket) == "" {
			return fmt.Errorf("`data[%d].bucket` is required", i)
		}
		if strings.TrimSpace(d.Path) != "" {
			return fmt.Errorf("`data[%d].path` is not supported; apps access MinIO via the injected AWS_* credentials + ABC_MINIO_ENDPOINT, not via filesystem mounts", i)
		}
		acc := strings.ToLower(strings.TrimSpace(d.Access))
		if acc == "" {
			acc = AccessRead
		}
		if acc != AccessRead && acc != AccessReadWrite {
			return fmt.Errorf("`data[%d].access` %q is invalid; use `read` (default) or `read-write`", i, d.Access)
		}
	}

	return nil
}

// ApplyDefaults fills framework-derived port/health and the resource defaults,
// and normalises access/replicas. Call after Validate. Mutates the receiver so
// `abc app show` / `--dry-run` reflect the resolved (post-default) values.
func (s *Spec) ApplyDefaults() {
	s.Version = normalizeSpecVersion(s.Version)
	fw := s.NormFramework()
	def := frameworkDefaults[fw]
	if s.Port == 0 {
		s.Port = def.port
	}
	if strings.TrimSpace(s.Health) == "" {
		s.Health = def.health
	}
	if s.Resources.CPU == 0 {
		s.Resources.CPU = defaultCPU
	}
	if s.Resources.Memory == 0 {
		s.Resources.Memory = defaultMemory
	}
	if s.Replicas == 0 {
		s.Replicas = 1
	}
	if strings.TrimSpace(s.Access) == "" {
		s.Access = "team"
	} else {
		s.Access = strings.ToLower(strings.TrimSpace(s.Access))
	}
	// StripPrefix default: when the user didn't set it, derive from framework.
	// `custom` containers are BYOI — they typically serve at `/` and would 404
	// on the `/apps/<project>-<name>/...` prefix Traefik forwards. The framework
	// presets (streamlit/shiny/pode) are configured to serve under the prefix
	// natively (--server.baseUrlPath etc.) and need the prefix to remain.
	if s.StripPrefix == nil {
		v := strings.ToLower(strings.TrimSpace(s.Framework)) == "custom"
		s.StripPrefix = &v
	}
	// Normalise the network-reach planes into s.Expose (the canonical form):
	//   explicit `expose:` wins; else map the legacy `exposure:`; else default public.
	if len(s.Expose) > 0 {
		s.Expose = ExposePlanes(normPlanes([]string(s.Expose)))
		s.Exposure = "" // canonicalise on the new field
	} else {
		switch strings.ToLower(strings.TrimSpace(s.Exposure)) {
		case ExposureInternal:
			s.Expose = ExposePlanes{ExposeShared, ExposePrivate}
		case ExposureBoth:
			s.Expose = ExposePlanes{ExposePublic, ExposeShared, ExposePrivate}
		default: // "" or public → public (preserves phase-1 default)
			s.Expose = ExposePlanes{ExposePublic}
		}
		s.Exposure = ""
	}
	s.Framework = fw
	for i := range s.Data {
		if strings.TrimSpace(s.Data[i].Access) == "" {
			s.Data[i].Access = AccessRead
		} else {
			s.Data[i].Access = strings.ToLower(strings.TrimSpace(s.Data[i].Access))
		}
	}
}

// normalizeSpecVersion maps empty or legacy "1" to CurrentSpecVersion
// (mirrors config.normalizeConfigFileVersionForSave).
func normalizeSpecVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "1" {
		return CurrentSpecVersion
	}
	return v
}

// NormFramework returns the lowercased, trimmed framework value.
func (s *Spec) NormFramework() string {
	return strings.ToLower(strings.TrimSpace(s.Framework))
}

// Stateful reports the framework's phase-2 sticky-cookie classification.
// Documented for the multi-replica path; not emitted in phase 1.
func (s *Spec) Stateful() bool {
	return frameworkDefaults[s.NormFramework()].stateful
}

// Subdomain returns the flat `<project>-<name>` host label.
func (s *Spec) Subdomain() string {
	return s.Project + "-" + s.Name
}

// NormExposure returns a legacy {public|internal|both} summary derived from the
// normalised plane set — for back-compat with callers/meta that predate `expose:`.
func (s *Spec) NormExposure() string {
	pub := s.HasPlane(ExposePublic)
	internal := s.HasPlane(ExposeShared) || s.HasPlane(ExposePrivate)
	switch {
	case pub && internal:
		return ExposureBoth
	case internal:
		return ExposureInternal
	default:
		return ExposurePublic
	}
}

// Planes returns the normalised network-reach plane set (from `expose:`, the legacy
// `exposure:`, or the default). Order is canonical: public, shared, private.
func (s *Spec) Planes() []string {
	if len(s.Expose) > 0 {
		return normPlanes([]string(s.Expose))
	}
	switch strings.ToLower(strings.TrimSpace(s.Exposure)) {
	case ExposureInternal:
		return []string{ExposeShared, ExposePrivate}
	case ExposureBoth:
		return []string{ExposePublic, ExposeShared, ExposePrivate}
	default:
		return []string{ExposePublic}
	}
}

// HasPlane reports whether the app is exposed on the given plane.
func (s *Spec) HasPlane(plane string) bool {
	for _, p := range s.Planes() {
		if p == plane {
			return true
		}
	}
	return false
}

// AppPath is the stable path segment for the private/shared planes: `/apps/<app>`.
// The app must serve under this base path (Wave H2O_WAVE_BASE_URL, Streamlit
// --server.baseUrlPath); the doors forward `/apps/*` and Traefik PathPrefix-routes it.
func (s *Spec) AppPath() string {
	return AppsPathPrefix + "/" + s.Subdomain()
}

// normPlanes lowercases, dedupes, and canonically orders a plane list.
func normPlanes(in []string) []string {
	seen := map[string]bool{}
	for _, p := range in {
		seen[strings.ToLower(strings.TrimSpace(p))] = true
	}
	out := make([]string, 0, 3)
	for _, p := range []string{ExposePublic, ExposeShared, ExposePrivate} {
		if seen[p] {
			out = append(out, p)
		}
	}
	return out
}

// PublicHost is the public-edge host, under the *.apps.seedling… wildcard.
func (s *Spec) PublicHost() string {
	return s.Subdomain() + "." + AppsDomain
}

// InternalHost is the internal-only host, off the public edge wildcard.
func (s *Spec) InternalHost() string {
	return s.Subdomain() + "." + InternalAppsDomain
}

// Hosts returns the routing Host(s) for the spec's exposure, in priority order
// (primary first). The Traefik router rule ORs these together.
func (s *Spec) Hosts() []string {
	switch s.NormExposure() {
	case ExposureInternal:
		return []string{s.InternalHost()}
	case ExposureBoth:
		return []string{s.PublicHost(), s.InternalHost()}
	default: // public
		return []string{s.PublicHost()}
	}
}

// Host returns the primary external Host for routing (Hosts()[0]). For public
// and both this is the public-edge host (unchanged); for internal it is the
// internal-only host.
func (s *Spec) Host() string {
	return s.Hosts()[0]
}

// URL returns the primary clickable app URL using the supplied per-deployment
// AppsDoors (the active context's admin.services.apps block). Preference:
//
//	public  → https://<sub>.<doors.PublicDomain>/  (falls back to the build-time
//	          AppsDomain const when doors.PublicDomain is empty — back-compat;
//	          to be removed once every context surface migrates to the new field.)
//	private → https://<doors.PrivateDoor>/apps/<sub>/
//	shared  → https://<doors.SharedDoor>/apps/<sub>/  (falls back to PrivateDoor
//	          when SharedDoor is empty — Traefik routes the PathPrefix on either
//	          entrypoint, so the private door reaches the same backing service).
//
// When every relevant door is empty, returns the bare /apps/<sub>/ path as a
// last resort (a hint, not a clickable URL).
func (s *Spec) URL(doors AppsDoors) string {
	if s.HasPlane(ExposePublic) {
		dom := strings.TrimSpace(doors.PublicDomain)
		if dom == "" {
			// Back-compat: callers that didn't populate doors.PublicDomain get the
			// build-time AppsDomain const. New code should pass doors with this
			// field populated (from ctx.AppsPublicDomain()).
			dom = AppsDomain
		}
		return "https://" + s.Subdomain() + "." + dom
	}
	if s.HasPlane(ExposePrivate) && strings.TrimSpace(doors.PrivateDoor) != "" {
		return "https://" + doors.PrivateDoor + s.AppPath() + "/"
	}
	if s.HasPlane(ExposeShared) {
		if h := strings.TrimSpace(doors.SharedDoor); h != "" {
			return "https://" + h + s.AppPath() + "/"
		}
		if h := strings.TrimSpace(doors.PrivateDoor); h != "" {
			return "https://" + h + s.AppPath() + "/"
		}
	}
	return s.AppPath() + "/"
}

// URLIP returns the bare-IP form of URL for private/shared apps — useful for
// users without DNS / hosts-file resolution for the door host. Composes from
// doors.PrivateDoorIP (or SharedDoorIP, when only shared is exposed and
// configured). Returns "" when:
//   - the app is public-only (the wildcard host is the URL),
//   - the app uses no path-prefix plane, or
//   - no relevant *DoorIP is configured.
//
// The TLS cert at the door must include the IP as a SAN for browsers not to
// warn — operators are responsible for that on their deployments.
func (s *Spec) URLIP(doors AppsDoors) string {
	if !s.HasPlane(ExposePrivate) && !s.HasPlane(ExposeShared) {
		return ""
	}
	if s.HasPlane(ExposePrivate) && strings.TrimSpace(doors.PrivateDoorIP) != "" {
		return "https://" + doors.PrivateDoorIP + s.AppPath() + "/"
	}
	if s.HasPlane(ExposeShared) {
		if ip := strings.TrimSpace(doors.SharedDoorIP); ip != "" {
			return "https://" + ip + s.AppPath() + "/"
		}
		if ip := strings.TrimSpace(doors.PrivateDoorIP); ip != "" {
			return "https://" + ip + s.AppPath() + "/"
		}
	}
	return ""
}

// JobName returns the Nomad job (and service) name: `app-<project>-<name>`.
func (s *Spec) JobName() string {
	return "app-" + s.Project + "-" + s.Name
}

// ServiceAccountName returns the Vault/MinIO service-account name for this app:
// `abc-app-<project>-<name>`.
func (s *Spec) ServiceAccountName() string {
	return "abc-app-" + s.Project + "-" + s.Name
}

// looksLikeImageRef does a cheap structural check that an image string is a
// fully-qualified OCI reference (has a registry/repo shape). It deliberately
// does not attempt a full grammar — Nomad/the registry are the real authority;
// this just catches obviously-wrong values (bare words, empty tags) early.
func looksLikeImageRef(img string) bool {
	img = strings.TrimSpace(img)
	if img == "" {
		return false
	}
	// Must contain a slash (registry/repo or org/repo) OR a tag/digest on a
	// known-registry-less form is too ambiguous — require a path separator.
	if !strings.Contains(img, "/") {
		return false
	}
	// Reject a trailing ':' with no tag.
	if strings.HasSuffix(img, ":") {
		return false
	}
	return true
}
