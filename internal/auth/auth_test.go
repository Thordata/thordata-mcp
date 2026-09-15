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
