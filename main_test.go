package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"thordata-mcp/internal/auth"
	"time"
)

func TestGoModDeclaresMCPDependency(t *testing.T) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "github.com/mark3labs/mcp-go v0.54.1") {
		t.Fatal("go.mod does not declare github.com/mark3labs/mcp-go v0.54.1")
	}
}

func TestMCPHTTPContextCarriesBearerToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer context-key")
	ctx := mcpHTTPContext(context.Background(), req)
	if got, ok := auth.TokenFromContext(ctx); !ok || got != "context-key" {
		t.Fatalf("token=%q ok=%v", got, ok)
	}
}

func TestLoadConfigReadsConfiguredEndpointsAndTimeouts(t *testing.T) {
	cfg, err := loadConfig("configs/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8800" {
		t.Fatalf("listen address = %q, want :8800", cfg.ListenAddr)
	}
	if cfg.SchemaEndpoint != "https://api.thordata.com/serp/playground/schema?lang=en" {
		t.Fatalf("schema endpoint = %q", cfg.SchemaEndpoint)
	}
	if cfg.SerpEndpoint != "https://scraperapi.thordata.com/request" {
		t.Fatalf("serp endpoint = %q", cfg.SerpEndpoint)
	}
	if cfg.Timeout != 120*time.Second || cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("timeouts = %s/%s", cfg.Timeout, cfg.ShutdownTimeout)
	}
	if cfg.SchemaTimeout != 30*time.Second {
		t.Fatalf("production schema timeout = %s, want 30s", cfg.SchemaTimeout)
	}
}

func TestLoadConfigDefaultsSchemaTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("schema_timeout_ms: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaTimeout != 30*time.Second {
		t.Fatalf("default schema timeout = %s, want 30s", cfg.SchemaTimeout)
	}
}

func TestRoutesExposeRootAndHealth(t *testing.T) {
	h := routes()
	for _, path := range []string{"/", "/healthz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.Code)
		}
		if got := resp.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("GET %s content type = %q", path, got)
		}
		if path == "/" {
			var body map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"name", "version", "transport", "mcp_path"} {
				if _, ok := body[key]; !ok {
					t.Fatalf("GET / response missing %q", key)
				}
			}
		}
	}
}

func TestRoutesReturnNotFoundForUnknownPath(t *testing.T) {
	resp := httptest.NewRecorder()
	routes().ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	if resp.Code != http.StatusNotFound {
		t.Fatalf("GET /unknown status = %d, want 404", resp.Code)
	}
}

func TestRoutesProtectsSingleSegmentMCPCompatibilityPaths(t *testing.T) {
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := routes(mcp)

	cases := []struct {
		name       string
		method     string
		path       string
		header     string
		headerName string
		wantStatus int
	}{
		{name: "openai authorization get", method: http.MethodGet, path: "/openai/mcp", headerName: "Authorization", header: "Bearer header-token", wantStatus: http.StatusNoContent},
		{name: "openai serp token post", method: http.MethodPost, path: "/openai/mcp", headerName: "X-Thordata-Serp-Token", header: "header-token", wantStatus: http.StatusNoContent},
		{name: "openai missing token", method: http.MethodGet, path: "/openai/mcp", wantStatus: http.StatusUnauthorized},
		{name: "path token compatibility", method: http.MethodPost, path: "/path-token/mcp", wantStatus: http.StatusNoContent},
		{name: "root mcp compatibility", method: http.MethodPost, path: "/mcp", headerName: "Authorization", header: "Bearer root-token", wantStatus: http.StatusNoContent},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.headerName != "" {
				req.Header.Set(tc.headerName, tc.header)
			}
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)
			if resp.Code != tc.wantStatus {
				t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, resp.Code, tc.wantStatus)
			}
		})
	}
}

func TestRunServerReturnsListenError(t *testing.T) {
	srv := &http.Server{Addr: "invalid-address"}
	signals := make(chan os.Signal)
	if err := runServer(context.Background(), srv, time.Second, signals); err == nil {
		t.Fatal("runServer returned nil, want listen error")
	}
}
