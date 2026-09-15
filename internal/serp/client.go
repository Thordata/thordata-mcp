package serp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	requestTimeout   = 120 * time.Second
	maxResponseSize  = 10 << 20
	maxRequestSize   = 1 << 20
	maxPayloadUnwrap = 2
)

type Client struct {
	Endpoint   string
	HTTPClient *http.Client
}

type Result struct {
	OK      bool
	Status  int
	Request map[string]string
	Data    any
	Text    string
	Code    *int
	Error   string
}

func NewClient(endpoint string) *Client {
	return &Client{Endpoint: endpoint, HTTPClient: &http.Client{Timeout: requestTimeout}}
}

func (c *Client) Execute(ctx context.Context, token string, params any) (Result, error) {
	if strings.TrimSpace(token) == "" {
		return Result{}, fmt.Errorf("missing user token")
	}
	if c == nil || c.Endpoint == "" {
		return Result{}, fmt.Errorf("serp endpoint is empty")
	}
	form, err := formValues(params)
	if err != nil {
		return Result{}, err
	}
	encoded := form.Encode()
	if len(encoded) > maxRequestSize {
		return Result{}, fmt.Errorf("SERP request exceeds %d bytes", maxRequestSize)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, strings.NewReader(encoded))
	if err != nil {
		return Result{}, fmt.Errorf("create SERP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("platform", "mcp")
	req.Header.Set("api-source", "api")
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout}
	}
	resp, err := hc.Do(req)
	result := Result{Request: map[string]string{"method": req.Method, "url": req.URL.String()}}
	if err != nil {
		return result, fmt.Errorf("execute SERP request: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponseSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return result, fmt.Errorf("read SERP response: %w", err)
	}
	if len(body) > maxResponseSize {
		return result, fmt.Errorf("SERP response exceeds %d bytes", maxResponseSize)
	}
	result.Status, result.OK, result.Text = resp.StatusCode, resp.StatusCode >= 200 && resp.StatusCode < 300, string(body)
	result.Data, result.Code, result.Error = DecodeBody(body)
	if result.Error != "" {
		result.OK = false
	}
	return result, nil
}

// DecodeBody reads the payload, business code and failure message out of an
// upstream body. Upstreams disagree on shape: some wrap the result in
// {"code":..,"data":..}, some return it directly, and some encode the payload as
// a JSON string, so a failure is only ruled out after every known signal is
// checked. The returned error message is empty when the body reports success.
func DecodeBody(body []byte) (any, *int, string) {
	var parsed any
	if json.Unmarshal(body, &parsed) != nil {
		return nil, nil, ""
	}
	var code *int
	var errMsg string
	payload := parsed
	if obj, isObject := parsed.(map[string]any); isObject {
		if value, ok := responseInt(obj["code"]); ok {
			code = &value
		}
		if value, ok := responseInt(obj["statusCode"]); ok && value >= http.StatusBadRequest {
			errMsg = upstreamMessage(obj, fmt.Sprintf("upstream statusCode %d", value))
		} else if code != nil && *code != 0 && *code != http.StatusOK {
			errMsg = upstreamMessage(obj, fmt.Sprintf("upstream code %d", *code))
		} else if text, ok := obj["error"].(string); ok && strings.TrimSpace(text) != "" {
			errMsg = strings.TrimSpace(text)
		}
		if data, exists := obj["data"]; exists {
			payload = data
		}
	}
	payload = unwrapJSONPayload(payload)
	if text, isText := payload.(string); isText {
		if message, isError := errorText(text); isError {
			errMsg = message
		}
	}
	return payload, code, errMsg
}

// responseInt reads a numeric upstream field that may also arrive as a string.
func responseInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), typed == float64(int(typed))
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}

// upstreamMessage prefers the upstream's own wording for a failure.
func upstreamMessage(obj map[string]any, fallback string) string {
	for _, key := range []string{"error", "message", "msg"} {
		if text, ok := obj[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return fallback
}

// unwrapJSONPayload decodes payloads that upstreams double-encode as JSON
// strings, so callers always receive structured data. Plain strings are left
// untouched.
func unwrapJSONPayload(payload any) any {
	for i := 0; i < maxPayloadUnwrap; i++ {
		text, isText := payload.(string)
		if !isText {
			return payload
		}
		trimmed := strings.TrimSpace(text)
		if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
			return payload
		}
		var decoded any
		if json.Unmarshal([]byte(trimmed), &decoded) != nil {
			return payload
		}
		switch decoded.(type) {
		case map[string]any, []any:
			payload = decoded
		default:
			return payload
		}
	}
	return payload
}

// errorText reports whether a plain-text payload is an upstream failure message.
func errorText(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "error") || strings.HasPrefix(lower, "failed") {
		return trimmed, true
	}
	return "", false
}

func formValues(params any) (url.Values, error) {
	out := make(url.Values)
	switch x := params.(type) {
	case nil:
		return out, nil
	case url.Values:
		for k, values := range x {
			out[k] = append([]string(nil), values...)
		}
	case map[string]string:
		for k, v := range x {
			out.Set(k, v)
		}
	case map[string]any:
		for k, v := range x {
			if v == nil {
				continue
			}
			switch values := v.(type) {
			case []string:
				out[k] = append([]string(nil), values...)
			case []any:
				for _, item := range values {
					out.Add(k, fmt.Sprint(item))
				}
			case bool:
				out.Set(k, strconv.FormatBool(values))
			default:
				out.Set(k, fmt.Sprint(v))
			}
		}
	default:
		return nil, fmt.Errorf("unsupported form params %T", params)
	}
	return out, nil
}
