package schema

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testSchema() Schema {
	return Schema{
		SchemaVersion: "1", Audience: "is_serp_old=0", DefaultEngine: "google",
		Categories: []Category{{Key: "google", Name: "Google", Engines: []Engine{{
			Key: "google", Name: "Search", QueryField: "q", Groups: []Group{{
				Key: "parameters", Name: "Parameters", Fields: []Field{
					{Key: "q", Label: "Query", Type: "string", Control: "input", Visible: true, Required: true},
					{Key: "count", Label: "Count", Type: "number", Control: "number", Visible: true, DefaultValue: float64(0)},
					{Key: "safe", Label: "Safe", Type: "boolean", Control: "switch", Visible: true, DefaultValue: false},
				},
			}},
		}}}},
	}
}

func schemaResponse(s Schema) map[string]any {
	return map[string]any{"code": 0, "msg": "ok", "data": s}
}

func TestClientFetchValidatesSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(schemaResponse(testSchema()))
	}))
	defer srv.Close()
	s, err := NewClient(srv.URL).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.DefaultEngine != "google" || s.Categories[0].Engines[0].Groups[0].Fields[1].DefaultValue != float64(0) {
		t.Fatalf("unexpected schema: %+v", s)
	}
}

func TestClientUsesConfiguredTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := NewClient(srv.URL, 25*time.Millisecond)
	started := time.Now()
	_, err := client.Fetch(context.Background())
	if err == nil {
		t.Fatal("slow schema request did not time out")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("custom schema timeout was not applied: %s", elapsed)
	}
}

func TestClientDefaultsSchemaTimeout(t *testing.T) {
	client := NewClient("http://127.0.0.1", 0)
	if client.timeout != defaultSchemaTimeout {
		t.Fatalf("schema timeout = %s, want %s", client.timeout, defaultSchemaTimeout)
	}
}

func TestClientFetchRejectsHTTPJSONAudienceAndDuplicateKeys(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    any
		wantErr bool
	}{
		{"http", 503, schemaResponse(testSchema()), true},
		{"bad-json", 200, "{", true},
		{"audience", 200, schemaResponse(func() Schema { s := testSchema(); s.Audience = "is_serp_old=1"; return s }()), true},
		{"duplicate", 200, schemaResponse(func() Schema {
			s := testSchema()
			s.Categories[0].Engines[0].Groups[0].Fields = append(s.Categories[0].Engines[0].Groups[0].Fields, s.Categories[0].Engines[0].Groups[0].Fields[0])
			return s
		}()), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				switch v := tc.body.(type) {
				case string:
					_, _ = w.Write([]byte(v))
				default:
					_ = json.NewEncoder(w).Encode(v)
				}
			}))
			defer srv.Close()
			if _, err := NewClient(srv.URL).Fetch(context.Background()); (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestCacheHitAndFailedRefreshKeepPreviousValue(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if calls.Load() > 1 {
			http.Error(w, "down", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(schemaResponse(testSchema()))
	}))
	defer srv.Close()
	now := time.Unix(0, 0)
	c := NewCache(NewClient(srv.URL))
	c.now = func() time.Time { return now }
	got, err := c.Current(context.Background())
	if err != nil || got.DefaultEngine != "google" {
		t.Fatalf("first current = %+v, %v", got, err)
	}
	_, err = c.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("cache calls = %d", calls.Load())
	}
	now = now.Add(cacheTTL + time.Second)
	got, err = c.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultEngine != "google" || calls.Load() != 2 {
		t.Fatalf("stale current = %+v calls=%d", got, calls.Load())
	}
}

func TestCacheConcurrentRefreshSharesRequest(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(schemaResponse(testSchema()))
	}))
	defer srv.Close()
	c := NewCache(NewClient(srv.URL))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Current(context.Background()) }()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("requests = %d, want 1", calls.Load())
	}
}

func TestCacheConcurrentGetRefreshBroadcastsSuccess(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(schemaResponse(testSchema()))
	}))
	defer srv.Close()
	c := NewCache(NewClient(srv.URL))
	type result struct {
		schema Schema
		err    error
	}
	results := make(chan result, 12)
	for i := 0; i < 12; i++ {
		go func(i int) {
			if i%2 == 0 {
				s, err := c.Get(context.Background())
				results <- result{s, err}
				return
			}
			s, err := c.Refresh(context.Background())
			results <- result{s, err}
		}(i)
	}
	<-started
	close(release)
	var first result
	for i := 0; i < 12; i++ {
		r := <-results
		if i == 0 {
			first = r
		}
		if r.err != first.err || r.schema.DefaultEngine != first.schema.DefaultEngine || r.schema.DefaultEngine == "" {
			t.Fatalf("result mismatch: first=%+v got=%+v", first, r)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("requests = %d, want 1", calls.Load())
	}
}

func TestCacheConcurrentGetRefreshBroadcastsFailure(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		_, _ = w.Write([]byte("{"))
	}))
	defer srv.Close()
	c := NewCache(NewClient(srv.URL))
	type result struct {
		schema Schema
		err    error
	}
	results := make(chan result, 10)
	for i := 0; i < 10; i++ {
		go func(i int) {
			if i%2 == 0 {
				s, err := c.Get(context.Background())
				results <- result{s, err}
				return
			}
			s, err := c.Refresh(context.Background())
			results <- result{s, err}
		}(i)
	}
	<-started
	close(release)
	var first result
	for i := 0; i < 10; i++ {
		r := <-results
		if i == 0 {
			first = r
		}
		if r.err == nil || r.err.Error() != first.err.Error() || r.schema.DefaultEngine != first.schema.DefaultEngine || r.schema.DefaultEngine == "" {
			t.Fatalf("failure mismatch: first=%+v got=%+v", first, r)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("requests = %d, want 1", calls.Load())
	}
}

func TestValidateChecksCategoriesAndFlatEnginesAndMultiSelectDefaults(t *testing.T) {
	s := testSchema()
	s.Engines = []Engine{{Key: "", Name: "flat", QueryField: "q", Groups: s.Categories[0].Engines[0].Groups}}
	if err := validate(s); err == nil {
		t.Fatal("expected empty flat engine key to fail")
	}
	s = testSchema()
	f := &s.Categories[0].Engines[0].Groups[0].Fields[0]
	f.Type, f.Control, f.Options, f.DefaultValue = "array", "multi_select", []Option{{Label: "one", Value: "one"}}, []any{"missing"}
	if err := validate(s); err == nil {
		t.Fatal("expected invalid multi-select default to fail")
	}
}

func TestCacheOwnerCancellationDoesNotCancelSharedRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(schemaResponse(testSchema()))
	}))
	defer srv.Close()
	c := NewCache(NewClient(srv.URL))
	ownerCtx, cancel := context.WithCancel(context.Background())
	ownerResult := make(chan resultValue, 1)
	go func() { s, err := c.Get(ownerCtx); ownerResult <- resultValue{s, err} }()
	<-started
	cancel()
	waiterResult := make(chan resultValue, 1)
	go func() { s, err := c.Get(context.Background()); waiterResult <- resultValue{s, err} }()
	close(release)
	select {
	case got := <-waiterResult:
		if got.err != nil || got.schema.DefaultEngine == "" {
			t.Fatalf("waiter = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter blocked")
	}
	select {
	case <-ownerResult:
	case <-time.After(time.Second):
		t.Fatal("owner blocked")
	}
}

func TestCacheWaiterCancellationReturnsPromptly(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(schemaResponse(testSchema()))
	}))
	defer srv.Close()
	defer close(release)
	c := NewCache(NewClient(srv.URL))
	go func() { _, _ = c.Get(context.Background()) }()
	<-started
	waiterCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Get(waiterCtx)
	if err == nil || waiterCtx.Err() == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("waiter err=%v duration=%s", err, time.Since(start))
	}
}

type resultValue struct {
	schema Schema
	err    error
}

func TestCacheReturnsDeepCopies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(schemaResponse(testSchema()))
	}))
	defer srv.Close()
	c := NewCache(NewClient(srv.URL))
	s, err := c.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.Categories[0].Engines[0].Groups[0].Fields[0].Key = "mutated"
	s2, err := c.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s2.Categories[0].Engines[0].Groups[0].Fields[0].Key != "q" {
		t.Fatal("Current exposed cache storage")
	}
	doc, err := c.EngineDocument("google")
	if err != nil {
		t.Fatal(err)
	}
	doc["groups"].([]Group)[0].Fields[0].Key = "mutated"
	doc2, err := c.EngineDocument("google")
	if err != nil {
		t.Fatal(err)
	}
	if doc2["groups"].([]Group)[0].Fields[0].Key != "q" {
		t.Fatal("EngineDocument exposed cache storage")
	}
}

func TestValidateAllowsShowWhenReferenceToLaterField(t *testing.T) {
	s := testSchema()
	fields := s.Categories[0].Engines[0].Groups[0].Fields
	fields[0].ShowWhen = &ShowWhen{Field: "later", Operator: "equals", Values: []any{"yes"}}
	fields = append(fields, Field{Key: "later", Label: "Later", Type: "string", Control: "input", Visible: true})
	s.Categories[0].Engines[0].Groups[0].Fields = fields
	if err := validate(s); err != nil {
		t.Fatal(err)
	}
}

func TestCacheDeepCopiesNestedShowWhenAndDefaultValues(t *testing.T) {
	s := testSchema()
	nestedShow := map[string]any{"items": []any{map[string]any{"enabled": true}}}
	nestedDefault := map[string]any{"items": []any{map[string]any{"count": float64(0)}}}
	f := &s.Categories[0].Engines[0].Groups[0].Fields[0]
	f.ShowWhen = &ShowWhen{Field: "safe", Operator: "equals", Values: []any{nestedShow}}
	safe := &s.Categories[0].Engines[0].Groups[0].Fields[2]
	safe.Type, safe.Control, safe.DefaultValue = "object", "json", nestedDefault
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(schemaResponse(s)) }))
	defer srv.Close()
	c := NewCache(NewClient(srv.URL))
	got, err := c.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got.Categories[0].Engines[0].Groups[0].Fields[0].ShowWhen.Values[0].(map[string]any)["items"].([]any)[0].(map[string]any)["enabled"] = false
	got.Categories[0].Engines[0].Groups[0].Fields[2].DefaultValue.(map[string]any)["items"].([]any)[0].(map[string]any)["count"] = 9
	again, err := c.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Categories[0].Engines[0].Groups[0].Fields[0].ShowWhen.Values[0].(map[string]any)["items"].([]any)[0].(map[string]any)["enabled"] != true {
		t.Fatal("nested show_when value was mutated")
	}
	if again.Categories[0].Engines[0].Groups[0].Fields[2].DefaultValue.(map[string]any)["items"].([]any)[0].(map[string]any)["count"] != float64(0) {
		t.Fatal("nested default value was mutated")
	}
}

func TestCacheSnapshotFallbackAndDocuments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", 503) }))
	defer srv.Close()
	c := NewCache(NewClient(srv.URL))
	got, err := c.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultEngine != snapshotSchema.DefaultEngine || len(got.AllEngines()) != 34 {
		t.Fatalf("snapshot = %s engines=%d", got.DefaultEngine, len(got.AllEngines()))
	}
	list, err := c.ListDocument(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list["resource_uri"] != enginesURI {
		t.Fatalf("resource uri = %v", list["resource_uri"])
	}
	doc, err := c.EngineDocument("google")
	if err != nil {
		t.Fatal(err)
	}
	if doc["resource_uri"] != enginesURI+"/google" {
		t.Fatalf("engine uri = %v", doc["resource_uri"])
	}
}

func TestClientRejectsOversizedSchemaResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 16<<20+1))
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL).Fetch(context.Background()); err == nil {
		t.Fatal("oversized schema response accepted")
	}
}
