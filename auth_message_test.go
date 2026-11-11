package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPRoutesMissingCredentialMessage(t *testing.T) {
	for _, path := range []string{"/mcp", "/openai/mcp"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(method+path, func(t *testing.T) {
				called := false
				handler := routes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
				}))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
				if response.Code != http.StatusUnauthorized || called {
					t.Fatalf("status=%d called=%v", response.Code, called)
				}
				message := response.Body.String()
				for _, want := range []string{"missing API key", "Authorization: Bearer", "X-API-Key", "/<API_KEY>/mcp"} {
					if !strings.Contains(message, want) {
						t.Errorf("message %q does not contain %q", message, want)
					}
				}
				if strings.Contains(message, "SERP") {
					t.Errorf("shared authentication message is SERP-specific: %q", message)
				}
			})
		}
	}
}

func TestMCPRoutesKeepSpecificAuthenticationErrors(t *testing.T) {
	for _, tc := range []struct {
		name, path, authorization, want string
	}{
		{"malformed bearer", "/mcp", "Bearer", "malformed Authorization header"},
		{"query token", "/mcp?token=test-key", "", "token query parameter is not allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := routes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid credential reached MCP")
			}))
			request := httptest.NewRequest(http.MethodPost, tc.path, nil)
			request.Header.Set("Authorization", tc.authorization)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || strings.TrimSpace(response.Body.String()) != tc.want {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}
