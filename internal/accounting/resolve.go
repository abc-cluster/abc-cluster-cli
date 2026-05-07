package accounting

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	cfgpkg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// Acceptable keys for the accounting block.
const (
	KeyCurrency                       = "currency"
	KeyCostCpuHour                    = "cost.cpu_hour"
	KeyCostGpuHour                    = "cost.gpu_hour"
	KeyCostMemoryGbHour               = "cost.memory_gb_hour"
	KeyCostStorageScratchGbHour       = "cost.storage_scratch_gb_hour"
	KeyCostStoragePersistentGbMonth   = "cost.storage_persistent_gb_month"
	KeyCostStorageEgressGb            = "cost.storage_egress_gb"
)

// Acceptable keys for the emissions block.
const (
	KeyGridFactor                = "grid_factor_gco2_per_kwh"
	KeyCpuW                      = "cpu_w"
	KeyGpuW                      = "gpu_w"
	KeyMemoryGbW                 = "memory_gb_w"
	KeyPue                       = "pue"
	KeyStorageScratchWPerTb      = "storage_scratch_w_per_tb"
	KeyStoragePersistentWPerTb   = "storage_persistent_w_per_tb"
	KeyStorageEcAmplification    = "storage_ec_amplification"
)

// AccountingKeys lists supported accounting keys (set/show/unset).
var AccountingKeys = []string{
	KeyCurrency, KeyCostCpuHour, KeyCostGpuHour, KeyCostMemoryGbHour,
	KeyCostStorageScratchGbHour, KeyCostStoragePersistentGbMonth, KeyCostStorageEgressGb,
}

// EmissionsKeys lists supported emissions keys.
var EmissionsKeys = []string{
	KeyGridFactor, KeyCpuW, KeyGpuW, KeyMemoryGbW, KeyPue,
	KeyStorageScratchWPerTb, KeyStoragePersistentWPerTb, KeyStorageEcAmplification,
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// ValidateAccountingValue checks the (key, raw value string) pair for the
// accounting block. Returns the parsed value (string for currency, float
// otherwise; the caller decides which to use) and any error.
func ValidateAccountingValue(key, raw string) (string, float64, error) {
	switch key {
	case KeyCurrency:
		if !currencyRe.MatchString(raw) {
			return "", 0, fmt.Errorf("%s: %q is not an ISO-4217 alpha currency code (want ^[A-Z]{3}$)", key, raw)
		}
		return raw, 0, nil
	case KeyCostCpuHour, KeyCostGpuHour, KeyCostMemoryGbHour,
		KeyCostStorageScratchGbHour, KeyCostStoragePersistentGbMonth, KeyCostStorageEgressGb:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return "", 0, fmt.Errorf("%s: cannot parse %q as a number: %w", key, raw, err)
		}
		if v < 0 {
			return "", 0, fmt.Errorf("%s: must be ≥ 0 (got %v)", key, v)
		}
		return "", v, nil
	default:
		return "", 0, fmt.Errorf("unknown accounting key %q (allowed: %v)", key, AccountingKeys)
	}
}

// ValidateEmissionsValue checks the (key, raw value string) pair for the
// emissions block.
func ValidateEmissionsValue(key, raw string) (float64, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: cannot parse %q as a number: %w", key, raw, err)
	}
	switch key {
	case KeyGridFactor:
		if v < 0 || v > 2000 {
			return 0, fmt.Errorf("%s: must be in [0, 2000] g CO2e/kWh (got %v)", key, v)
		}
	case KeyCpuW, KeyGpuW, KeyMemoryGbW,
		KeyStorageScratchWPerTb, KeyStoragePersistentWPerTb:
		if v < 0 {
			return 0, fmt.Errorf("%s: must be ≥ 0 (got %v)", key, v)
		}
	case KeyPue:
		if v < 1.0 || v > 3.0 {
			return 0, fmt.Errorf("%s: must be in [1.0, 3.0] (got %v)", key, v)
		}
	case KeyStorageEcAmplification:
		if v < 1.0 || v > 5.0 {
			return 0, fmt.Errorf("%s: must be in [1.0, 5.0] (got %v) — 1.0 = no overhead, ~6.0 caps far-fetched 5x replication", key, v)
		}
	default:
		return 0, fmt.Errorf("unknown emissions key %q (allowed: %v)", key, EmissionsKeys)
	}
	return v, nil
}

// LayeredOverrides describes the per-context Layer 1 values, used by
// Resolve to overlay onto Layer 0 (ZADefaults).
type LayeredOverrides struct {
	// Mtime of the underlying config file when these were read, used as
	// UpdatedAt for any field overridden at this layer.
	Mtime time.Time
	// Accounting overrides keyed by the strings in AccountingKeys.
	Accounting map[string]string
	// Emissions overrides keyed by the strings in EmissionsKeys.
	Emissions map[string]string
}

// FlagOverrides describes Layer 2 per-invocation overrides keyed by the
// constants above. Values are raw strings (validated against the same
// rules as Layer 1).
type FlagOverrides struct {
	Accounting map[string]string
	Emissions  map[string]string
}

// Resolve applies the override chain Layer 0 → Layer 1 → Layer 2 and
// returns the final RateCard. Validation errors are returned at the layer
// they occur in.
func Resolve(z RateCard, layer1 LayeredOverrides, layer2 FlagOverrides) (RateCard, error) {
	out := z
	now := time.Now()

	apply := func(card *RateCard, src Source, ts time.Time, accounting, emissions map[string]string) error {
		// Accounting block.
		for k, raw := range accounting {
			s, fv, err := ValidateAccountingValue(k, raw)
			if err != nil {
				return err
			}
			switch k {
			case KeyCurrency:
				card.Currency = RateString{Value: s, Source: src, UpdatedAt: ts}
			case KeyCostCpuHour:
				card.Cost.CpuHour = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyCostGpuHour:
				card.Cost.GpuHour = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyCostMemoryGbHour:
				card.Cost.MemoryGbHour = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyCostStorageScratchGbHour:
				card.Cost.StorageScratchGbHour = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyCostStoragePersistentGbMonth:
				card.Cost.StoragePersistentGbMonth = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyCostStorageEgressGb:
				card.Cost.StorageEgressGb = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			}
		}
		// Emissions block.
		for k, raw := range emissions {
			fv, err := ValidateEmissionsValue(k, raw)
			if err != nil {
				return err
			}
			switch k {
			case KeyGridFactor:
				card.Emissions.GridFactorGco2PerKwh = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyCpuW:
				card.Emissions.CpuW = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyGpuW:
				card.Emissions.GpuW = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyMemoryGbW:
				card.Emissions.MemoryGbW = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyPue:
				card.Emissions.Pue = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyStorageScratchWPerTb:
				card.Emissions.StorageScratchWPerTb = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyStoragePersistentWPerTb:
				card.Emissions.StoragePersistentWPerTb = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			case KeyStorageEcAmplification:
				card.Emissions.StorageEcAmplification = RateValue{Value: fv, Source: src, UpdatedAt: ts}
			}
		}
		return nil
	}

	// Layer 1 — config.
	if err := apply(&out, SourceConfig, layer1.Mtime, layer1.Accounting, layer1.Emissions); err != nil {
		return out, err
	}
	// Layer 2 — flags.
	if err := apply(&out, SourceFlag, now, layer2.Accounting, layer2.Emissions); err != nil {
		return out, err
	}
	return out, nil
}

// LoadLayer1 reads the per-context accounting/emissions blocks from
// ~/.abc/config.yaml. If the active context has no blocks set, returns
// empty maps and a zero Mtime. The Mtime is taken from the config file
// itself.
//
// Storage uses the lightweight raw YAML scan (the canonical Config struct
// in internal/config does not currently model accounting/emissions; rather
// than expand that surface, we read the underlying YAML directly and merge
// at write time).
func LoadLayer1(contextName string) (LayeredOverrides, error) {
	out := LayeredOverrides{
		Accounting: map[string]string{},
		Emissions:  map[string]string{},
	}
	path := cfgpkg.DefaultConfigPath()
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	out.Mtime = st.ModTime()
	data, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	acct, em, err := readContextBlocks(data, contextName)
	if err != nil {
		return out, err
	}
	out.Accounting = acct
	out.Emissions = em
	return out, nil
}
