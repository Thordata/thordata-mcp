package webscraper

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientSubmitsBearerPollsWithSignAndDownloadsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/builder":
			if r.Header.Get("api-source") != "api" || r.Header.Get("platform") != "mcp" {
				t.Fatalf("builder legacy headers = api-source=%q platform=%q", r.Header.Get("api-source"), r.Header.Get("platform"))
			}
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("api_source") != "api" || r.Form.Get("source_platform") != "mcp" {
				t.Fatalf("builder source fields = api_source=%q source_platform=%q", r.Form.Get("api_source"), r.Form.Get("source_platform"))
			}
			var parameters []map[string]any
			if err := json.Unmarshal([]byte(r.Form.Get("spider_parameters")), &parameters); err != nil {
				t.Fatal(err)
			}
			if len(parameters) != 1 || parameters[0]["url"] != "https://linkedin.com/company/example?a=1&b=2" {
				t.Fatalf("spider_parameters = %#v", parameters)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"task_id": "task-1"}})
		case "/tasks-status":
			if r.Header.Get("api-source") != "api" || r.Header.Get("platform") != "mcp" {
				t.Fatalf("status legacy headers = api-source=%q platform=%q", r.Header.Get("api-source"), r.Header.Get("platform"))
			}
			if r.Header.Get("sign") != "token" {
				t.Fatalf("sign = %q", r.Header.Get("sign"))
			}
			_ = r.ParseForm()
			if r.Form.Get("api_source") != "api" || r.Form.Get("source_platform") != "mcp" {
				t.Fatalf("status source fields = api_source=%q source_platform=%q", r.Form.Get("api_source"), r.Form.Get("source_platform"))
			}
			if r.Form.Get("tasks_ids") != "task-1" {
				t.Fatalf("tasks_ids = %q", r.Form.Get("tasks_ids"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []any{map[string]any{"task_id": "task-1", "status": "Ready"}}})
		case "/tasks-download":
			if r.Header.Get("api-source") != "api" || r.Header.Get("platform") != "mcp" {
				t.Fatalf("download legacy headers = api-source=%q platform=%q", r.Header.Get("api-source"), r.Header.Get("platform"))
			}
			if r.Header.Get("sign") != "token" {
				t.Fatalf("download sign = %q", r.Header.Get("sign"))
			}
			_ = r.ParseForm()
			if r.Form.Get("api_source") != "api" || r.Form.Get("source_platform") != "mcp" {
				t.Fatalf("download source fields = api_source=%q source_platform=%q", r.Form.Get("api_source"), r.Form.Get("source_platform"))
			}
			if r.Form.Get("tasks_id") != "task-1" || r.Form.Get("type") != "json" {
				t.Fatalf("download form = %#v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"tasks_ids": "task-1", "download": "http://" + r.Host + "/download?task_id=task-1&type=json"}})
		case "/download":
			if r.Header.Get("Authorization") != "" || r.Header.Get("sign") != "" {
				t.Fatal("credential header was forwarded to download")
			}
			_, _ = w.Write([]byte(`[{"name":"Example","success":true}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(Endpoints{Builder: server.URL + "/builder", TaskAPI: server.URL, Download: server.URL + "/download"}, 2*time.Second)
	result, err := client.Fetch(context.Background(), "token", Request{
		SpiderID: "linkedin_company_information_by-url", SpiderName: "linkedin.com",
		Parameters: map[string]any{"url": "https://linkedin.com/company/example?a=1&b=2"}, MaxWait: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != "task-1" || result.Status != "completed" || result.RecordCount != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientReturnsWaitTimeoutWithTaskID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/builder":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"task_id": "task-timeout"}})
		case "/tasks-status":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []any{map[string]any{"task_id": "task-timeout", "status": "Running"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Endpoints{Builder: server.URL + "/builder", TaskAPI: server.URL, Download: server.URL + "/download"}, time.Second)
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	_, err := client.Fetch(context.Background(), "token", Request{SpiderID: "linkedin_company_information_by-url", SpiderName: "linkedin.com", Parameters: map[string]any{"url": "https://linkedin.com/company/example"}, MaxWait: 25 * time.Millisecond})
	var webErr *Error
	if !errors.As(err, &webErr) || webErr.Code != "wait_timeout" || webErr.TaskID != "task-timeout" || webErr.Stage != "poll" {
		t.Fatalf("error = %#v, want wait_timeout with task id", err)
	}
}

func TestClientRejectsCrossOriginDownloadURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/builder":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"task_id": "task-1"}})
		case "/tasks-status":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []any{map[string]any{"task_id": "task-1", "status": "Ready"}}})
		case "/tasks-download":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"tasks_ids": "task-1", "download": "https://evil.example/download?task_id=task-1&type=json"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Endpoints{Builder: server.URL + "/builder", TaskAPI: server.URL, Download: server.URL + "/download"}, time.Second)
	_, err := client.Fetch(context.Background(), "token", Request{SpiderID: "linkedin_company_information_by-url", SpiderName: "linkedin.com", Parameters: map[string]any{"url": "https://linkedin.com/company/example"}, MaxWait: time.Second})
	var webErr *Error
	if !errors.As(err, &webErr) || webErr.Code != "protocol_error" || webErr.Stage != "download_url" {
		t.Fatalf("error = %#v, want protocol_error", err)
	}
}

func TestClientUsesFormEncodingForSpecialValues(t *testing.T) {
	values := url.Values{"spider_parameters": {`[{"keyword":"a+b & c"}]`}}
	if strings.Contains(values.Encode(), "a+b & c") {
		t.Fatal("special form value was not encoded")
	}
}
