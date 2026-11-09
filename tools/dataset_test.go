package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"thordata-mcp/internal/auth"
	"thordata-mcp/internal/dataset"
	"thordata-mcp/internal/schema"
	"thordata-mcp/internal/serp"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestFetchDatasetUsesDatasetKeyNotSerpToken(t *testing.T) {
	var gotKey, gotIdem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		gotIdem = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","request_id":"req_1","data":{"order_id":"ord_1","status":"succeeded","delivered_records":8,"billing":{"amount":"0.008"}}}`))
	}))
	defer srv.Close()
	toolSet := New(schema.NewCache(nil), serp.NewClient(""), "", "")
	toolSet.SetDatasetClient(dataset.NewClient(srv.URL, 0))
	ctx := auth.WithToken(auth.WithDatasetKey(context.Background(), "ac-key"), "serp-token")
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "fetch_dataset", Arguments: map[string]any{
		"dataset_key": "ds", "records": 8, "idempotency_key": "idem-1",
	}}}
	result, err := toolSet.HandleFetchDataset(ctx, request)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if gotKey != "ac-key" || gotIdem != "idem-1" {
		t.Fatalf("key=%q idem=%q", gotKey, gotIdem)
	}
	if strings.Contains(string(mustJSON(t, result.StructuredContent)), "serp-token") {
		t.Fatal("SERP token leaked into dataset result")
	}
}

func TestFetchDatasetSecondPageForwardsOrderAndCursor(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"order_id":"ord_1","page_no":2,"status":"completed","delivered_records":5,"next_cursor":""}}`))
	}))
	defer srv.Close()
	toolSet := New(schema.NewCache(nil), serp.NewClient(""), "", "")
	toolSet.SetDatasetClient(dataset.NewClient(srv.URL, 0))
	ctx := auth.WithDatasetKey(context.Background(), "ac-key")
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "fetch_dataset", Arguments: map[string]any{
		"order_id": "ord_1", "cursor": "c1", "page_no": 2, "records": 10,
	}}}
	result, err := toolSet.HandleFetchDataset(ctx, request)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if body["order_id"] != "ord_1" || body["cursor"] != "c1" || body["page_no"] != float64(2) {
		t.Fatalf("body = %#v", body)
	}
	if _, ok := body["dataset_key"]; ok {
		t.Fatalf("later page should not resend dataset_key: %#v", body)
	}
}

func TestFetchDatasetRequiresIdempotencyKeyOnFirstPage(t *testing.T) {
	toolSet := New(schema.NewCache(nil), serp.NewClient(""), "", "")
	toolSet.SetDatasetClient(dataset.NewClient("http://127.0.0.1:1", 0))
	ctx := auth.WithDatasetKey(context.Background(), "ac-key")
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "fetch_dataset", Arguments: map[string]any{
		"dataset_key": "ds", "records": 8,
	}}}
	result, err := toolSet.HandleFetchDataset(ctx, request)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("expected idempotency_key_required, result=%v err=%v", result, err)
	}
}

func TestDatasetIntArgStrictRejectsInvalidValue(t *testing.T) {
	if _, ok := datasetIntArgStrict("abc"); ok {
		t.Fatal("string abc must not fall back to a valid integer")
	}
	if n, ok := datasetIntArgStrict(float64(8)); !ok || n != 8 {
		t.Fatalf("float 8 rejected: n=%d ok=%v", n, ok)
	}
	if _, ok := datasetIntArgStrict(float64(8.5)); ok {
		t.Fatal("fractional records must be rejected")
	}
}

func TestFetchDatasetRequiresDatasetKey(t *testing.T) {
	toolSet := New(schema.NewCache(nil), serp.NewClient(""), "", "")
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "fetch_dataset", Arguments: map[string]any{
		"dataset_key": "ds", "records": 8,
	}}}
	result, err := toolSet.HandleFetchDataset(auth.WithToken(context.Background(), "serp-only"), request)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("expected dataset_credential_required, result=%v err=%v", result, err)
	}
}
