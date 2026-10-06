package adminconnectors

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/configvalue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// maxConfigBodyBytes bounds a PATCH /admin/config body.
const maxConfigBodyBytes = 1 << 20

// configChange is one validated write: a nil Value resets the key to its
// default by deleting its row.
type configChange struct {
	entry manifest.ConfigEntry
	value any
	data  []byte
}

// ServePatchConfig is PATCH /admin/config: a JSON object of qualified
// "{module}.{key}" names to new values, all belonging to one module. It writes
// every key or none. An encrypted value is encrypted server-side, and the
// masked "***" a read returns means "unchanged". A null value resets the key
// to its default. A generated key is never written directly.
func (h *Handler) ServePatchConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	var body map[string]any
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, maxConfigBodyBytes), &body); err != nil || len(body) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_body", "the body must be a JSON object of {module}.{key} values")
		return
	}
	snap := h.Registry.Snapshot()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}

	moduleName, changes, problems, err := h.plan(snap.Modules(), body)
	if err != nil {
		internalError(w, c, "encode config values", err)
		return
	}
	if len(problems) > 0 {
		writeErrorDetails(w, http.StatusUnprocessableEntity, "invalid_config", "one or more values were rejected", problems)
		return
	}

	if len(changes) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"module_name": moduleName, "updated": []string{}, "restart_required": []string{}})
		return
	}

	schema := tenantschema.Name(c.tenantSlug)
	rows, err := h.Config.ModuleConfigRows(ctx, schema, moduleName)
	if err != nil {
		internalError(w, c, "read module config", err)
		return
	}
	if missing := requiredCleared(changes); len(missing) > 0 {
		writeErrorDetails(w, http.StatusUnprocessableEntity, "invalid_config", "a required value cannot be cleared", missing)
		return
	}

	writes := make([]tenantconfig.ModuleConfigValue, 0, len(changes))
	updated := make([]string, 0, len(changes))
	restart := []string{}
	for _, ch := range changes {
		writes = append(writes, tenantconfig.ModuleConfigValue{Key: ch.entry.Key, Value: ch.data, Type: ch.entry.Type, Encrypted: ch.entry.Encrypted})
		updated = append(updated, ch.entry.Key)
		if ch.entry.RestartRequired {
			restart = append(restart, ch.entry.Key)
		}
	}
	if err := h.Config.SetModuleConfigMany(ctx, c.tenantID, schema, moduleName, writes, c.userID); err != nil {
		internalError(w, c, "write module config", err)
		return
	}
	for _, key := range updated {
		h.Cache.Invalidate(c.tenantID, moduleName+"."+key)
	}

	h.emitAudit(ctx, c, "module.config_changed", map[string]any{
		"module_name": moduleName,
		"changes":     auditChanges(changes, rows),
	})

	configured := true
	if m, ok := snap.Modules()[moduleName]; ok {
		if fresh, err := h.Config.ModuleConfigRows(ctx, schema, moduleName); err == nil {
			configured = Configured(m.Manifest.ConfigSchema, fresh)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"module_name": moduleName, "updated": updated, "restart_required": restart, "configured": configured,
	})
}

// plan resolves and validates every key of body against its module's
// config_schema, returning the one module they belong to and the encoded
// writes, or a message per rejected key.
func (h *Handler) plan(modules map[string]*module.LoadedModule, body map[string]any) (string, []configChange, map[string]string, error) {
	problems := map[string]string{}
	var moduleName string
	var changes []configChange

	for _, qualified := range slices.Sorted(mapsKeys(body)) {
		name, key, ok := strings.Cut(qualified, ".")
		m, loaded := modules[name]
		if !ok || !loaded || m.Status == module.StatusFailed || m.Status == module.StatusUnloaded {
			problems[qualified] = "unknown module"
			continue
		}
		if moduleName != "" && moduleName != name {
			problems[qualified] = "all keys of one request must belong to one module"
			continue
		}
		moduleName = name

		i := slices.IndexFunc(m.Manifest.ConfigSchema, func(e manifest.ConfigEntry) bool { return e.Key == key })
		if i < 0 {
			problems[qualified] = "not a declared config key"
			continue
		}
		entry := m.Manifest.ConfigSchema[i]
		value := body[qualified]

		switch {
		case entry.Generated:
			problems[qualified] = "a generated value cannot be written directly; rotate it instead"
		case value == nil:
			changes = append(changes, configChange{entry: entry})
		case entry.Encrypted && value == maskedValue:
			// Unchanged.
		default:
			if err := validateInput(entry, value); err != nil {
				problems[qualified] = err.Error()
				continue
			}
			value = normalize(entry, value)
			data, err := configvalue.Encode(entry, value, h.Keys)
			if _, invalid := errors.AsType[*configvalue.InvalidValueError](err); invalid {
				problems[qualified] = err.Error()
				continue
			}
			if err != nil {
				return "", nil, nil, fmt.Errorf("encode %s: %w", qualified, err)
			}
			changes = append(changes, configChange{entry: entry, value: value, data: data})
		}
	}
	return moduleName, changes, problems, nil
}

// normalize turns the whole-number floats a JSON body decodes to into int64 for
// integer entries, so an encrypted value's text form round-trips through Decode
// (a float64 of 1e6 would otherwise render as "1e+06").
func normalize(entry manifest.ConfigEntry, value any) any {
	switch entry.Type {
	case "integer":
		if f, ok := value.(float64); ok {
			return int64(f)
		}
	case "integer[]":
		if items, ok := value.([]any); ok {
			out := make([]any, len(items))
			for i, item := range items {
				out[i] = item
				if f, ok := item.(float64); ok {
					out[i] = int64(f)
				}
			}
			return out
		}
	}
	return value
}

func mapsKeys(m map[string]any) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// requiredCleared reports required entries a null write would leave with
// neither a stored value nor a default.
func requiredCleared(changes []configChange) map[string]string {
	problems := map[string]string{}
	for _, ch := range changes {
		if ch.data == nil && ch.entry.Required && ch.entry.Default == nil {
			problems[ch.entry.Key] = "a required value with no default cannot be cleared"
		}
	}
	return problems
}

// validateInput applies what a JSON body needs beyond configvalue's checks: a
// whole number for integer types, element types for arrays, membership in the
// declared options, and the declared regular expression.
func validateInput(entry manifest.ConfigEntry, value any) error {
	switch entry.Type {
	case "integer":
		if !isWhole(value) {
			return errors.New("must be a whole number")
		}
	case "string[]", "integer[]", "float[]":
		items, ok := value.([]any)
		if !ok {
			return errors.New("must be an array")
		}
		for _, item := range items {
			switch entry.Type {
			case "string[]":
				if _, ok := item.(string); !ok {
					return errors.New("every item must be a string")
				}
			case "integer[]":
				if !isWhole(item) {
					return errors.New("every item must be a whole number")
				}
			case "float[]":
				if _, ok := item.(float64); !ok {
					return errors.New("every item must be a number")
				}
			}
		}
	}

	if len(entry.Options) > 0 {
		items := []any{value}
		if arr, ok := value.([]any); ok {
			items = arr
		}
		for _, item := range items {
			if !hasOption(entry.Options, item) {
				return fmt.Errorf("%v is not one of the allowed options", item)
			}
		}
	}

	if entry.ValidationRegex != "" {
		if s, ok := value.(string); ok {
			re, err := regexp.Compile(entry.ValidationRegex)
			if err == nil && !re.MatchString(s) {
				return errors.New("does not match the required format")
			}
		}
	}
	return nil
}

func isWhole(v any) bool {
	f, ok := v.(float64)
	return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
}

func hasOption(options []manifest.FieldOption, v any) bool {
	text := fmt.Sprint(v)
	if f, ok := v.(float64); ok && f == math.Trunc(f) {
		text = fmt.Sprintf("%d", int64(f))
	}
	return slices.ContainsFunc(options, func(o manifest.FieldOption) bool { return o.Value == text })
}

// auditChanges describes each write for the audit record: old and new values
// for a plain key, and only that it changed for an encrypted one.
func auditChanges(changes []configChange, rows map[string]tenantconfig.ModuleConfigRow) []map[string]any {
	out := make([]map[string]any, 0, len(changes))
	for _, ch := range changes {
		entry := map[string]any{"key": ch.entry.Key, "encrypted": ch.entry.Encrypted}
		if !ch.entry.Encrypted {
			if old, ok := rows[ch.entry.Key]; ok {
				entry["old"] = jsonText(old.Value)
			}
			entry["new"] = ch.value
		}
		out = append(out, entry)
	}
	return out
}

func jsonText(raw string) any {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	return v
}

// ServeRotate is POST /admin/connectors/{name}/config/{key}/rotate: the engine
// mints a new random value for a generated string key and stores it. The new
// value is returned only for a key that is not encrypted.
func (h *Handler) ServeRotate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	params := route.ParamsFromContext(ctx)

	snap := h.Registry.Snapshot()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	m, ok := connectorModule(snap, params["name"])
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "connector is not installed")
		return
	}
	i := slices.IndexFunc(m.Manifest.ConfigSchema, func(e manifest.ConfigEntry) bool { return e.Key == params["key"] })
	if i < 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such config key")
		return
	}
	entry := m.Manifest.ConfigSchema[i]
	if !entry.Generated || entry.Type != "string" {
		writeError(w, http.StatusUnprocessableEntity, "not_rotatable", "only a generated string value can be rotated")
		return
	}

	secret, err := newSecret()
	if err != nil {
		internalError(w, c, "generate secret", err)
		return
	}
	data, err := configvalue.Encode(entry, secret, h.Keys)
	if err != nil {
		internalError(w, c, "encode rotated value", err)
		return
	}
	name := m.Manifest.Name
	schema := tenantschema.Name(c.tenantSlug)
	if err := h.Config.SetModuleConfigMany(ctx, c.tenantID, schema, name,
		[]tenantconfig.ModuleConfigValue{{Key: entry.Key, Value: data, Type: entry.Type, Encrypted: entry.Encrypted}}, c.userID); err != nil {
		internalError(w, c, "write rotated value", err)
		return
	}
	h.Cache.Invalidate(c.tenantID, name+"."+entry.Key)
	h.emitAudit(ctx, c, "module.config_rotated", map[string]any{"module_name": name, "key": entry.Key})

	result := map[string]any{"module_name": name, "key": entry.Key, "rotated_at": time.Now().UTC().Format(time.RFC3339)}
	if entry.Encrypted {
		result["value"] = maskedValue
	} else {
		result["value"] = secret
	}
	writeJSON(w, http.StatusOK, result)
}

// newSecret returns 32 random bytes, hex-encoded.
func newSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
