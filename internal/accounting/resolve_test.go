package accounting

import (
	"testing"
	"time"
)

func TestResolve_PureBuiltIn(t *testing.T) {
	z := ZADefaults()
	out, err := Resolve(z, LayeredOverrides{
		Accounting: map[string]string{},
		Emissions:  map[string]string{},
	}, FlagOverrides{
		Accounting: map[string]string{},
		Emissions:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Cost.CpuHour.Source != SourceBuiltIn {
		t.Errorf("CpuHour.Source = %q, want built-in", out.Cost.CpuHour.Source)
	}
	if out.Cost.CpuHour.Value != ZADefaultCostCpuHour {
		t.Errorf("CpuHour.Value = %v, want %v", out.Cost.CpuHour.Value, ZADefaultCostCpuHour)
	}
}

func TestResolve_ConfigOverride(t *testing.T) {
	z := ZADefaults()
	mtime := time.Date(2026, 5, 6, 14, 23, 0, 0, time.UTC)
	out, err := Resolve(z, LayeredOverrides{
		Mtime:      mtime,
		Accounting: map[string]string{KeyCostCpuHour: "0.45"},
		Emissions:  map[string]string{KeyPue: "1.27"},
	}, FlagOverrides{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Cost.CpuHour.Source != SourceConfig {
		t.Errorf("CpuHour.Source = %q, want config", out.Cost.CpuHour.Source)
	}
	if out.Cost.CpuHour.Value != 0.45 {
		t.Errorf("CpuHour.Value = %v, want 0.45", out.Cost.CpuHour.Value)
	}
	if !out.Cost.CpuHour.UpdatedAt.Equal(mtime) {
		t.Errorf("CpuHour.UpdatedAt = %v, want %v", out.Cost.CpuHour.UpdatedAt, mtime)
	}
	if out.Cost.CpuHour.Citation != "" {
		t.Errorf("CpuHour.Citation should be empty for config source, got %q", out.Cost.CpuHour.Citation)
	}
	if out.Emissions.Pue.Source != SourceConfig || out.Emissions.Pue.Value != 1.27 {
		t.Errorf("Pue override failed: %+v", out.Emissions.Pue)
	}
	// Untouched field stays built-in.
	if out.Cost.GpuHour.Source != SourceBuiltIn {
		t.Errorf("GpuHour.Source = %q, want built-in (untouched)", out.Cost.GpuHour.Source)
	}
}

func TestResolve_FlagOverrideBeatsConfig(t *testing.T) {
	z := ZADefaults()
	before := time.Now()
	out, err := Resolve(z, LayeredOverrides{
		Mtime:      time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC),
		Accounting: map[string]string{KeyCostCpuHour: "0.45"},
	}, FlagOverrides{
		Accounting: map[string]string{KeyCostCpuHour: "0.55"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Cost.CpuHour.Source != SourceFlag {
		t.Errorf("CpuHour.Source = %q, want flag", out.Cost.CpuHour.Source)
	}
	if out.Cost.CpuHour.Value != 0.55 {
		t.Errorf("CpuHour.Value = %v, want 0.55", out.Cost.CpuHour.Value)
	}
	if out.Cost.CpuHour.UpdatedAt.Before(before) {
		t.Errorf("flag UpdatedAt should be ~now")
	}
}

func TestResolve_ValidationFailures(t *testing.T) {
	z := ZADefaults()
	cases := []struct {
		name   string
		layer1 LayeredOverrides
		layer2 FlagOverrides
		want   string
	}{
		{
			name:   "negative cpu_hour at config",
			layer1: LayeredOverrides{Accounting: map[string]string{KeyCostCpuHour: "-1"}},
			want:   "cost.cpu_hour",
		},
		{
			name:   "pue out of range at config",
			layer1: LayeredOverrides{Emissions: map[string]string{KeyPue: "0.5"}},
			want:   "pue",
		},
		{
			name:   "grid_factor too large at flag",
			layer2: FlagOverrides{Emissions: map[string]string{KeyGridFactor: "9999"}},
			want:   "grid_factor",
		},
		{
			name:   "currency not iso",
			layer1: LayeredOverrides{Accounting: map[string]string{KeyCurrency: "zar"}},
			want:   "currency",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Resolve(z, c.layer1, c.layer2)
			if err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			}
			if !contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err.Error(), c.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
