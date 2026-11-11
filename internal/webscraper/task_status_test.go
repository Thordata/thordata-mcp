package webscraper

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRejectsInvalidSubmittedTaskID(t *testing.T) {
	for _, id := range []string{"", "invalid task", "task/invalid"} {
		t.Run(id, func(t *testing.T) {
			var submissions, polls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/builder" {
					submissions.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"task_id": id}})
					return
				}
				polls.Add(1)
				http.NotFound(w, r)
			}))
			defer upstream.Close()
			client := NewClient(Endpoints{Builder: upstream.URL + "/builder", TaskAPI: upstream.URL}, time.Second)
			_, err := client.Fetch(context.Background(), "fixture-key", Request{SpiderID: "linkedin_company_information_by-url", MaxWait: time.Second})
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != "submission_unknown" || failure.Stage != "submit" || failure.TaskID != "" {
				t.Fatalf("error=%#v", err)
			}
			if submissions.Load() != 1 || polls.Load() != 0 {
				t.Fatalf("submissions=%d polls=%d", submissions.Load(), polls.Load())
			}
		})
	}
}

func TestClientRejectsMismatchedStatusTaskID(t *testing.T) {
	var submissions, polls, downloads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/builder":
			submissions.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"task_id": "submitted-task"}})
		case "/tasks-status":
			polls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.PostForm.Get("tasks_ids") != "submitted-task" {
				t.Errorf("queried task=%q", r.PostForm.Get("tasks_ids"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []any{map[string]any{"task_id": "other-task", "status": "Running"}}})
		default:
			downloads.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	client := NewClient(Endpoints{Builder: upstream.URL + "/builder", TaskAPI: upstream.URL}, time.Second)
	_, err := client.Fetch(context.Background(), "fixture-key", Request{SpiderID: "linkedin_company_information_by-url", MaxWait: time.Second})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "protocol_error" || failure.Stage != "poll" || failure.TaskID != "submitted-task" {
		t.Fatalf("error=%#v", err)
	}
	if submissions.Load() != 1 || polls.Load() != 1 || downloads.Load() != 0 {
		t.Fatalf("submissions=%d polls=%d downloads=%d", submissions.Load(), polls.Load(), downloads.Load())
	}
}

func TestClientUnknownTaskRunningStopsWithoutResubmission(t *testing.T) {
	const unknownID = "unknown-task"
	var submissions, polls, downloads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/builder":
			submissions.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"task_id": unknownID}})
		case "/tasks-status":
			polls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []any{map[string]any{"task_id": unknownID, "status": "Running"}}})
		default:
			downloads.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	client := NewClient(Endpoints{Builder: upstream.URL + "/builder", TaskAPI: upstream.URL}, time.Second)
	start := time.Now()
	_, err := client.Fetch(context.Background(), "fixture-key", Request{SpiderID: "linkedin_company_information_by-url", MaxWait: 250 * time.Millisecond})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "wait_timeout" || failure.Stage != "poll" || failure.TaskID != unknownID {
		t.Fatalf("error=%#v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("bounded wait exceeded: %s", elapsed)
	}
	if submissions.Load() != 1 || polls.Load() == 0 || downloads.Load() != 0 {
		t.Fatalf("submissions=%d polls=%d downloads=%d", submissions.Load(), polls.Load(), downloads.Load())
	}
}
