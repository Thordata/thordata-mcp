package serp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientExecutePostsFormWithHeadersAndParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("authorization %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Errorf("content type %q", got)
		}
		if got := r.Header.Get("platform"); got != "mcp" {
			t.Errorf("platform %q, want mcp", got)
		}
		if got := r.Header.Get("api-source"); got != "api" {
			t.Errorf("api-source %q, want api", got)
		}
		_ = r.ParseForm()
		if r.Form.Get("engine") != "google" || r.Form.Get("num") != "0" || r.Form.Get("flag") != "false" {
			t.Errorf("form %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()
	result, err := NewClient(srv.URL).Execute(context.Background(), "key", map[string]any{"engine": "google", "num": 0, "flag": false})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Status != http.StatusCreated || result.Request == nil || result.Text == "" || result.Data == nil {
		t.Fatalf("result %#v", result)
	}
	if _, leaked := result.Request["authorization"]; leaked {
		t.Fatal("request metadata leaks authorization")
	}
}

func TestClientExecuteRejectsEmptyToken(t *testing.T) {
	_, err := NewClient("http://127.0.0.1").Execute(context.Background(), "", nil)
	if err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestClientExecuteRejectsOversizedRequestBody(t *testing.T) {
	params := map[string]any{"q": strings.Repeat("x", 2<<20)}
	_, err := NewClient("http://127.0.0.1").Execute(context.Background(), "key", params)
	if err == nil {
		t.Fatal("oversized request accepted")
	}
}

func TestClientExecutePreservesDirectJSONObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"answer":42}`)) }))
	defer srv.Close()
	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["answer"] != float64(42) {
		t.Fatalf("data %#v", result.Data)
	}
}

func TestClientExecuteMarksNonZeroBusinessCodeAsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":1001,"message":"invalid query","data":null}`))
	}))
	defer srv.Close()
	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatalf("business error was accepted: %#v", result)
	}
}

func TestClientExecuteAcceptsThordataSuccessCode200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"result":{"organic":[{"title":"ok"}]}}}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("Thordata success code 200 was rejected: %#v", result)
	}
	if result.Code == nil || *result.Code != 200 {
		t.Fatalf("business code = %v, want 200", result.Code)
	}
}

func TestClientExecuteRejectsUpstreamStatusCode400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"statusCode":400,"message":"Google Images request was rejected"}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatalf("upstream statusCode 400 was accepted: %#v", result)
	}
	if result.Error != "Google Images request was rejected" {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestClientExecuteRejectsErrorTextPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":"error, Collection failed"}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatalf("error text payload was accepted: %#v", result)
	}
	if result.Error != "error, Collection failed" {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestClientExecuteRejectsBareErrorString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`"error, Collection failed"`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error == "" {
		t.Fatalf("bare error string was accepted: %#v", result)
	}
}

func TestClientExecuteRejectsErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error != "invalid api key" {
		t.Fatalf("error field was accepted: %#v", result)
	}
}

func TestClientExecuteUnwrapsJSONStringPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":"{\"organic_results\":[{\"title\":\"ok\"}]}"}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("double encoded payload was rejected: %#v", result)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("data was not unwrapped: %#v", result.Data)
	}
	results, ok := data["organic_results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("organic_results %#v", data["organic_results"])
	}
}

func TestClientExecuteKeepsPlainStringPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":"1234"}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Data != "1234" {
		t.Fatalf("plain string payload was rewritten: %#v", result)
	}
}

func TestClientExecuteAcceptsStringBusinessCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"200","statusCode":"200","data":{"organic":[]}}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Code == nil || *result.Code != 200 {
		t.Fatalf("string business code was rejected: %#v", result)
	}
}

func TestClientExecuteReportsHTTPAndTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad", 502) }))
	result, err := NewClient(srv.URL).Execute(context.Background(), "key", nil)
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Status != 502 || result.Text != "bad\n" {
		t.Fatalf("result %#v", result)
	}
	result, err = NewClient("://bad").Execute(context.Background(), "key", nil)
	if err == nil || result.Request != nil {
		t.Fatalf("expected request error, %#v %v", result, err)
	}
}
