package dataset

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientFetchSendsDatasetCredentialAndIdempotency(t *testing.T) {
	var gotKey, gotIdem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fetch" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotKey = r.Header.Get("X-API-Key")
		gotIdem = r.Header.Get("Idempotency-Key")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["dataset_key"] != "ds" || body["records"] != float64(10) {
			t.Errorf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","request_id":"req_1","data":{"order_id":"ord_1","status":"succeeded","delivered_records":10}}`))
	}))
	defer srv.Close()
	client := NewClient(srv.URL, 0)
	data, err := client.Fetch(context.Background(), "api-key", FetchInput{
		DatasetKey: "ds", Records: 10, Format: "json", Channel: "mcp", IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "api-key" || gotIdem != "idem-1" {
		t.Fatalf("key=%q idem=%q", gotKey, gotIdem)
	}
	if data["order_id"] != "ord_1" || data["request_id"] != "req_1" {
		t.Fatalf("data = %#v", data)
	}
}

func TestClientListDatasets(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/datasets" || r.Method != http.MethodGet {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		gotKey = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"datasets":[{"dataset_key":"amazon","unit_price":"1.000"}]}}`))
	}))
	defer srv.Close()
	data, err := NewClient(srv.URL, 0).ListDatasets(context.Background(), "api-key")
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "api-key" {
		t.Fatalf("key=%q", gotKey)
	}
	if _, ok := data["datasets"].([]any); !ok {
		t.Fatalf("data = %#v", data)
	}
}

func TestClientFetchForwardsPageCursor(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"order_id":"ord_1","page_no":2,"next_cursor":""}}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, 0).Fetch(context.Background(), "key", FetchInput{
		OrderID: "ord_1", Cursor: "c1", PageNo: 2, PageSize: 10, Records: 10, Format: "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if body["order_id"] != "ord_1" || body["cursor"] != "c1" || body["page_no"] != float64(2) || body["page_size"] != float64(10) {
		t.Fatalf("body = %#v", body)
	}
	if _, ok := body["dataset_key"]; ok {
		t.Fatalf("later page should not resend dataset_key: %#v", body)
	}
}

func TestClientMapsBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code":"wallet_insufficient","msg":"no balance","request_id":"req_2","retryable":false}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, 0).Fetch(context.Background(), "key", FetchInput{DatasetKey: "ds", Records: 1, Format: "json"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*Error)
	if !ok || apiErr.Code != "wallet_insufficient" || apiErr.RequestID != "req_2" {
		t.Fatalf("err = %#v", err)
	}
}
