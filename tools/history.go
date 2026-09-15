package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"thordata-mcp/internal/auth"
	"thordata-mcp/internal/serp"
)

const (
	defaultHTTPTimeout      = 120 * time.Second
	maxUpstreamResponseSize = 10 << 20
)

type httpClient struct {
	client  *http.Client
	timeout time.Duration
}

func newHTTPClient(timeout time.Duration) *httpClient {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &httpClient{client: &http.Client{Timeout: timeout}, timeout: timeout}
}

func (t *ToolSet) HistoryTool() mcp.Tool {
	return mcp.Tool{Name: "history", Description: "Query Thor SERP request history.", Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}, InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{
		"page": map[string]any{"type": "integer"}, "page_size": map[string]any{"type": "integer"}, "search_query": map[string]any{"type": "string"}, "search_engine": map[string]any{"type": "string"}, "status": map[string]any{"type": "string", "enum": []string{"all", "success", "error"}}, "start_time": map[string]any{"type": "integer"}, "end_time": map[string]any{"type": "integer"}, "timezone": map[string]any{"type": "string"},
	}}, OutputSchema: envelopeSchema("history")}
}

func (t *ToolSet) HandleHistory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return toolError("missing user token")
	}
	args := request.GetArguments()
	params := url.Values{}
	page := intArg(args["page"])
	if page <= 0 {
		page = 1
	}
	params.Set("page", strconv.Itoa(page))
	pageSize := intArg(args["page_size"])
	if pageSize <= 0 {
		pageSize = 20
	}
	params.Set("page_size", strconv.Itoa(pageSize))
	params.Set("status", "all")
	for _, key := range []string{"search_query", "search_engine", "status", "timezone"} {
		if value := strings.TrimSpace(fmt.Sprint(args[key])); value != "" && value != "<nil>" {
			if key == "status" && value != "all" && value != "success" && value != "error" {
				return toolError("status must be all, success, or error")
			}
			params.Set(key, value)
		}
	}
	for _, key := range []string{"start_time", "end_time"} {
		if value := intArg(args[key]); value > 0 {
			params.Set(key, strconv.Itoa(value))
		}
	}
	return t.executeGET(ctx, "history", t.historyEndpoint, token, params)
}

func intArg(value any) int {
	switch x := value.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		n, _ := strconv.Atoi(string(x))
		return n
	default:
		return 0
	}
}

func (t *ToolSet) executeGET(ctx context.Context, name, endpoint, token string, params url.Values) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(endpoint) == "" {
		return toolError(name + " endpoint is empty")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return toolError("invalid " + name + " endpoint")
	}
	q := u.Query()
	for key, values := range params {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, t.httpClient.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return toolError("create request failed")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.httpClient.client.Do(req)
	if err != nil {
		cause := upstreamCause(err, token)
		log.Printf("%s upstream request failed: %s", name, cause)
		return toolError("upstream request failed: " + cause)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamResponseSize+1))
	if err != nil {
		log.Printf("%s upstream response read failed: %s", name, sanitizeError(err, token))
		return toolError("read upstream response failed")
	}
	if len(body) > maxUpstreamResponseSize {
		return toolError("upstream response too large")
	}
	body = []byte(strings.ReplaceAll(string(body), token, "[redacted]"))
	data, _, upstreamErr := serp.DecodeBody(body)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300 && upstreamErr == ""
	if !ok {
		log.Printf("%s upstream rejected the request: status=%d error=%q", name, resp.StatusCode, upstreamErr)
	}
	envelope := map[string]any{"ok": ok, "status": resp.StatusCode, "tool": name, "request": map[string]any{"method": req.Method, "url": req.URL.String()}}
	if upstreamErr != "" {
		envelope["error"] = upstreamErr
	}
	if data != nil {
		envelope["data"] = data
	} else {
		envelope["raw"] = string(body)
	}
	encoded, _ := json.Marshal(envelope)
	if !ok {
		return toolError(string(encoded))
	}
	return mcp.NewToolResultStructured(envelope, string(encoded)), nil
}

func envelopeSchema(name string) mcp.ToolOutputSchema {
	return mcp.ToolOutputSchema{Type: "object", Properties: map[string]any{"ok": map[string]any{"type": "boolean"}, "status": map[string]any{"type": "integer"}, "engine": map[string]any{"type": "string"}, "tool": map[string]any{"type": "string"}, "error": map[string]any{"type": "string"}, "request": map[string]any{"type": "object"}, "data": map[string]any{}, "raw": map[string]any{"type": "string"}}, Required: []string{"ok", "status", "request"}}
}
func toolError(message string) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(message), nil
}
func structured(value any) (*mcp.CallToolResult, error) {
	data, _ := json.Marshal(value)
	return mcp.NewToolResultStructured(value, string(data)), nil
}
func sanitizeError(message error, token string) string {
	text := message.Error()
	return strings.ReplaceAll(text, token, "[redacted]")
}

// upstreamCause reduces a transport error to its underlying cause so a DNS
// failure can be told apart from a refused or timed out connection.
func upstreamCause(err error, token string) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	return sanitizeError(err, token)
}
