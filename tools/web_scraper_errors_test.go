package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"thordata-mcp/internal/auth"
	"thordata-mcp/internal/webscraper"
)

func TestFetchWebScraperPreservesBuilderDataError(t *testing.T) {
	const spiderID = "linkedin_company_information_by-url"
	const token = "fixture-secret-key"
	const reason = "API key authentication failed: error-6"
	for _, tc := range []struct {
		name, upstreamReason, want string
	}{
		{"data reason", reason, reason},
		{"redacted data reason", reason + " key=" + token, reason + " key=[redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var submissions, laterRequests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/operations":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
						"schema_version": "1", "resource": "web_scraper", "operations": []any{map[string]any{
							"value": spiderID, "name": "URL", "spider_id": spiderID, "spider_name": "linkedin.com", "product_type": "text", "execution_path": "/builder",
						}},
					}})
				case "/parameters":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
						"schema_version": "1", "resource": "web_scraper", "spider_id": spiderID, "spider_name": "linkedin.com",
						"product_type": "text", "execution_path": "/builder", "general_parameters": []any{}, "validation_rules": []any{}, "warnings": []any{},
						"parameters": []any{map[string]any{"key": "url", "label": "URL", "type": "string", "control": "input", "required": true}},
					}})
				case "/builder":
					submissions.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "data": tc.upstreamReason})
				default:
					laterRequests.Add(1)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			toolSet := New(nil, nil, "", "")
			toolSet.SetWebScraper(webscraper.NewCache(webscraper.NewSchemaClient(upstream.URL, time.Second)),
				webscraper.NewClient(webscraper.Endpoints{Builder: upstream.URL + "/builder", TaskAPI: upstream.URL, Download: upstream.URL + "/download"}, time.Second))
			result, err := toolSet.HandleFetchWebScraper(auth.WithDatasetKey(context.Background(), token), mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: "fetch_web_scraper", Arguments: map[string]any{"spider_id": spiderID, "parameters": map[string]any{"url": "https://linkedin.com/company/example"}},
			}})
			if err != nil || result == nil || !result.IsError || len(result.Content) == 0 {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			content, ok := result.Content[0].(mcp.TextContent)
			if !ok {
				t.Fatalf("unexpected content type %T", result.Content[0])
			}
			for _, want := range []string{"authentication_failed", "stage=submit", tc.want} {
				if !strings.Contains(content.Text, want) {
					t.Errorf("MCP error %q missing %q", content.Text, want)
				}
			}
			if strings.Contains(content.Text, token) {
				t.Error("API key leaked in MCP error")
			}
			if submissions.Load() != 1 || laterRequests.Load() != 0 {
				t.Fatalf("submissions=%d laterRequests=%d", submissions.Load(), laterRequests.Load())
			}
		})
	}
}
