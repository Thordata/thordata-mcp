package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractTokenSupportsBearerHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	got, err := ExtractToken(r)
	if err != nil || got != "abc123" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestExtractTokenSupportsThorHeaderAndPath(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("X-Thordata-Serp-Token", "header-token")
	got, err := ExtractToken(r)
	if err != nil || got != "header-token" {
		t.Fatalf("got %q, err %v", got, err)
	}
	r = httptest.NewRequest(http.MethodPost, "/path-token/mcp", nil)
	got, err = ExtractToken(r)
	if err != nil || got != "path-token" {
		t.Fatalf("path got %q, err %v", got, err)
	}
}

func TestExtractTokenRejectsPlatformPathWithoutHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/platform/mcp", nil)
	if _, err := ExtractToken(r); err == nil {
		t.Fatal("platform path was accepted as a token")
	}
}

func TestExtractTokenRejectsMalformedMissingAndQueryToken(t *testing.T) {
	cases := []string{"", "Basic abc", "Bearer", "Bearer a b", "Bearer "}
	for _, authorization := range cases {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		if _, err := ExtractToken(r); err == nil {
			t.Errorf("authorization %q was accepted", authorization)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp?token=abc", nil)
	if _, err := ExtractToken(r); err == nil {
		t.Fatal("query token accepted")
	}
}

func TestMiddlewareStoresOnlyContextToken(t *testing.T) {
	var got string
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = TokenFromContext(r.Context())
		if r.URL.Query().Get("token") != "" {
			t.Error("token leaked through query")
		}
	}))
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "secret" {
		t.Fatalf("context token %q", got)
	}
	if _, ok := TokenFromContext(context.Background()); ok {
		t.Fatal("background context unexpectedly has token")
	}
}

func TestMiddlewareAcceptsDatasetKeyWithoutSerpToken(t *testing.T) {
	var got string
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = DatasetKeyFromContext(r.Context())
		if _, ok := TokenFromContext(r.Context()); ok {
			t.Error("dataset-only request must not get a SERP token")
		}
	}))
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("X-API-Key", "ac-api-key")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "ac-api-key" {
		t.Fatalf("dataset key %q", got)
	}
}

func TestExtractCredentialsKeepsBothIndependent(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer serp-token")
	r.Header.Set("X-API-Key", "ac-api-key")
	serp, datasetKey, err := ExtractCredentials(r)
	if err != nil || serp != "serp-token" || datasetKey != "ac-api-key" {
		t.Fatalf("serp=%q dataset=%q err=%v", serp, datasetKey, err)
	}
}

func TestUnifiedPathTokenProvidesDatasetKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api_unifiedtoken1234567890/mcp", nil)
	serp, datasetKey, err := ExtractCredentials(r)
	if err != nil || serp == "" || datasetKey != serp {
		t.Fatalf("serp=%q dataset=%q err=%v", serp, datasetKey, err)
	}
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, ok := DatasetKeyFromContext(r.Context()); !ok || got != datasetKey {
			t.Errorf("dataset context=%q ok=%v", got, ok)
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), r)
}
