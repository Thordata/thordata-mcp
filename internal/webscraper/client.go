package webscraper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	defaultMaxWait       = 5 * time.Minute
	maxScraperResponse   = 10 << 20
	builderRequestLimit  = 40 * time.Second
	statusRequestLimit   = 15 * time.Second
	downloadRequestLimit = 15 * time.Second
	fileRequestLimit     = 60 * time.Second
)

var taskIDPattern = regexp.MustCompile(`^[\w-]{1,200}$`)

type Error struct {
	Code     string
	Stage    string
	SpiderID string
	TaskID   string
	Message  string
}

func (e *Error) Error() string {
	parts := []string{e.Code}
	if e.Stage != "" {
		parts = append(parts, "stage="+e.Stage)
	}
	if e.SpiderID != "" {
		parts = append(parts, "spider_id="+e.SpiderID)
	}
	if e.TaskID != "" {
		parts = append(parts, "task_id="+e.TaskID)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, ": ")
}

type Endpoints struct {
	Builder  string
	TaskAPI  string
	Download string
}

type Request struct {
	SpiderID      string
	SpiderName    string
	Parameters    map[string]any
	IncludeErrors bool
	FileName      string
	MaxWait       time.Duration
}

type Result struct {
	SpiderID         string           `json:"spider_id"`
	TaskID           string           `json:"task_id"`
	Status           string           `json:"status"`
	RecordCount      int              `json:"record_count"`
	ErrorRecordCount int              `json:"error_record_count,omitempty"`
	Result           []map[string]any `json:"result"`
}

type Client struct {
	Endpoints  Endpoints
	HTTPClient *http.Client
	Timeout    time.Duration
	Sleep      func(context.Context, time.Duration) error
}

func NewClient(endpoints Endpoints, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		Endpoints:  endpoints,
		HTTPClient: &http.Client{Timeout: timeout},
		Timeout:    timeout,
		Sleep:      sleepContext,
	}
}

func (c *Client) Fetch(ctx context.Context, token string, input Request) (Result, error) {
	if strings.TrimSpace(token) == "" {
		return Result{}, &Error{Code: "web_scraper_credential_required", Stage: "validate", SpiderID: input.SpiderID}
	}
	product, ok := Product(input.SpiderID)
	if !ok {
		return Result{}, &Error{Code: "invalid_parameters", Stage: "validate", SpiderID: input.SpiderID, Message: "unsupported spider_id"}
	}
	if strings.TrimSpace(input.SpiderName) == "" {
		input.SpiderName = product.Domain
	}
	if input.MaxWait <= 0 {
		input.MaxWait = defaultMaxWait
	}
	if input.MaxWait > defaultMaxWait {
		input.MaxWait = defaultMaxWait
	}
	if input.FileName == "" {
		input.FileName = "{{TasksID}}"
	}
	if strings.TrimSpace(input.FileName) == "" {
		return Result{}, &Error{Code: "invalid_parameters", Stage: "validate", SpiderID: input.SpiderID, Message: "file_name cannot be empty"}
	}
	deadline := time.Now().Add(input.MaxWait)
	request := func(stage string, limit time.Duration, method, endpoint string, fields url.Values, auth string) ([]byte, error) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, &Error{Code: "wait_timeout", Stage: stage, SpiderID: input.SpiderID}
		}
		if limit > remaining {
			limit = remaining
		}
		requestCtx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		var body io.Reader
		if fields != nil {
			fields.Set("api_source", "api")
			fields.Set("source_platform", "mcp")
			body = strings.NewReader(fields.Encode())
		}
		req, err := http.NewRequestWithContext(requestCtx, method, endpoint, body)
		if err != nil {
			return nil, &Error{Code: "request_failed", Stage: stage, SpiderID: input.SpiderID, Message: redactText(err.Error(), token)}
		}
		req.Header.Set("Accept", "application/json")
		if fields != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if fields != nil {
			req.Header.Set("platform", "mcp")
			req.Header.Set("api-source", "api")
		}
		if auth == "bearer" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if auth == "sign" {
			req.Header.Set("sign", token)
		}
		client := c.HTTPClient
		if method == http.MethodGet {
			client = cloneNoRedirectClient(client, limit)
		}
		resp, err := client.Do(req)
		if err != nil {
			if time.Until(deadline) <= 0 {
				return nil, &Error{Code: "wait_timeout", Stage: stage, SpiderID: input.SpiderID}
			}
			code := "request_failed"
			if stage == "submit" {
				code = "submission_unknown"
			}
			return nil, &Error{Code: code, Stage: stage, SpiderID: input.SpiderID, Message: redactText(err.Error(), token)}
		}
		defer resp.Body.Close()
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxScraperResponse+1))
		if readErr != nil {
			return nil, &Error{Code: "request_failed", Stage: stage, SpiderID: input.SpiderID, Message: redactText(readErr.Error(), token)}
		}
		if len(data) > maxScraperResponse {
			return nil, &Error{Code: "protocol_error", Stage: stage, SpiderID: input.SpiderID, Message: "upstream response is too large"}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			code := "request_failed"
			if stage == "submit" {
				code = "submission_failed"
				if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
					code = "submission_unknown"
				}
			}
			return nil, &Error{Code: code, Stage: stage, SpiderID: input.SpiderID, Message: redactText(strings.TrimSpace(string(data)), token)}
		}
		return data, nil
	}

	encodedParameters, err := json.Marshal([]map[string]any{input.Parameters})
	if err != nil {
		return Result{}, &Error{Code: "invalid_parameters", Stage: "validate", SpiderID: input.SpiderID, Message: err.Error()}
	}
	submissionBody, err := request("submit", builderRequestLimit, http.MethodPost, c.Endpoints.Builder, url.Values{
		"spider_name":       {input.SpiderName},
		"spider_id":         {input.SpiderID},
		"spider_parameters": {string(encodedParameters)},
		"spider_errors":     {fmt.Sprintf("%t", input.IncludeErrors)},
		"file_name":         {input.FileName},
	}, "bearer")
	if err != nil {
		return Result{}, err
	}
	submission, err := decodeEnvelope(submissionBody)
	if err != nil || submission.Code != http.StatusOK {
		return Result{}, &Error{Code: submissionCode(submission.Code, err), Stage: "submit", SpiderID: input.SpiderID, Message: envelopeMessage(submission, err)}
	}
	var submissionData struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(submission.Data, &submissionData); err != nil || !taskIDPattern.MatchString(submissionData.TaskID) || strings.Contains(submissionData.TaskID, token) {
		return Result{}, &Error{Code: "submission_unknown", Stage: "submit", SpiderID: input.SpiderID}
	}
	taskID := submissionData.TaskID

	for {
		body, requestErr := request("poll", statusRequestLimit, http.MethodPost, strings.TrimRight(c.Endpoints.TaskAPI, "/")+"/tasks-status", url.Values{"tasks_ids": {taskID}}, "sign")
		if requestErr != nil {
			return Result{}, withTask(requestErr, taskID)
		}
		poll, parseErr := decodeEnvelope(body)
		if parseErr != nil || poll.Code != http.StatusOK {
			return Result{}, &Error{Code: requestCode(poll.Code, parseErr), Stage: "poll", SpiderID: input.SpiderID, TaskID: taskID, Message: envelopeMessage(poll, parseErr)}
		}
		var statuses []struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(poll.Data, &statuses); err != nil {
			return Result{}, &Error{Code: "protocol_error", Stage: "poll", SpiderID: input.SpiderID, TaskID: taskID, Message: "status data is invalid"}
		}
		matches := make([]struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		}, 0, 1)
		for _, item := range statuses {
			if item.TaskID == taskID {
				matches = append(matches, item)
			}
		}
		if len(matches) != 1 || (matches[0].Status != "Running" && matches[0].Status != "Ready" && matches[0].Status != "Failed") {
			return Result{}, &Error{Code: "protocol_error", Stage: "poll", SpiderID: input.SpiderID, TaskID: taskID, Message: "task status is invalid"}
		}
		switch matches[0].Status {
		case "Failed":
			return Result{}, &Error{Code: "scrape_failed", Stage: "poll", SpiderID: input.SpiderID, TaskID: taskID, Message: "upstream task failed"}
		case "Ready":
			goto ready
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return Result{}, &Error{Code: "wait_timeout", Stage: "poll", SpiderID: input.SpiderID, TaskID: taskID}
		}
		wait := 5 * time.Second
		if wait > remaining {
			wait = remaining
		}
		if err := c.Sleep(ctx, wait); err != nil {
			if time.Until(deadline) <= 0 || errors.Is(err, context.DeadlineExceeded) {
				return Result{}, &Error{Code: "wait_timeout", Stage: "poll", SpiderID: input.SpiderID, TaskID: taskID}
			}
			return Result{}, &Error{Code: "request_failed", Stage: "poll", SpiderID: input.SpiderID, TaskID: taskID, Message: redactText(err.Error(), token)}
		}
	}

ready:
	addressBody, requestErr := request("download_url", downloadRequestLimit, http.MethodPost, strings.TrimRight(c.Endpoints.TaskAPI, "/")+"/tasks-download", url.Values{"tasks_id": {taskID}, "type": {"json"}}, "sign")
	if requestErr != nil {
		return Result{}, withTask(requestErr, taskID)
	}
	address, parseErr := decodeEnvelope(addressBody)
	if parseErr != nil || address.Code != http.StatusOK {
		return Result{}, &Error{Code: requestCode(address.Code, parseErr), Stage: "download_url", SpiderID: input.SpiderID, TaskID: taskID, Message: envelopeMessage(address, parseErr)}
	}
	var downloadData struct {
		TasksID  string `json:"tasks_ids"`
		Download string `json:"download"`
	}
	if err := json.Unmarshal(address.Data, &downloadData); err != nil || downloadData.TasksID != taskID {
		return Result{}, &Error{Code: "protocol_error", Stage: "download_url", SpiderID: input.SpiderID, TaskID: taskID, Message: "download response is invalid"}
	}
	downloadURL, err := validateDownloadURL(downloadData.Download, c.Endpoints.Download, taskID)
	if err != nil {
		return Result{}, &Error{Code: "protocol_error", Stage: "download_url", SpiderID: input.SpiderID, TaskID: taskID, Message: err.Error()}
	}
	fileBody, requestErr := request("download", fileRequestLimit, http.MethodGet, downloadURL.String(), nil, "")
	if requestErr != nil {
		return Result{}, withTask(requestErr, taskID)
	}
	result, err := ParseResult(fileBody, taskID, input.SpiderID, nil, []string{token, downloadURL.Query().Get("api_key")})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

type envelope struct {
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decodeEnvelope(body []byte) (envelope, error) {
	var result envelope
	if err := json.Unmarshal(body, &result); err != nil {
		return result, err
	}
	return result, nil
}

func envelopeMessage(value envelope, err error) string {
	if strings.TrimSpace(value.Msg) != "" {
		return value.Msg
	}
	if strings.TrimSpace(value.Message) != "" {
		return value.Message
	}
	// Some upstream endpoints put failure details in a string-valued data field; objects and arrays are not error text.
	var dataMessage string
	if json.Unmarshal(value.Data, &dataMessage) == nil && strings.TrimSpace(dataMessage) != "" {
		return strings.TrimSpace(dataMessage)
	}
	if err != nil {
		return err.Error()
	}
	return "upstream request failed"
}

func submissionCode(code int, err error) string {
	if err != nil || code == 0 {
		return "submission_unknown"
	}
	return businessCode(code, "submission_failed")
}

func requestCode(code int, err error) string {
	if err != nil {
		return "protocol_error"
	}
	return businessCode(code, "request_failed")
}

func businessCode(code int, fallback string) string {
	switch code {
	case http.StatusUnauthorized, 10013, 10014, 10047, 10048:
		return "authentication_failed"
	case http.StatusPaymentRequired:
		return "insufficient_balance"
	case http.StatusForbidden:
		return "permission_denied"
	default:
		return fallback
	}
}

func withTask(err error, taskID string) error {
	var webErr *Error
	if errors.As(err, &webErr) {
		copy := *webErr
		copy.TaskID = taskID
		return &copy
	}
	return err
}

func validateDownloadURL(value, configured, taskID string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || strings.TrimSpace(value) != value || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("download URL is invalid")
	}
	trusted, err := url.Parse(configured)
	if err != nil || parsed.Scheme != trusted.Scheme || parsed.Host != trusted.Host || parsed.Path != trusted.Path {
		return nil, fmt.Errorf("download URL is outside the configured environment")
	}
	query := parsed.Query()
	for key := range query {
		if key != "task_id" && key != "type" && key != "api_key" {
			return nil, fmt.Errorf("download URL contains an unsupported query")
		}
	}
	if query.Get("task_id") != taskID || query.Get("type") != "json" {
		return nil, fmt.Errorf("download URL does not match the task")
	}
	return parsed, nil
}

func cloneNoRedirectClient(source *http.Client, timeout time.Duration) *http.Client {
	if source == nil {
		source = http.DefaultClient
	}
	clone := *source
	clone.Timeout = timeout
	clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func redactText(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[redacted]")
}
