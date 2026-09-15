package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const (
	enginesURI = "thordata://engines"
)

type Schema struct {
	SchemaVersion string     `json:"schema_version"`
	Audience      string     `json:"audience"`
	DefaultEngine string     `json:"default_engine"`
	Categories    []Category `json:"categories"`
	Engines       []Engine   `json:"engines,omitempty"`
}

type Category struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Engines []Engine `json:"engines"`
}

type Engine struct {
	Key        string  `json:"key"`
	Name       string  `json:"name"`
	QueryField string  `json:"query_field"`
	Groups     []Group `json:"groups"`
}

type Group struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

type Field struct {
	Key          string    `json:"key"`
	Name         string    `json:"name,omitempty"`
	Label        string    `json:"label,omitempty"`
	Type         string    `json:"type"`
	Control      string    `json:"control"`
	Visible      bool      `json:"visible"`
	Required     bool      `json:"required"`
	DefaultValue any       `json:"default_value"`
	Options      []Option  `json:"options,omitempty"`
	ShowWhen     *ShowWhen `json:"show_when,omitempty"`
}

type Option struct {
	Label string `json:"label"`
	Value any    `json:"value"`
}

type ShowWhen struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Values   []any  `json:"values"`
}

func (s Schema) AllEngines() []Engine {
	var result []Engine
	for _, category := range s.Categories {
		result = append(result, category.Engines...)
	}
	result = append(result, s.Engines...)
	return result
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = cloneAny(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = cloneAny(v)
		}
		return out
	default:
		return v
	}
}
func cloneSchema(s Schema) Schema {
	out := s
	out.Categories = make([]Category, len(s.Categories))
	for i, c := range s.Categories {
		out.Categories[i] = c
		out.Categories[i].Engines = make([]Engine, len(c.Engines))
		for j, e := range c.Engines {
			out.Categories[i].Engines[j] = e
			out.Categories[i].Engines[j].Groups = cloneGroups(e.Groups)
		}
	}
	out.Engines = make([]Engine, len(s.Engines))
	for i, e := range s.Engines {
		out.Engines[i] = e
		out.Engines[i].Groups = cloneGroups(e.Groups)
	}
	return out
}
func cloneGroups(gs []Group) []Group {
	out := make([]Group, len(gs))
	for i, g := range gs {
		out[i] = g
		out[i].Fields = make([]Field, len(g.Fields))
		for j, f := range g.Fields {
			out[i].Fields[j] = f
			out[i].Fields[j].DefaultValue = cloneAny(f.DefaultValue)
			out[i].Fields[j].Options = append([]Option(nil), f.Options...)
			if f.ShowWhen != nil {
				sw := *f.ShowWhen
				sw.Values = make([]any, len(f.ShowWhen.Values))
				for k, value := range f.ShowWhen.Values {
					sw.Values[k] = cloneAny(value)
				}
				out[i].Fields[j].ShowWhen = &sw
			}
		}
	}
	return out
}

func validate(s Schema) error {
	if strings.TrimSpace(s.SchemaVersion) == "" {
		return fmt.Errorf("schema_version cannot be empty")
	}
	if s.Audience != "is_serp_old=0" {
		return fmt.Errorf("unsupported audience %q", s.Audience)
	}
	if strings.TrimSpace(s.DefaultEngine) == "" {
		return fmt.Errorf("default_engine cannot be empty")
	}
	engines := s.AllEngines()
	seenEngines := make(map[string]bool, len(engines))
	seenCategories := make(map[string]bool, len(s.Categories))
	for ci, category := range s.Categories {
		if strings.TrimSpace(category.Key) == "" {
			return fmt.Errorf("category %d key cannot be empty", ci)
		}
		if seenCategories[category.Key] {
			return fmt.Errorf("duplicate category key %q", category.Key)
		}
		seenCategories[category.Key] = true
	}
	for _, engine := range engines {
		if strings.TrimSpace(engine.Key) == "" {
			return fmt.Errorf("engine key cannot be empty")
		}
		if seenEngines[engine.Key] {
			return fmt.Errorf("duplicate engine key %q", engine.Key)
		}
		seenEngines[engine.Key] = true
		if strings.TrimSpace(engine.Name) == "" || strings.TrimSpace(engine.QueryField) == "" {
			return fmt.Errorf("engine %q has missing name or query_field", engine.Key)
		}
		fieldKeys := make(map[string]bool)
		groupKeys := make(map[string]bool)
		for _, group := range engine.Groups {
			if strings.TrimSpace(group.Key) == "" {
				return fmt.Errorf("engine %q group key cannot be empty", engine.Key)
			}
			if groupKeys[group.Key] {
				return fmt.Errorf("engine %q duplicate group key %q", engine.Key, group.Key)
			}
			groupKeys[group.Key] = true
			for _, field := range group.Fields {
				if strings.TrimSpace(field.Key) == "" {
					return fmt.Errorf("engine %q field key cannot be empty", engine.Key)
				}
				if fieldKeys[field.Key] {
					return fmt.Errorf("engine %q duplicate field key %q", engine.Key, field.Key)
				}
				fieldKeys[field.Key] = true
			}
		}
		for _, group := range engine.Groups {
			for _, field := range group.Fields {
				if err := validateField(engine.Key, field, fieldKeys); err != nil {
					return err
				}
			}
		}
		if !fieldKeys[engine.QueryField] {
			return fmt.Errorf("engine %q query_field %q not found", engine.Key, engine.QueryField)
		}
	}
	if !seenEngines[s.DefaultEngine] {
		return fmt.Errorf("default_engine %q does not reference an engine", s.DefaultEngine)
	}
	return nil
}

func validateField(engineKey string, field Field, fieldKeys map[string]bool) error {
	expected := map[string]string{"input": "string", "number": "number", "select": "options", "multi_select": "array", "switch": "boolean", "json": "object"}
	if want, ok := expected[field.Control]; !ok {
		return fmt.Errorf("engine %q field %q unsupported control %q", engineKey, field.Key, field.Control)
	} else if field.Type != want {
		return fmt.Errorf("engine %q field %q control %q requires type %q", engineKey, field.Key, field.Control, want)
	}
	optionControl := field.Control == "select" || field.Control == "multi_select"
	if optionControl && len(field.Options) == 0 {
		return fmt.Errorf("engine %q field %q requires options", engineKey, field.Key)
	}
	if !optionControl && len(field.Options) > 0 {
		return fmt.Errorf("engine %q field %q cannot have options", engineKey, field.Key)
	}
	seen := map[string]bool{}
	for _, option := range field.Options {
		if strings.TrimSpace(option.Label) == "" {
			return fmt.Errorf("engine %q field %q option label cannot be empty", engineKey, field.Key)
		}
		key, err := valueKey(option.Value)
		if err != nil {
			return fmt.Errorf("engine %q field %q: %w", engineKey, field.Key, err)
		}
		if seen[key] {
			return fmt.Errorf("engine %q field %q duplicate option value", engineKey, field.Key)
		}
		seen[key] = true
	}
	if field.DefaultValue != nil {
		if err := validateDefault(field, seen); err != nil {
			return fmt.Errorf("engine %q field %q: %w", engineKey, field.Key, err)
		}
	}
	if field.ShowWhen != nil {
		if !fieldKeys[field.ShowWhen.Field] {
			return fmt.Errorf("engine %q field %q show_when references unknown field %q", engineKey, field.Key, field.ShowWhen.Field)
		}
		switch field.ShowWhen.Operator {
		case "equals", "not_equals", "in", "not_in":
		default:
			return fmt.Errorf("engine %q field %q show_when has unsupported operator %q", engineKey, field.Key, field.ShowWhen.Operator)
		}
		if len(field.ShowWhen.Values) == 0 {
			return fmt.Errorf("engine %q field %q show_when values cannot be empty", engineKey, field.Key)
		}
	}
	return nil
}

func validateDefault(field Field, optionKeys map[string]bool) error {
	v := reflect.ValueOf(field.DefaultValue)
	switch field.Type {
	case "string":
		if v.Kind() != reflect.String {
			return fmt.Errorf("default_value does not match type %q", field.Type)
		}
	case "number":
		switch v.Kind() {
		case reflect.String:
			if _, ok := field.DefaultValue.(json.Number); !ok {
				return fmt.Errorf("default_value does not match type %q", field.Type)
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		default:
			return fmt.Errorf("default_value does not match type %q", field.Type)
		}
	case "boolean":
		if v.Kind() != reflect.Bool {
			return fmt.Errorf("default_value does not match type %q", field.Type)
		}
	case "array":
		if v.Kind() != reflect.Array && v.Kind() != reflect.Slice {
			return fmt.Errorf("default_value does not match type %q", field.Type)
		}
		if field.Control == "multi_select" && len(optionKeys) > 0 {
			for i := 0; i < v.Len(); i++ {
				key, err := valueKey(v.Index(i).Interface())
				if err != nil || !optionKeys[key] {
					return fmt.Errorf("default_value contains a value not present in options")
				}
			}
		}
	case "object":
		if v.Kind() != reflect.Map && v.Kind() != reflect.Struct {
			return fmt.Errorf("default_value does not match type %q", field.Type)
		}
	case "options":
		key, err := valueKey(field.DefaultValue)
		if err != nil {
			return fmt.Errorf("default_value is invalid: %w", err)
		}
		if !optionKeys[key] {
			return fmt.Errorf("default_value is not one of options")
		}
	}
	return nil
}

func valueKey(v any) (string, error) {
	if v == nil {
		return "", fmt.Errorf("option value must be a string, number, or boolean")
	}
	if n, ok := v.(json.Number); ok {
		f, err := strconv.ParseFloat(string(n), 64)
		if err != nil {
			return "", fmt.Errorf("option value is not a valid number")
		}
		return "n:" + strconv.FormatFloat(f, 'g', -1, 64), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return "s:" + rv.String(), nil
	case reflect.Bool:
		return "b:" + strconv.FormatBool(rv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "n:" + strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "n:" + strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return "n:" + strconv.FormatFloat(rv.Float(), 'g', -1, 64), nil
	}
	return "", fmt.Errorf("option value must be a string, number, or boolean")
}
