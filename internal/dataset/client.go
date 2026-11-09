package dataset

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTimeout  = 120 * time.Second
	maxResponseSize = 10 << 20
)

type Client struct {
	Endpoint   string
	HTTPClient *http.Client
}

func NewClient(endpoint string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		Endpoint:   strings.TrimRight(endpoint, "/"),
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

type FetchInput struct {
	DatasetKey     string                 `json:"dataset_key,omitempty"`
	Records        int                    `json:"records,omitempty"`
	OrderID        string                 `json:"order_id,omitempty"`
	Cursor         string                 `json:"cursor,omitempty"`
	PageNo         int                    `json:"page_no,omitempty"`
	PageSize       int                    `json:"page_size,omitempty"`
	TotalRecords   int                    `json:"total_records,omitempty"`
	WindowDays     int                    `json:"window_days,omitempty"`
	QueryGroupID   string                 `json:"query_group_id,omitempty"`
	ParentOrderID  string                 `json:"parent_order_id,omitempty"`
	Filter         map[string]interface{} `json:"filter,omitempty"`
	Format         string                 `json:"format,omitempty"`
	Channel        string                 `json:"channel,omitempty"`
	Client         map[string]string      `json:"client,omitempty"`
	IdempotencyKey string                 `json:"-"`
}

type Error struct {
	Code      string
	Message   string
	Status    int
	RequestID string
	Retryable bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (c *Client) Fetch(ctx context.Context, apiKey string, input FetchInput) (map[string]any, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, "/fetch", apiKey, input.IdempotencyKey, body)
}

func (c *Client) GetRequest(ctx context.Context, apiKey, requestID string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/requests/"+requestID, apiKey, "", nil)
}

func (c *Client) GetOrder(ctx context.Context, apiKey, orderID string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/orders/"+orderID, apiKey, "", nil)
}

func (c *Client) GetRequestByIdempotencyKey(ctx context.Context, apiKey, key string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/requests/by-idempotency-key/"+key, apiKey, "", nil)
}

func (c *Client) GetDatasetInfo(ctx context.Context, apiKey, datasetKey string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/datasets/"+datasetKey, apiKey, "", nil)
}

func (c *Client) ListDatasets(ctx context.Context, apiKey string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/datasets", apiKey, "", nil)
}

func (c *Client) do(ctx context.Context, method, path, apiKey, idempotencyKey string, body []byte) (map[string]any, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, &Error{Code: "dataset_credential_required", Message: "dataset API key is required", Status: http.StatusUnauthorized}
	}
	if c == nil || c.Endpoint == "" {
		return nil, &Error{Code: "dataset_endpoint_missing", Message: "dataset endpoint is not configured", Status: http.StatusInternalServerError}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &Error{Code: "gateway_timeout", Message: err.Error(), Status: http.StatusGatewayTimeout, Retryable: true}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponseSize+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, &Error{Code: "gateway_timeout", Message: "read dataset response failed", Status: http.StatusGatewayTimeout, Retryable: true}
	}
	if len(raw) > maxResponseSize {
		return nil, &Error{Code: "result_too_large", Message: "dataset response exceeds size limit", Status: http.StatusRequestEntityTooLarge}
	}
	var envelope struct {
		Code      json.RawMessage `json:"code"`
		Msg       string          `json:"msg"`
		Message   string          `json:"message"`
		RequestID string          `json:"request_id"`
		Retryable bool            `json:"retryable"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, &Error{Code: "dataset_invalid_response", Message: "dataset response is not valid JSON", Status: http.StatusBadGateway, Retryable: true, RequestID: envelope.RequestID}
	}
	success := isSuccessCode(envelope.Code)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !success {
		message := envelope.Msg
		if message == "" {
			message = envelope.Message
		}
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, &Error{
			Code:      errorCode(envelope.Code, resp.StatusCode),
			Message:   message,
			Status:    resp.StatusCode,
			RequestID: envelope.RequestID,
			Retryable: envelope.Retryable,
		}
	}
	var data map[string]any
	if len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return nil, &Error{Code: "dataset_invalid_response", Message: "dataset data is not an object", Status: http.StatusBadGateway, Retryable: true, RequestID: envelope.RequestID}
		}
	}
	if data == nil {
		data = map[string]any{}
	}
	if envelope.RequestID != "" {
		data["request_id"] = envelope.RequestID
	}
	return data, nil
}

func isSuccessCode(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "0" || trimmed == `"0"` || trimmed == "null"
}

func errorCode(raw json.RawMessage, status int) string {
	trimmed := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if trimmed != "" && trimmed != "0" && trimmed != "null" {
		return trimmed
	}
	switch status {
	case http.StatusPaymentRequired:
		return "wallet_insufficient"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusGatewayTimeout:
		return "gateway_timeout"
	case http.StatusBadGateway:
		return "data_group_unavailable"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return "invalid_argument"
	}
}
