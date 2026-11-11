package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"thordata-mcp/internal/auth"
	"thordata-mcp/internal/dataset"
	"thordata-mcp/internal/webscraper"

	"github.com/mark3labs/mcp-go/mcp"
)

func (t *ToolSet) SetDatasetClient(client *dataset.Client) {
	t.datasetClient = client
}

func (t *ToolSet) SetWebScraper(cache *webscraper.Cache, client *webscraper.Client) {
	t.webScraperCache = cache
	t.webScraperClient = client
}

func (t *ToolSet) FetchDatasetTool() mcp.Tool {
	return mcp.Tool{
		Name:        "fetch_dataset",
		Description: "Fetch one 10-record page from a ThorData dataset. This call charges the wallet for records actually returned on this page. First call: pass dataset_key, records (1-10), filter, and idempotency_key; it returns order_id and next_cursor. Next pages: call again with order_id + cursor + page_no; do not resend dataset_key. Stop when next_cursor is empty. Only a dataset API key (X-API-Key) is accepted; SERP tokens are not valid here.",
		Annotations: mcp.ToolAnnotation{
			ReadOnlyHint:    boolPtr(false),
			DestructiveHint: boolPtr(false),
			IdempotentHint:  boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"dataset_key": map[string]any{"type": "string", "description": "First page only: stable dataset key. Available keys depend on your account's dataset configuration."},
				"records":     map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "Records for this page, max 10. Default 10."},
				"order_id":    map[string]any{"type": "string", "description": "Returned by the first page; required for all later pages."},
				"cursor":      map[string]any{"type": "string", "description": "next_cursor returned by the previous page."},
				"page_no":     map[string]any{"type": "integer", "minimum": 1, "description": "Page number; first page is 1."},
				"total_records": map[string]any{"type": "integer", "minimum": 1, "maximum": 100000,
					"description": "Optional total records the user intends to fetch; used for backend tracing."},
				"window_days": map[string]any{"type": "integer", "minimum": 3, "maximum": 30,
					"description": "Time window in days. Default 3 for the first page; extend to 7/15/30 to continue or re-query."},
				"query_group_id": map[string]any{"type": "string",
					"description": "Optional group id; reuse it when re-querying the same user intent."},
				"parent_order_id": map[string]any{"type": "string",
					"description": "For a re-query, the previous order_id; the new order is linked to it."},
				"filter": map[string]any{"type": "object", "additionalProperties": true, "description": "First page only: optional dataset filter."},
				"format": map[string]any{"type": "string", "enum": []string{"json"}, "default": "json"},
				"idempotency_key": map[string]any{
					"type": "string", "minLength": 8, "maxLength": 128,
					"description": "First page only and required on first page: client-generated key; reuse it when retrying the first page. Later pages use order_id + cursor instead.",
				},
			},
			Required: []string{"records"},
		},
		OutputSchema: datasetFetchOutputSchema(),
	}
}

func (t *ToolSet) ListDatasetsTool() mcp.Tool {
	return mcp.Tool{
		Name:        "list_datasets",
		Description: "List the dataset packages currently available to this account. This tool does not charge the wallet.",
		Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
		OutputSchema: datasetListOutputSchema(),
	}
}

func (t *ToolSet) DatasetInfoTool() mcp.Tool {
	return mcp.Tool{
		Name:        "get_dataset_info",
		Description: "Read a ThorData dataset's billing unit price, currency, record limits and batch size. This tool does not charge the wallet.",
		Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{"dataset_key": map[string]any{"type": "string"}},
			Required:   []string{"dataset_key"},
		},
		OutputSchema: datasetInfoOutputSchema(),
	}
}

func (t *ToolSet) DatasetRequestStatusTool() mcp.Tool {
	return mcp.Tool{
		Name:        "get_request_status",
		Description: "Query a dataset order by order_id, or a legacy request by request_id or idempotency_key. This tool does not charge the wallet.",
		Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"order_id":        map[string]any{"type": "string"},
				"request_id":      map[string]any{"type": "string"},
				"idempotency_key": map[string]any{"type": "string"},
			},
		},
		OutputSchema: datasetStatusOutputSchema(),
	}
}

func (t *ToolSet) HandleFetchDataset(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	apiKey, ok := auth.DatasetKeyFromContext(ctx)
	if !ok {
		return toolError("dataset_credential_required: provide a valid API key via MCP URL, Authorization, or X-API-Key")
	}
	if t.datasetClient == nil {
		return toolError("dataset service unavailable")
	}
	args := request.GetArguments()
	datasetKey := stringArg(args["dataset_key"])
	orderID := stringArg(args["order_id"])
	cursor := stringArg(args["cursor"])
	pageNo := datasetIntArg(args["page_no"], 0)
	if orderID == "" && datasetKey == "" {
		return toolError("dataset_key is required for the first page")
	}
	records, recordsOK := datasetIntArgStrict(args["records"])
	if !recordsOK {
		if _, exists := args["records"]; exists {
			return toolError("records must be an integer between 1 and 10")
		}
		records = 10
	}
	if records < 1 || records > 10 {
		return toolError("records must be between 1 and 10; each page is at most 10 records")
	}
	filter, _ := args["filter"].(map[string]any)
	idempotencyKey := stringArg(args["idempotency_key"])
	if orderID == "" && idempotencyKey == "" {
		return toolError("idempotency_key is required for the first page; reuse it when retrying")
	}
	data, err := t.datasetClient.Fetch(ctx, apiKey, dataset.FetchInput{
		DatasetKey:     datasetKey,
		Records:        records,
		OrderID:        orderID,
		Cursor:         cursor,
		PageNo:         pageNo,
		PageSize:       records,
		TotalRecords:   datasetIntArg(args["total_records"], 0),
		WindowDays:     datasetIntArg(args["window_days"], 0),
		QueryGroupID:   stringArg(args["query_group_id"]),
		ParentOrderID:  stringArg(args["parent_order_id"]),
		Filter:         filter,
		Format:         "json",
		Channel:        "mcp",
		Client:         map[string]string{"name": "thordata-mcp", "version": "0.2.0"},
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return toolError(sanitizeError(err, apiKey))
	}
	return structured(data)
}

func (t *ToolSet) HandleListDatasets(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	apiKey, ok := auth.DatasetKeyFromContext(ctx)
	if !ok {
		return toolError("dataset_credential_required: provide a valid API key via MCP URL, Authorization, or X-API-Key")
	}
	if t.datasetClient == nil {
		return toolError("dataset service unavailable")
	}
	data, err := t.datasetClient.ListDatasets(ctx, apiKey)
	if err != nil {
		return toolError(sanitizeError(err, apiKey))
	}
	return structured(data)
}

func (t *ToolSet) HandleDatasetInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	apiKey, ok := auth.DatasetKeyFromContext(ctx)
	if !ok {
		return toolError("dataset_credential_required: provide a valid API key via MCP URL, Authorization, or X-API-Key")
	}
	if t.datasetClient == nil {
		return toolError("dataset service unavailable")
	}
	datasetKey := stringArg(request.GetArguments()["dataset_key"])
	if datasetKey == "" {
		return toolError("dataset_key is required")
	}
	data, err := t.datasetClient.GetDatasetInfo(ctx, apiKey, datasetKey)
	if err != nil {
		return toolError(sanitizeError(err, apiKey))
	}
	return structured(data)
}

func (t *ToolSet) HandleDatasetRequestStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	apiKey, ok := auth.DatasetKeyFromContext(ctx)
	if !ok {
		return toolError("dataset_credential_required: provide a valid API key via MCP URL, Authorization, or X-API-Key")
	}
	if t.datasetClient == nil {
		return toolError("dataset service unavailable")
	}
	args := request.GetArguments()
	orderID := stringArg(args["order_id"])
	requestID := stringArg(args["request_id"])
	key := stringArg(args["idempotency_key"])
	var (
		data map[string]any
		err  error
	)
	switch {
	case orderID != "":
		data, err = t.datasetClient.GetOrder(ctx, apiKey, orderID)
	case requestID != "":
		data, err = t.datasetClient.GetRequest(ctx, apiKey, requestID)
	case key != "":
		data, err = t.datasetClient.GetRequestByIdempotencyKey(ctx, apiKey, key)
	default:
		return toolError("order_id, request_id or idempotency_key is required")
	}
	if err != nil {
		return toolError(sanitizeError(err, apiKey))
	}
	return structured(data)
}

func datasetIntArg(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

// datasetIntArgStrict distinguishes "not provided" from "provided but invalid";
// an invalid value must not be swallowed by the fallback.
func datasetIntArgStrict(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n), true
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func stringArg(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func datasetListOutputSchema() mcp.ToolOutputSchema {
	return mcp.ToolOutputSchema{
		Type: "object",
		Properties: map[string]any{
			"datasets": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"dataset_key":             map[string]any{"type": "string"},
						"display_name":            map[string]any{"type": "string"},
						"billing_mode":            map[string]any{"type": "string"},
						"unit_price":              map[string]any{"type": "string"},
						"currency":                map[string]any{"type": "string"},
						"min_records_per_request": map[string]any{"type": "integer"},
						"max_records_per_request": map[string]any{"type": "integer"},
						"batch_size":              map[string]any{"type": "integer"},
						"billing_note":            map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

func datasetFetchOutputSchema() mcp.ToolOutputSchema {
	return mcp.ToolOutputSchema{
		Type: "object",
		Properties: map[string]any{
			"request_id":              map[string]any{"type": "string"},
			"order_id":                map[string]any{"type": "string"},
			"page_no":                 map[string]any{"type": "integer"},
			"dataset_key":             map[string]any{"type": "string"},
			"dataset_name":            map[string]any{"type": "string"},
			"status":                  map[string]any{"type": "string"},
			"requested_page_size":     map[string]any{"type": "integer"},
			"delivered_records":       map[string]any{"type": "integer"},
			"billed_records":          map[string]any{"type": "integer"},
			"page_amount":             map[string]any{"type": "string"},
			"order_delivered_records": map[string]any{"type": "integer"},
			"order_amount":            map[string]any{"type": "string"},
			"currency":                map[string]any{"type": "string"},
			"next_cursor":             map[string]any{"type": "string"},
			"has_more":                map[string]any{"type": "boolean"},
			"can_extend":              map[string]any{"type": "boolean"},
			"max_window_days":         map[string]any{"type": "integer"},
			"window_days":             map[string]any{"type": "integer"},
			"query_timestamp":         map[string]any{"type": "integer"},
			"records":                 map[string]any{"type": "array"},
			"warnings":                map[string]any{"type": "array"},
		},
	}
}

func datasetInfoOutputSchema() mcp.ToolOutputSchema {
	return mcp.ToolOutputSchema{
		Type: "object",
		Properties: map[string]any{
			"dataset_key":             map[string]any{"type": "string"},
			"display_name":            map[string]any{"type": "string"},
			"billing_mode":            map[string]any{"type": "string"},
			"unit_price":              map[string]any{"type": "string"},
			"currency":                map[string]any{"type": "string"},
			"min_records_per_request": map[string]any{"type": "integer"},
			"max_records_per_request": map[string]any{"type": "integer"},
			"batch_size":              map[string]any{"type": "integer"},
			"billing_note":            map[string]any{"type": "string"},
		},
	}
}

func datasetStatusOutputSchema() mcp.ToolOutputSchema {
	return mcp.ToolOutputSchema{
		Type: "object",
		Properties: map[string]any{
			"request_id":        map[string]any{"type": "string"},
			"order_id":          map[string]any{"type": "string"},
			"dataset_key":       map[string]any{"type": "string"},
			"dataset_name":      map[string]any{"type": "string"},
			"status":            map[string]any{"type": "string"},
			"requested_records": map[string]any{"type": "integer"},
			"delivered_records": map[string]any{"type": "integer"},
			"pages_charged":     map[string]any{"type": "integer"},
			"amount_paid":       map[string]any{"type": "string"},
			"currency":          map[string]any{"type": "string"},
			"next_cursor":       map[string]any{"type": "string"},
			"pages":             map[string]any{"type": "array"},
			"created_at":        map[string]any{"type": "integer"},
			"last_page_no":      map[string]any{"type": "integer"},
			"parent_order_id":   map[string]any{"type": "string"},
			"pay_status":        map[string]any{"type": "integer"},
			"query_group_id":    map[string]any{"type": "string"},
			"query_timestamp":   map[string]any{"type": "integer"},
			"request_params":    map[string]any{"type": "string"},
			"updated_at":        map[string]any{"type": "integer"},
			"window_days":       map[string]any{"type": "integer"},
		},
	}
}
