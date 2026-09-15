package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultSchemaTimeout = 30 * time.Second

type Client struct {
	Endpoint   string
	HTTPClient *http.Client
	timeout    time.Duration
}

func NewClient(endpoint string, timeouts ...time.Duration) *Client {
	timeout := defaultSchemaTimeout
	if len(timeouts) > 0 && timeouts[0] > 0 {
		timeout = timeouts[0]
	}
	return &Client{Endpoint: endpoint, HTTPClient: &http.Client{Timeout: timeout}, timeout: timeout}
}

func (c *Client) Fetch(ctx context.Context) (Schema, error) {
	if c == nil || c.Endpoint == "" {
		return Schema{}, fmt.Errorf("schema endpoint is empty")
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultSchemaTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return Schema{}, fmt.Errorf("create schema request: %w", err)
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Schema{}, fmt.Errorf("fetch schema: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Schema{}, fmt.Errorf("schema endpoint returned HTTP %d", resp.StatusCode)
	}
	const maxSchemaResponse = 16 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSchemaResponse+1))
	if err != nil {
		return Schema{}, fmt.Errorf("read schema response: %w", err)
	}
	if len(body) > maxSchemaResponse {
		return Schema{}, fmt.Errorf("schema response exceeds %d bytes", maxSchemaResponse)
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data Schema `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Schema{}, fmt.Errorf("decode schema response: %w", err)
	}
	if envelope.Code != 0 {
		return Schema{}, fmt.Errorf("schema API code %d: %s", envelope.Code, envelope.Msg)
	}
	if err := validate(envelope.Data); err != nil {
		return Schema{}, fmt.Errorf("invalid schema: %w", err)
	}
	return envelope.Data, nil
}
