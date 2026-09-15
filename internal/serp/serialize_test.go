package serp

import (
	"encoding/json"
	"net/url"
	"testing"

	"thordata-mcp/internal/schema"
)

func testEngine() schema.Engine {
	return schema.Engine{Key: "google_flights", QueryField: "q", Groups: []schema.Group{{Fields: []schema.Field{
		{Key: "q", Type: "string", Control: "input"}, {Key: "date_range", Type: "date_range", Control: "input"}, {Key: "tags", Type: "tags", Control: "input"}, {Key: "switch", Type: "switch", Control: "switch"}, {Key: "number", Type: "number", Control: "number"}, {Key: "obj", Type: "object", Control: "json"}, {Key: "cr", Type: "tags", Control: "input"}, {Key: "time_range", Type: "time_range", Control: "input"}, {Key: "cascader", Type: "cascader", Control: "input"}, {Key: "departure_id", Type: "string", Control: "input"}, {Key: "arrival_id", Type: "string", Control: "input"}, {Key: "engine", Type: "string", Control: "input"},
	}}}}
}

func TestSerializeIsSchemaDrivenAndOmitEmptyUnsupported(t *testing.T) {
	params := map[string]any{"q": "hello", "date_range": map[string]any{"start": "2026-01-01", "end": "2026-01-03"}, "tags": []any{"a", "b"}, "switch": false, "number": 12.5, "obj": map[string]any{"x": 1}, "unknown": "omit", "empty": "", "nil": nil, "engine": "attacker"}
	got, err := Serialize(testEngine(), params)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("q") != "hello" || got.Get("date_range_start") != "2026-01-01" || got.Get("date_range_end") != "2026-01-03" {
		t.Fatalf("values %v", got)
	}
	if got.Get("switch") != "false" || got.Get("number") != "12.5" || got.Get("obj") != `{"x":1}` {
		t.Fatalf("types %v", got)
	}
	if got.Get("engine") != "google_flights" || got.Get("unknown") != "" || got.Get("empty") != "" {
		t.Fatalf("omit/engine %v", got)
	}
	if got.Get("tags") == "" {
		t.Fatalf("tags omitted: %v", got)
	}
}

func TestSerializeThorSpecialContracts(t *testing.T) {
	params := map[string]any{"cr": []any{"us", "GB"}, "time_range": []any{"9", "0", "17", "0"}, "cascader": []any{"one", "two"}, "departure_id": "jfk"}
	got, err := Serialize(testEngine(), params)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("cr") != "countryUS|countryGB" || got.Get("time_range") != "0900,1700" || got.Get("departure_id") != "JFK" {
		t.Fatalf("special values %v", got)
	}
	if got.Get("cascader") == "" {
		t.Fatalf("cascader omitted: %v", got)
	}
}

func TestSerializeCRNormalizesCountryNames(t *testing.T) {
	params := map[string]any{"cr": []any{"United States", "USA", "GB"}}
	got, err := Serialize(testEngine(), params)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("cr") != "countryUS|countryUS|countryGB" {
		t.Fatalf("cr = %q", got.Get("cr"))
	}
}

func TestSerializeRejectsMalformedSpecialValues(t *testing.T) {
	cases := []map[string]any{
		{"time_range": []any{"1", "2"}},
		{"cascader": "one"},
		{"cascader": []any{}},
		{"tags": []any{"ok", nil}},
		{"tags": []int{1, 2}},
		{"number": "12"},
		{"date_range": map[string]any{"start": "2026-01-01"}},
	}
	for _, params := range cases {
		if _, err := Serialize(testEngine(), params); err == nil {
			t.Errorf("accepted malformed params %#v", params)
		}
	}
}

func TestSerializeAcceptsSchemaAndPreservesZeroFalse(t *testing.T) {
	e := testEngine()
	s := schema.Schema{DefaultEngine: e.Key, Categories: []schema.Category{{Engines: []schema.Engine{e}}}}
	got, err := Serialize(&s, map[string]any{"switch": false, "number": 0})
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("switch") != "false" || got.Get("number") != "0" {
		t.Fatalf("%v", got)
	}
	var _ url.Values = got
	_ = json.Valid
}
