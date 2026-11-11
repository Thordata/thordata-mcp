package webscraper

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseResultMarksMixedRecordsPartialAndRedactsSecrets(t *testing.T) {
	result, err := ParseResult([]byte(`[{"name":"ok","success":true},{"success":false,"error":"bad secret-token","error_code":"400"}]`), "task-1", "linkedin_company_information_by-url", []string{"url"}, []string{"secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partial" || result.ErrorRecordCount != 1 || result.RecordCount != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Contains(string(mustJSONResult(t, result.Result)), "secret-token") {
		t.Fatal("secret leaked in result")
	}
}

func TestParseResultClassifiesEmptyAndAllErrors(t *testing.T) {
	empty, err := ParseResult([]byte(`[]`), "task-empty", "youtube_video-post_by-keyword", nil, nil)
	if err != nil || empty.Status != "empty" || empty.RecordCount != 0 {
		t.Fatalf("empty result = %#v, err=%v", empty, err)
	}
	_, err = ParseResult([]byte(`[{"success":false,"error":"failed"}]`), "task-failed", "youtube_video-post_by-keyword", nil, nil)
	var webErr *Error
	if !errors.As(err, &webErr) || webErr.Code != "scrape_failed" || webErr.TaskID != "task-failed" {
		t.Fatalf("all-error result error = %#v", err)
	}
}

func TestParseResultAcceptsDoubleEncodedObject(t *testing.T) {
	result, err := ParseResult([]byte(`"{\"name\":\"Example\"}"`), "task-1", "facebook_profile_by-profiles-url", nil, nil)
	if err != nil || result.Status != "completed" || result.RecordCount != 1 {
		t.Fatalf("double encoded result = %#v, err=%v", result, err)
	}
}

func mustJSONResult(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
