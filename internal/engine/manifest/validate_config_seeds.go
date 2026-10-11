package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"math"
	"math/big"
	"slices"
	"strings"
)

// Decoding through float64 would round large integers and fractional numbers near them.
func decodeIntegerSeeds(m *Manifest, raw []byte) error {
	if len(m.TenantConfigSeeds) == 0 {
		return nil
	}

	var values struct {
		Seeds map[string]jsontext.Value `json:"tenant_config_seeds"`
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("decode config seed values: %w", err)
	}

	for _, entry := range m.ConfigSchema {
		key := m.Name + "." + entry.Key
		value, seeded := values.Seeds[key]
		if !seeded {
			continue
		}

		switch entry.Type {
		case "integer":
			n, err := seedInteger(value)
			if err != nil {
				return fmt.Errorf("tenant_config_seeds %q: %w", key, err)
			}
			m.TenantConfigSeeds[key] = n
		case "integer[]":
			var rawItems []jsontext.Value
			if err := json.Unmarshal(value, &rawItems); err != nil || string(value) == "null" {
				return fmt.Errorf("tenant_config_seeds %q: value must match config type integer[]", key)
			}

			items := make([]any, 0, len(rawItems))
			for index, rawItem := range rawItems {
				n, err := seedInteger(rawItem)
				if err != nil {
					return fmt.Errorf("tenant_config_seeds %q: element %d: %w", key, index, err)
				}
				items = append(items, n)
			}
			m.TenantConfigSeeds[key] = items
		}
	}

	return nil
}

func seedInteger(value jsontext.Value) (int64, error) {
	n, ok := new(big.Rat).SetString(string(value))
	if !ok || !n.IsInt() || !n.Num().IsInt64() {
		return 0, fmt.Errorf("value %s must be a signed 64-bit integer", value)
	}

	return n.Num().Int64(), nil
}

func validateConfigSeeds(m Manifest) error {
	entries := make(map[string]ConfigEntry, len(m.ConfigSchema))
	for _, entry := range m.ConfigSchema {
		entries[entry.Key] = entry
	}

	var violations []string
	for _, key := range slices.Sorted(maps.Keys(m.TenantConfigSeeds)) {
		owner, shortKey, qualified := strings.Cut(key, ".")
		if !qualified || owner != m.Name || shortKey == "" || strings.Contains(shortKey, ".") {
			violations = append(violations, fmt.Sprintf("tenant_config_seeds %q: key must be {%s}.{config_key}", key, m.Name))
			continue
		}

		entry, declared := entries[shortKey]
		if !declared {
			violations = append(violations, fmt.Sprintf("tenant_config_seeds %q: key is not declared in config_schema", key))
			continue
		}

		if err := validateSeedType(entry.Type, m.TenantConfigSeeds[key]); err != nil {
			violations = append(violations, fmt.Sprintf("tenant_config_seeds %q: %v", key, err))
		}
	}

	if len(violations) > 0 {
		return fmt.Errorf("%s", strings.Join(violations, "; "))
	}

	return nil
}

func validateSeedType(typ string, value any) error {
	switch typ {
	case "json":
		return nil
	case "string":
		if _, ok := value.(string); ok {
			return nil
		}
	case "boolean":
		if _, ok := value.(bool); ok {
			return nil
		}
	case "integer":
		if _, ok := value.(int64); ok {
			return nil
		}
	case "float":
		if n, ok := value.(float64); ok && !math.IsInf(n, 0) && !math.IsNaN(n) {
			return nil
		}
	case "duration":
		if str, ok := value.(string); ok && validDuration(str) {
			return nil
		}
	case "string[]", "integer[]", "float[]":
		if items, ok := value.([]any); ok {
			for index, item := range items {
				if err := validateSeedType(strings.TrimSuffix(typ, "[]"), item); err != nil {
					return fmt.Errorf("element %d: %w", index, err)
				}
			}

			return nil
		}
	}

	return fmt.Errorf("value %v must match config type %q", value, typ)
}
