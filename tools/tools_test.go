package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"thordata-mcp/internal/auth"
	"thordata-mcp/internal/schema"
	"thordata-mcp/internal/serp"

	"github.com/mark3labs/mcp-go/mcp"
)

func testCache(t *testing.T) *schema.Cache {
	t.Helper()
	return schema.NewCache(nil)
}

func withSchema(t *testing.T, c *schema.Cache) {
	t.Helper()
	// The package snapshot is used by a nil cache and includes google.
	_ = c
}

func TestToolSetRegistersExpectedTools(t *testing.T) {
	toolSet := New(schema.NewCache(nil), serp.NewClient("http://127.0.0.1"), "http://history", "http://statistics")
	for _, tool := range []mcp.Tool{toolSet.ListEnginesTool(), toolSet.SearchTool(), toolSet.HistoryTool(), toolSet.StatisticsTool()} {
		if tool.Name == "" {
			t.Fatal("tool name is empty")
		}
	}
}

func TestSearchMergesTopLevelAndReturnsEnvelope(t *testing.T) {
	var got url.Values
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.Form
		_, _ = w.Write([]byte(`{"data":{"answer":42},"secret":"ignored"}`))
	}))
	defer upstream.Close()
	toolSet := New(schema.NewCache(nil), serp.NewClient(upstream.URL), "", "")
	ctx := auth.WithToken(context.Background(), "secret-token")
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "search", Arguments: map[string]any{
		"engine": "google", "q": "cats", "json": "2", "params": map[string]any{"q": "old"}, "response_mode": "complete",
	}}}
	result, err := toolSet.HandleSearch(ctx, request)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if got.Get("engine") != "google" || got.Get("q") != "cats" || got.Get("json") != "2" {
		t.Fatalf("form=%v", got)
	}
	if strings.Contains(string(mustJSON(t, result.StructuredContent)), "secret-token") {
		t.Fatal("token leaked in result")
	}
}

func TestSearchRejectsUnknownModeAndMissingToken(t *testing.T) {
	toolSet := New(schema.NewCache(nil), serp.NewClient("http://127.0.0.1"), "", "")
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "search", Arguments: map[string]any{"response_mode": "bad"}}}
	result, err := toolSet.HandleSearch(context.Background(), request)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("expected tool error, result=%v err=%v", result, err)
	}
}

func TestSearchReturnsToolErrorForNonZeroBusinessCode(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":1001,"message":"invalid query","data":null}`))
	}))
	defer upstream.Close()
	toolSet := New(schema.NewCache(nil), serp.NewClient(upstream.URL), "", "")
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "search", Arguments: map[string]any{"engine": "google"}}}
	result, err := toolSet.HandleSearch(auth.WithToken(context.Background(), "key"), request)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("expected tool error, result=%v err=%v", result, err)
	}
}

func TestHistoryAndStatisticsForwardExactParameters(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.String())
		_, _ = w.Write([]byte(`{"code":0,"data":{"list":[]}}`))
	}))
	defer srv.Close()
	toolSet := New(schema.NewCache(nil), serp.NewClient(""), srv.URL+"/history", srv.URL+"/statistics")
	ctx := auth.WithToken(context.Background(), "key")
	history := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "history", Arguments: map[string]any{
		"page": 2, "page_size": 50, "search_query": "cats", "search_engine": "google", "status": "success", "start_time": 10, "end_time": 20, "timezone": "Asia/Shanghai",
	}}}
	if result, err := toolSet.HandleHistory(ctx, history); err != nil || result == nil || result.IsError {
		t.Fatalf("history result=%v err=%v", result, err)
	}
	statistics := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "statistics", Arguments: map[string]any{
		"start_date": "2026-01-01", "end_date": "2026-01-02", "engines": []any{"google", "bing"}, "timezone": "+08:00",
	}}}
	if result, err := toolSet.HandleStatistics(ctx, statistics); err != nil || result == nil || result.IsError {
		t.Fatalf("statistics result=%v err=%v", result, err)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "page=2") || !strings.Contains(paths[0], "status=success") || !strings.Contains(paths[1], "start_date=2026-01-01") || !strings.Contains(paths[1], "engines=google%2Cbing") {
		t.Fatalf("paths=%v", paths)
	}
}

func TestHistoryAndStatisticsTimeoutSlowUpstream(t *testing.T) {
	started := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()

	toolSet := New(schema.NewCache(nil), serp.NewClient(""), srv.URL+"/history", srv.URL+"/statistics", 25*time.Millisecond)
	ctx := auth.WithToken(context.Background(), "key")
	cases := []struct {
		name   string
		handle func(context.Context) (*mcp.CallToolResult, error)
	}{
		{name: "history", handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			return toolSet.HandleHistory(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "history"}})
		}},
		{name: "statistics", handle: func(ctx context.Context) (*mcp.CallToolResult, error) {
			return toolSet.HandleStatistics(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "statistics", Arguments: map[string]any{"start_date": "2026-01-01", "end_date": "2026-01-02"}}})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			startedAt := time.Now()
			result, err := tc.handle(ctx)
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("expected timeout tool error, result=%v err=%v", result, err)
			}
			if elapsed := time.Since(startedAt); elapsed > time.Second {
				t.Fatalf("request exceeded finite timeout: %s", elapsed)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("upstream request was not started")
			}
		})
	}
}

const historyTestToken = "0123456789abcdef0123456789abcdef"

func runHistory(t *testing.T, body string) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	toolSet := New(schema.NewCache(nil), serp.NewClient(""), srv.URL+"/history", srv.URL+"/statistics")
	result, err := toolSet.HandleHistory(auth.WithToken(context.Background(), historyTestToken), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "history"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) == 0 {
		t.Fatal("empty tool result")
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content %#v", result.Content[0])
	}
	envelope := map[string]any{}
	if err := json.Unmarshal([]byte(text.Text), &envelope); err != nil {
		t.Fatalf("envelope %q: %v", text.Text, err)
	}
	return result, envelope
}

func TestExecuteGETAcceptsUpstreamCode200(t *testing.T) {
	result, envelope := runHistory(t, `{"code":200,"data":{"list":[],"count":0}}`)
	if ok, _ := envelope["ok"].(bool); result.IsError || !ok {
		t.Fatalf("upstream code 200 treated as failure: %#v", envelope)
	}
}

func TestExecuteGETReportsUpstreamFailures(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "status code", body: `{"statusCode":400,"message":"invalid date range"}`, want: "invalid date range"},
		{name: "error text payload", body: `{"data":"error, Collection failed"}`, want: "error, Collection failed"},
		{name: "error field", body: `{"error":"invalid api key"}`, want: "invalid api key"},
		{name: "business code", body: `{"code":401,"msg":"unauthorized"}`, want: "unauthorized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, envelope := runHistory(t, tc.body)
			if !result.IsError {
				t.Fatalf("upstream failure was accepted: %#v", envelope)
			}
			if got, _ := envelope["error"].(string); got != tc.want {
				t.Fatalf("error = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExecuteGETUnwrapsJSONStringPayload(t *testing.T) {
	result, envelope := runHistory(t, `{"code":0,"data":"{\"list\":[{\"id\":1}],\"count\":1}"}`)
	if result.IsError {
		t.Fatalf("double encoded payload was rejected: %#v", envelope)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok || data["count"] != float64(1) {
		t.Fatalf("data was not unwrapped: %#v", envelope["data"])
	}
}

func TestExecuteGETReportsTransportCause(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	endpoint := srv.URL + "/history"
	srv.Close()
	toolSet := New(schema.NewCache(nil), serp.NewClient(""), endpoint, endpoint)
	result, err := toolSet.HandleHistory(auth.WithToken(context.Background(), historyTestToken), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "history"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("unreachable upstream was accepted: %#v", result)
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content %#v", result.Content[0])
	}
	const prefix = "upstream request failed: "
	if !strings.HasPrefix(text.Text, prefix) || len(text.Text) <= len(prefix) {
		t.Fatalf("transport cause missing: %q", text.Text)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
