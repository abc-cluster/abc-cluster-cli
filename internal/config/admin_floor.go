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
	case "grafana":
		return &s.Grafana
	case "prometheus":
		return &s.Prometheus
	case "loki":
		return &s.Loki
	case "ntfy":
		return &s.Ntfy
	case "rustfs":
		return &s.Rustfs
	default:
		return nil
	}
}

// GetAdminFloorField returns admin.services.<svc>.<field> (http or endpoint).
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
	case "http", "endpoint":
	default:
		return fmt.Errorf("unknown field %q for admin.services.%s (want http or endpoint)", field, svc)
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
	s.Grafana = nilIfFloorEmpty(s.Grafana)
	s.Prometheus = nilIfFloorEmpty(s.Prometheus)
	s.Loki = nilIfFloorEmpty(s.Loki)
	s.Ntfy = nilIfFloorEmpty(s.Ntfy)
	s.Rustfs = nilIfFloorEmpty(s.Rustfs)
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
	}
	add("minio", s.MinIO)
	add("tusd", s.Tusd)
	add("grafana", s.Grafana)
	add("prometheus", s.Prometheus)
	add("loki", s.Loki)
	add("ntfy", s.Ntfy)
	add("rustfs", s.Rustfs)
	for _, p := range pairs {
		out = append(out, [2]string{prefix + ".admin.services." + p.svc + "." + p.field, p.val})
	}
	return out
}
