package accounting

import (
	"strconv"
	"strings"
	"testing"
)

func TestZADefaults_AllRatesHaveCitations(t *testing.T) {
	rc := ZADefaults()

	type field struct {
		name string
		rv   RateValue
	}
	fields := []field{
		{"cost.cpu_hour", rc.Cost.CpuHour},
		{"cost.gpu_hour", rc.Cost.GpuHour},
		{"cost.memory_gb_hour", rc.Cost.MemoryGbHour},
		{"emissions.grid_factor_gco2_per_kwh", rc.Emissions.GridFactorGco2PerKwh},
		{"emissions.cpu_w", rc.Emissions.CpuW},
		{"emissions.gpu_w", rc.Emissions.GpuW},
		{"emissions.memory_gb_w", rc.Emissions.MemoryGbW},
		{"emissions.pue", rc.Emissions.Pue},
	}
	for _, f := range fields {
		if f.rv.Source != SourceBuiltIn {
			t.Errorf("%s: Source = %q, want built-in", f.name, f.rv.Source)
		}
		if strings.TrimSpace(f.rv.Citation) == "" {
			t.Errorf("%s: empty citation", f.name)
		}
	}
	if rc.Currency.Value != "ZAR" {
		t.Errorf("Currency = %q, want ZAR", rc.Currency.Value)
	}
	if rc.Currency.Source != SourceBuiltIn {
		t.Errorf("Currency.Source = %q, want built-in", rc.Currency.Source)
	}
	if strings.TrimSpace(rc.Currency.Citation) == "" {
		t.Errorf("Currency.Citation is empty")
	}
}

func TestZADefaults_ValuesWithinValidationRanges(t *testing.T) {
	rc := ZADefaults()
	costs := map[string]float64{
		KeyCostCpuHour:      rc.Cost.CpuHour.Value,
		KeyCostGpuHour:      rc.Cost.GpuHour.Value,
		KeyCostMemoryGbHour: rc.Cost.MemoryGbHour.Value,
	}
	for k, v := range costs {
		if _, _, err := ValidateAccountingValue(k, strconv.FormatFloat(v, 'f', -1, 64)); err != nil {
			t.Errorf("%s: built-in default %v fails validation: %v", k, v, err)
		}
	}
	emis := map[string]float64{
		KeyGridFactor: rc.Emissions.GridFactorGco2PerKwh.Value,
		KeyCpuW:       rc.Emissions.CpuW.Value,
		KeyGpuW:       rc.Emissions.GpuW.Value,
		KeyMemoryGbW:  rc.Emissions.MemoryGbW.Value,
		KeyPue:        rc.Emissions.Pue.Value,
	}
	for k, v := range emis {
		if _, err := ValidateEmissionsValue(k, strconv.FormatFloat(v, 'f', -1, 64)); err != nil {
			t.Errorf("%s: built-in default %v fails validation: %v", k, v, err)
		}
	}
	if _, _, err := ValidateAccountingValue(KeyCurrency, rc.Currency.Value); err != nil {
		t.Errorf("currency built-in default %q fails validation: %v", rc.Currency.Value, err)
	}
}
