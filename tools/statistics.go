package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"thordata-mcp/internal/auth"
)

func (t *ToolSet) StatisticsTool() mcp.Tool {
	return mcp.Tool{Name: "statistics", Description: "Query Thor SERP usage statistics.", Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}, InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{
		"start_date": map[string]any{"type": "string"}, "end_date": map[string]any{"type": "string"}, "engines": map[string]any{}, "timezone": map[string]any{"type": "string"},
	}, Required: []string{"start_date", "end_date"}}, OutputSchema: envelopeSchema("statistics")}
}

func (t *ToolSet) HandleStatistics(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return toolError("missing user token")
	}
	args := request.GetArguments()
	start := strings.TrimSpace(fmt.Sprint(args["start_date"]))
	end := strings.TrimSpace(fmt.Sprint(args["end_date"]))
	if start == "" || start == "<nil>" || end == "" || end == "<nil>" {
		return toolError("start_date and end_date are required")
	}
	params := make(url.Values)
	params.Set("start_date", start)
	params.Set("end_date", end)
	if value := normalizeEngines(args["engines"]); value != "" {
		params.Set("engines", value)
	}
	if value := strings.TrimSpace(fmt.Sprint(args["timezone"])); value != "" && value != "<nil>" {
		params.Set("timezone", value)
	}
	return t.executeGET(ctx, "statistics", t.statisticsEndpoint, token, params)
}

func normalizeEngines(value any) string {
	switch x := value.(type) {
	case string:
		return strings.TrimSpace(x)
	case []string:
		return strings.Join(x, ",")
	case []any:
		values := make([]string, 0, len(x))
		for _, item := range x {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
				values = append(values, text)
			}
		}
		return strings.Join(values, ",")
	default:
		return ""
	}
}
