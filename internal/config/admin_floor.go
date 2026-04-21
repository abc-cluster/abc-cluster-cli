package config

import (
	"fmt"
	"strings"
)

func floorPtr(s *AdminServices, svc string) **AdminFloorService {
	switch svc {
	case "minio":
		return &s.MinIO
	case "tusd":
		return &s.Tusd
	case "faasd":
		return &s.Faasd
	case "grafana":
		return &s.Grafana
	case "grafana_alloy":
		return &s.GrafanaAlloy
	case "prometheus":
		return &s.Prometheus
	case "loki":
		return &s.Loki
	case "ntfy":
		return &s.Ntfy
	case "rustfs":
		return &s.Rustfs
	case "vault":
		return &s.Vault
	case "traefik":
		return &s.Traefik
	case "uppy":
		return &s.Uppy
	default:
		return nil
	}
}

// GetAdminFloorField returns admin.services.<svc>.<field>.
// Known fields: http, endpoint, access_key, secret_key, user, password, ping_entrypoint, dashboard.
func GetAdminFloorField(s *AdminServices, svc, field string) (string, bool) {
	pp := floorPtr(s, svc)
	if pp == nil {
		return "", false
	}
	if *pp == nil {
		return "", false
	}
	fs := *pp
	switch field {
	case "http":
		v := strings.TrimSpace(fs.HTTP)
		return v, v != ""
	case "endpoint":
		v := strings.TrimSpace(fs.Endpoint)
		return v, v != ""
	case "access_key":
		v := strings.TrimSpace(fs.AccessKey)
		return v, v != ""
	case "secret_key":
		v := strings.TrimSpace(fs.SecretKey)
		return v, v != ""
	case "user":
		v := strings.TrimSpace(fs.User)
		return v, v != ""
	case "password":
		v := strings.TrimSpace(fs.Password)
		return v, v != ""
	case "ping_entrypoint":
		v := strings.TrimSpace(fs.PingEntryPoint)
		return v, v != ""
	case "dashboard":
		v := strings.TrimSpace(fs.Dashboard)
		return v, v != ""
	default:
		return "", false
	}
}

// SetAdminFloorField sets admin.services.<svc>.<field>.
func SetAdminFloorField(s *AdminServices, svc, field, value string) error {
	pp := floorPtr(s, svc)
	if pp == nil {
		return fmt.Errorf("unknown admin.services floor service %q", svc)
	}
	switch field {
	case "http", "endpoint", "access_key", "secret_key", "user", "password", "ping_entrypoint", "dashboard":
	default:
		return fmt.Errorf("unknown field %q for admin.services.%s", field, svc)
	}
	if *pp == nil {
		*pp = &AdminFloorService{}
	}
	fs := *pp
	switch field {
	case "http":
		fs.HTTP = value
	case "endpoint":
		fs.Endpoint = value
	case "access_key":
		fs.AccessKey = value
	case "secret_key":
		fs.SecretKey = value
	case "user":
		fs.User = value
	case "password":
		fs.Password = value
	case "ping_entrypoint":
		fs.PingEntryPoint = value
	case "dashboard":
		fs.Dashboard = value
	}
	if fs.IsEmpty() {
		*pp = nil
	}
	return nil
}

// UnsetAdminFloorField clears one field on admin.services.<svc>.
func UnsetAdminFloorField(s *AdminServices, svc, field string) error {
	pp := floorPtr(s, svc)
	if pp == nil || *pp == nil {
		return nil
	}
	fs := *pp
	switch field {
	case "http":
		fs.HTTP = ""
	case "endpoint":
		fs.Endpoint = ""
	case "access_key":
		fs.AccessKey = ""
	case "secret_key":
		fs.SecretKey = ""
	case "user":
		fs.User = ""
	case "password":
		fs.Password = ""
	case "ping_entrypoint":
		fs.PingEntryPoint = ""
	case "dashboard":
		fs.Dashboard = ""
	default:
		return fmt.Errorf("unknown field %q for admin.services.%s", field, svc)
	}
	if fs.IsEmpty() {
		*pp = nil
	}
	return nil
}

// NormalizeFloorServices clears empty admin.services floor blocks.
func NormalizeFloorServices(ctx *Context) {
	s := &ctx.Admin.Services
	s.MinIO = nilIfFloorEmpty(s.MinIO)
	s.Tusd = nilIfFloorEmpty(s.Tusd)
	s.Faasd = nilIfFloorEmpty(s.Faasd)
	s.Grafana = nilIfFloorEmpty(s.Grafana)
	s.GrafanaAlloy = nilIfFloorEmpty(s.GrafanaAlloy)
	s.Prometheus = nilIfFloorEmpty(s.Prometheus)
	s.Loki = nilIfFloorEmpty(s.Loki)
	s.Ntfy = nilIfFloorEmpty(s.Ntfy)
	s.Rustfs = nilIfFloorEmpty(s.Rustfs)
	s.Vault = nilIfFloorEmpty(s.Vault)
	s.Traefik = nilIfFloorEmpty(s.Traefik)
	s.Uppy = nilIfFloorEmpty(s.Uppy)
}

func nilIfFloorEmpty(a *AdminFloorService) *AdminFloorService {
	if a == nil || a.IsEmpty() {
		return nil
	}
	return a
}

// AppendAdminFloorAllKeys appends non-empty floor service keys for list output.
func AppendAdminFloorAllKeys(prefix string, s AdminServices, out [][2]string) [][2]string {
	type pair struct{ svc, field, val string }
	var pairs []pair
	add := func(svc string, fs *AdminFloorService) {
		if fs == nil {
			return
		}
		if v := strings.TrimSpace(fs.HTTP); v != "" {
			pairs = append(pairs, pair{svc, "http", v})
		}
		if v := strings.TrimSpace(fs.Endpoint); v != "" {
			pairs = append(pairs, pair{svc, "endpoint", v})
		}
		if v := strings.TrimSpace(fs.AccessKey); v != "" {
			pairs = append(pairs, pair{svc, "access_key", v})
		}
		if v := strings.TrimSpace(fs.SecretKey); v != "" {
			pairs = append(pairs, pair{svc, "secret_key", v})
		}
		if v := strings.TrimSpace(fs.User); v != "" {
			pairs = append(pairs, pair{svc, "user", v})
		}
		if v := strings.TrimSpace(fs.Password); v != "" {
			pairs = append(pairs, pair{svc, "password", v})
		}
		if v := strings.TrimSpace(fs.PingEntryPoint); v != "" {
			pairs = append(pairs, pair{svc, "ping_entrypoint", v})
		}
	}
	add("minio", s.MinIO)
	add("tusd", s.Tusd)
	add("faasd", s.Faasd)
	add("grafana", s.Grafana)
	add("grafana_alloy", s.GrafanaAlloy)
	add("prometheus", s.Prometheus)
	add("loki", s.Loki)
	add("ntfy", s.Ntfy)
	add("rustfs", s.Rustfs)
	add("vault", s.Vault)
	add("traefik", s.Traefik)
	add("uppy", s.Uppy)
	for _, p := range pairs {
		v := p.val
		switch p.field {
		case "access_key", "secret_key", "password":
			v = maskToken(v)
		}
		out = append(out, [2]string{prefix + ".admin.services." + p.svc + "." + p.field, v})
	}
	return out
}
