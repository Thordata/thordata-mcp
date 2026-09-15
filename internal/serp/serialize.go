package serp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"thordata-mcp/internal/schema"
)

// Serialize converts user parameters according to the selected Thor schema engine.
func Serialize(source any, params map[string]any) (url.Values, error) {
	engine, err := resolveEngine(source, params)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]schema.Field)
	for _, group := range engine.Groups {
		for _, field := range group.Fields {
			fields[field.Key] = field
		}
	}
	result := make(url.Values)
	for key, value := range params {
		if key == "engine" || value == nil {
			continue
		}
		field, ok := fields[key]
		if !ok {
			continue
		}
		if !fieldPresent(value) {
			if field.Type != "date_range" && field.Type != "time_range" && field.Type != "cascader" {
				continue
			}
		}
		if err := addField(result, field, value, engine.Key); err != nil {
			return nil, fmt.Errorf("serialize %q: %w", key, err)
		}
	}
	// The engine is always authoritative and emitted last.
	result.Set("engine", engine.Key)
	return result, nil
}

func resolveEngine(source any, params map[string]any) (schema.Engine, error) {
	switch x := source.(type) {
	case schema.Engine:
		return x, nil
	case *schema.Engine:
		if x != nil {
			return *x, nil
		}
	case schema.Schema:
		return findSchemaEngine(x, params)
	case *schema.Schema:
		if x != nil {
			return findSchemaEngine(*x, params)
		}
	}
	return schema.Engine{}, fmt.Errorf("unsupported schema source %T", source)
}

func findSchemaEngine(s schema.Schema, params map[string]any) (schema.Engine, error) {
	want := s.DefaultEngine
	if raw, ok := params["engine"].(string); ok && raw != "" {
		want = raw
	}
	for _, e := range s.AllEngines() {
		if e.Key == want {
			return e, nil
		}
	}
	return schema.Engine{}, fmt.Errorf("unknown engine %q", want)
}

func fieldPresent(v any) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}
	switch rv.Kind() {
	case reflect.String:
		return rv.String() != ""
	case reflect.Array, reflect.Slice, reflect.Map:
		return rv.Len() > 0
	}
	return true
}

func addField(out url.Values, field schema.Field, value any, engineKey string) error {
	key := field.Key
	switch field.Type {
	case "boolean", "switch":
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("want boolean")
		}
		out.Set(key, strconv.FormatBool(b))
	case "number":
		if !isNumber(value) {
			return fmt.Errorf("want number")
		}
		out.Set(key, scalarString(value))
	case "date_range":
		m := mapValue(value)
		if m == nil {
			return fmt.Errorf("want object with start/end")
		}
		start, sok := m["start"]
		end, eok := m["end"]
		if !sok || !eok || !isScalar(start) || !isScalar(end) || !fieldPresent(start) || !fieldPresent(end) {
			return fmt.Errorf("date range requires scalar start and end")
		}
		out.Set(key+"_start", scalarString(start))
		out.Set(key+"_end", scalarString(end))
	case "time_range":
		parts, err := scalarList(value)
		if err != nil || len(parts) != 4 {
			return fmt.Errorf("time range requires 4 numeric components")
		}
		for _, p := range parts {
			if _, err := strconv.Atoi(p); err != nil {
				return fmt.Errorf("time range components must be numeric")
			}
		}
		out.Set(key, fmt.Sprintf("%s%s,%s%s", zeroPad(parts[0]), zeroPad(parts[1]), zeroPad(parts[2]), zeroPad(parts[3])))
	case "cascader":
		parts, err := scalarList(value)
		if err != nil || len(parts) == 0 {
			return fmt.Errorf("cascader requires non-empty array")
		}
		out.Set(key, parts[len(parts)-1])
	case "tags":
		parts, err := scalarList(value)
		if err != nil {
			return fmt.Errorf("tags requires scalar array")
		}
		if key == "cr" {
			for i := range parts {
				p := parts[i]
				if strings.HasPrefix(strings.ToLower(p), "country") {
					p = strings.TrimSpace(p[len("country"):])
				}
				parts[i] = "country" + normalizeCountry(p)
			}
			out.Set(key, strings.Join(parts, "|"))
		} else {
			out.Set(key, strings.Join(parts, ","))
		}
	case "object":
		if key == "date_range" || key == "time_range" {
			special := mapValue(value)
			if special == nil {
				return fmt.Errorf("want object")
			}
			for sub, item := range special {
				if fieldPresent(item) {
					out.Set(key+"."+sub, fmt.Sprint(item))
				}
			}
			return nil
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out.Set(key, string(data))
	case "array":
		parts, err := scalarList(value)
		if err != nil {
			return fmt.Errorf("want scalar array")
		}
		out.Set(key, strings.Join(parts, ","))
	case "options", "string":
		if !isScalar(value) {
			return fmt.Errorf("want scalar")
		}
		text := scalarString(value)
		if engineKey == "google_flights" && (field.Key == "departure_id" || field.Key == "arrival_id") {
			text = normalizeAirport(text)
		}
		out.Set(key, text)
	default:
		return fmt.Errorf("unsupported field type %q", field.Type)
	}
	return nil
}

func mapValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func normalizeCountry(value string) string {
	known := map[string]string{"united states": "US", "usa": "US", "us": "US", "united kingdom": "GB", "uk": "GB", "canada": "CA", "australia": "AU", "china": "CN"}
	if code, ok := known[strings.ToLower(strings.TrimSpace(value))]; ok {
		return code
	}
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeAirport(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }

func isScalar(v any) bool {
	if v == nil {
		return false
	}
	if _, ok := v.(json.Number); ok {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func isNumber(v any) bool {
	if _, ok := v.(json.Number); ok {
		return true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func scalarString(v any) string {
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	return fmt.Sprint(v)
}

func scalarList(value any) ([]string, error) {
	switch x := value.(type) {
	case []string:
		return append([]string(nil), x...), nil
	case []any:
		out := make([]string, len(x))
		for i, item := range x {
			if !isScalar(item) {
				return nil, fmt.Errorf("array element must be scalar")
			}
			out[i] = scalarString(item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("want []string or []any")
	}
}

func listStrings(value any) []string {
	switch x := value.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, len(x))
		for i := range x {
			out[i] = fmt.Sprint(x[i])
		}
		return out
	case string:
		if x == "" {
			return nil
		}
		return strings.Split(x, ",")
	default:
		if value == nil {
			return nil
		}
		return []string{fmt.Sprint(value)}
	}
}

func zeroPad(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		return v[len(v)-2:]
	}
	return "0" + v
}
