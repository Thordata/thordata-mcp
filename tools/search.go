package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"thordata-mcp/internal/auth"
	"thordata-mcp/internal/schema"
	"thordata-mcp/internal/serp"

	"github.com/mark3labs/mcp-go/mcp"
)

type ToolSet struct {
	cache              *schema.Cache
	serpClient         *serp.Client
	historyEndpoint    string
	statisticsEndpoint string
	httpClient         *httpClient
}

// New creates the Thor MCP tool set. A nil cache is valid and uses the bundled snapshot.
func New(cache *schema.Cache, serpClient *serp.Client, historyEndpoint, statisticsEndpoint string, timeout ...time.Duration) *ToolSet {
	requestTimeout := defaultHTTPTimeout
	if len(timeout) > 0 {
		requestTimeout = timeout[0]
	}
	return &ToolSet{cache: cache, serpClient: serpClient, historyEndpoint: historyEndpoint, statisticsEndpoint: statisticsEndpoint, httpClient: newHTTPClient(requestTimeout)}
}

func (t *ToolSet) ListEnginesTool() mcp.Tool {
	return mcp.NewTool("list_engines", mcp.WithDescription("List Thor SERP engines and schema resource URIs."), mcp.WithReadOnlyHintAnnotation(true))
}

func (t *ToolSet) SearchTool() mcp.Tool {
	return mcp.Tool{Name: "search", Description: "Execute a Thor SERP search using the current Thor engine schema.", Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true), OpenWorldHint: boolPtr(true)}, InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{
		"engine": map[string]any{"type": "string"}, "q": map[string]any{"type": "string"}, "json": map[string]any{"type": "string", "enum": []string{"1", "2", "3"}}, "params": map[string]any{"type": "object", "additionalProperties": true}, "response_mode": map[string]any{"type": "string", "enum": []string{"complete", "compact"}},
	}}, OutputSchema: envelopeSchema("search")}
}

func (t *ToolSet) HandleListEngines(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if t.cache == nil {
		return toolError("schema cache unavailable")
	}
	doc, err := t.cache.ListDocument(ctx)
	if err != nil {
		return toolError(err.Error())
	}
	return structured(doc)
}

func (t *ToolSet) HandleSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mode := strings.TrimSpace(request.GetString("response_mode", "complete"))
	if mode != "complete" && mode != "compact" {
		return toolError("response_mode must be complete or compact")
	}
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return toolError("missing user token")
	}
	if t.cache == nil || t.serpClient == nil {
		return toolError("search service unavailable")
	}
	s, err := t.cache.Current(ctx)
	if err != nil {
		return toolError(err.Error())
	}
	args := request.GetArguments()
	params := coerceMap(args["params"])
	if params == nil {
		params = map[string]any{}
	}
	for _, key := range []string{"engine", "q", "json"} {
		if value, exists := args[key]; exists && value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
			params[key] = value
		}
	}
	engineKey := strings.TrimSpace(fmt.Sprint(params["engine"]))
	if engineKey == "" {
		engineKey = s.DefaultEngine
	}
	var engine schema.Engine
	for _, candidate := range s.AllEngines() {
		if candidate.Key == engineKey {
			engine = candidate
			break
		}
	}
	if engine.Key == "" {
		return toolError(fmt.Sprintf("unknown engine %q; read thordata://engines", engineKey))
	}
	params["engine"] = engineKey
	if q, ok := params["q"].(string); ok && strings.TrimSpace(q) != "" && engine.QueryField != "q" {
		params[engine.QueryField] = q
	}
	serialized, err := serp.Serialize(engine, params)
	if err != nil {
		return toolError(err.Error())
	}
	jsonValue, exists := params["json"]
	if !exists || strings.TrimSpace(fmt.Sprint(jsonValue)) == "" {
		jsonValue = "1"
	}
	if jsonValue != nil {
		serialized.Set("json", fmt.Sprint(jsonValue))
	}
	result, err := t.serpClient.Execute(ctx, token, serialized)
	if err != nil {
		return toolError(sanitizeError(err, token))
	}
	envelope := map[string]any{"ok": result.OK, "status": result.Status, "engine": engineKey, "request": result.Request}
	if result.Error != "" {
		envelope["error"] = strings.ReplaceAll(result.Error, token, "[redacted]")
	}
	if result.Data != nil {
		data := redactAny(result.Data, token)
		if mode == "compact" {
			data = compact(data)
		}
		envelope["data"] = data
	} else {
		envelope["raw"] = strings.ReplaceAll(result.Text, token, "[redacted]")
	}
	body, _ := json.Marshal(envelope)
	if !result.OK {
		return toolError(string(body))
	}
	return mcp.NewToolResultStructured(envelope, string(body)), nil
}

func coerceMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if out, ok := value.(map[string]any); ok {
		return out
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}
func compact(value any) any {
	if m, ok := value.(map[string]any); ok {
		out := map[string]any{}
		for k, v := range m {
			switch k {
			case "search_metadata", "search_parameters", "serpapi_pagination", "request_metadata":
				continue
			}
			out[k] = compact(v)
		}
		return out
	}
	if a, ok := value.([]any); ok {
		out := make([]any, len(a))
		for i, v := range a {
			out[i] = compact(v)
		}
		return out
	}
	return value
}
func boolPtr(v bool) *bool { return &v }

func redactAny(value any, token string) any {
	switch x := value.(type) {
	case string:
		return strings.ReplaceAll(x, token, "[redacted]")
	case map[string]any:
		out := make(map[string]any, len(x))
		for key, item := range x {
			out[key] = redactAny(item, token)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = redactAny(item, token)
		}
		return out
	default:
		return value
	}
}
