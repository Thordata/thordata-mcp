package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"thordata-mcp/internal/auth"
	"thordata-mcp/internal/webscraper"
)

const WebScrapersURI = "thordata://web-scrapers"

const webScrapersURI = WebScrapersURI

func (t *ToolSet) ListWebScrapersTool() mcp.Tool {
	return mcp.Tool{
		Name:        "list_web_scrapers",
		Description: "List the seven supported Thordata Web Scraper products and their current public parameters.",
		Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{}},
		OutputSchema: mcp.ToolOutputSchema{Type: "object", Properties: map[string]any{
			"schema_version": map[string]any{"type": "string"},
			"resource":       map[string]any{"type": "string"},
			"scrapers":       map[string]any{"type": "array"},
		}},
	}
}

func (t *ToolSet) FetchWebScraperTool() mcp.Tool {
	return mcp.Tool{
		Name:        "fetch_web_scraper",
		Description: "Run one allowlisted Thordata Web Scraper and wait up to five minutes for its final JSON result. A timeout returns the remote task_id without resubmitting the task.",
		Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(false), DestructiveHint: boolPtr(false), IdempotentHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"spider_id":        map[string]any{"type": "string", "description": "One of the seven supported Web Scraper spider IDs."},
				"parameters":       map[string]any{"type": "object", "additionalProperties": true, "description": "Product parameters from the web scraper schema."},
				"max_wait_seconds": map[string]any{"type": "integer", "minimum": 30, "maximum": 300, "default": 300},
				"include_errors":   map[string]any{"type": "boolean", "default": true},
				"file_name":        map[string]any{"type": "string", "default": "{{TasksID}}"},
			},
			Required: []string{"spider_id", "parameters"},
		},
		OutputSchema: mcp.ToolOutputSchema{Type: "object", Properties: map[string]any{
			"spider_id":          map[string]any{"type": "string"},
			"task_id":            map[string]any{"type": "string"},
			"status":             map[string]any{"type": "string", "enum": []string{"completed", "partial", "empty"}},
			"record_count":       map[string]any{"type": "integer"},
			"error_record_count": map[string]any{"type": "integer"},
			"result":             map[string]any{"type": "array"},
		}},
	}
}

func (t *ToolSet) HandleListWebScrapers(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	document, err := t.WebScrapersDocument(ctx)
	if err != nil {
		return toolError(err.Error())
	}
	return structured(document)
}

func (t *ToolSet) HandleFetchWebScraper(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	token, ok := auth.DatasetKeyFromContext(ctx)
	if !ok {
		return toolError("web_scraper_credential_required: provide a valid Dataset API key via MCP URL, Authorization, or X-API-Key")
	}
	if t.webScraperCache == nil || t.webScraperClient == nil {
		return toolError("web scraper service unavailable")
	}
	args := request.GetArguments()
	id := stringArg(args["spider_id"])
	sch, err := t.webScraperCache.Get(ctx, id)
	if err != nil {
		return toolError(sanitizeError(err, token))
	}
	parameters := coerceMap(args["parameters"])
	validated, err := webscraper.ValidateParameters(sch, parameters)
	if err != nil {
		return toolError(sanitizeError(err, token))
	}
	maxWaitSeconds := 300
	if raw, exists := args["max_wait_seconds"]; exists {
		value, valid := datasetIntArgStrict(raw)
		if !valid || value < 30 || value > 300 {
			return toolError("max_wait_seconds must be an integer between 30 and 300")
		}
		maxWaitSeconds = value
	}
	includeErrors := true
	if raw, exists := args["include_errors"]; exists {
		value, valid := raw.(bool)
		if !valid {
			return toolError("include_errors must be a boolean")
		}
		includeErrors = value
	}
	fileName := stringArg(args["file_name"])
	if fileName == "" {
		fileName = "{{TasksID}}"
	}
	result, err := t.webScraperClient.Fetch(ctx, token, webscraper.Request{
		SpiderID: id, SpiderName: sch.SpiderName, Parameters: validated,
		MaxWait:       time.Duration(maxWaitSeconds) * time.Second,
		IncludeErrors: includeErrors, FileName: fileName,
	})
	if err != nil {
		return toolError(sanitizeWebScraperError(err, token))
	}
	return structured(result)
}

func (t *ToolSet) WebScrapersDocument(ctx context.Context) (map[string]any, error) {
	if t.webScraperCache == nil {
		return nil, fmt.Errorf("schema_unavailable: web scraper schema cache is nil")
	}
	schemas, err := t.webScraperCache.List(ctx)
	if err != nil {
		return nil, err
	}
	scrapers := make([]map[string]any, 0, len(schemas))
	for _, schema := range schemas {
		scrapers = append(scrapers, webScraperSchemaDocument(schema))
	}
	return map[string]any{"schema_version": "1", "resource": "web_scraper", "scrapers": scrapers, "resource_uri": webScrapersURI}, nil
}

func (t *ToolSet) WebScraperDocument(ctx context.Context, id string) (map[string]any, error) {
	if t.webScraperCache == nil {
		return nil, fmt.Errorf("schema_unavailable: web scraper schema cache is nil")
	}
	schema, err := t.webScraperCache.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return webScraperSchemaDocument(schema), nil
}

func webScraperSchemaDocument(schema webscraper.Schema) map[string]any {
	product, _ := webscraper.Product(schema.SpiderID)
	fields := make([]map[string]any, 0, len(schema.Parameters))
	for _, field := range schema.Parameters {
		options := make([]map[string]any, 0, len(field.Options))
		for _, option := range field.Options {
			options = append(options, map[string]any{"label": option.Label, "value": option.Value})
		}
		fields = append(fields, map[string]any{
			"key": field.Key, "label": field.Label, "type": field.Type, "control": field.Control,
			"required": field.Required, "default_value": field.DefaultValue, "description": field.Description, "options": options,
		})
	}
	return map[string]any{
		"spider_id": schema.SpiderID, "name": product.Name, "domain": product.Domain,
		"spider_name": schema.SpiderName, "product_type": schema.ProductType, "execution_path": schema.ExecutionPath,
		"parameters": fields, "resource_uri": webScrapersURI + "/" + schema.SpiderID,
	}
}

func sanitizeWebScraperError(err error, token string) string {
	var webErr *webscraper.Error
	if !errors.As(err, &webErr) {
		return sanitizeError(err, token)
	}
	message := strings.ReplaceAll(webErr.Message, token, "[redacted]")
	parts := []string{webErr.Code}
	if webErr.Stage != "" {
		parts = append(parts, "stage="+webErr.Stage)
	}
	if webErr.SpiderID != "" {
		parts = append(parts, "spider_id="+webErr.SpiderID)
	}
	if webErr.TaskID != "" {
		parts = append(parts, "task_id="+webErr.TaskID)
	}
	if message != "" {
		parts = append(parts, message)
	}
	return strings.Join(parts, ": ")
}
