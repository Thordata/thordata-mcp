package webscraper

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func ParseResult(body []byte, taskID, spiderID string, inputKeys, secrets []string) (Result, error) {
	value, err := parseJSONValue(body)
	if err != nil {
		return Result{}, &Error{Code: "protocol_error", Stage: "parse", SpiderID: spiderID, TaskID: taskID, Message: "download body is not valid JSON"}
	}
	if object, ok := value.(map[string]any); ok {
		if _, hasCode := object["code"]; hasCode {
			code := numericCode(object["code"])
			if code != 0 && code != 200 {
				message := fmt.Sprint(firstValue(object, "msg", "message", "error"))
				for _, secret := range secrets {
					if secret != "" {
						message = strings.ReplaceAll(message, secret, "[redacted]")
					}
				}
				return Result{}, &Error{Code: "request_failed", Stage: "parse", SpiderID: spiderID, TaskID: taskID, Message: message}
			}
			if data, ok := object["data"]; ok {
				value = data
			}
		}
	}
	value, err = unwrapValue(value)
	if err != nil {
		return Result{}, &Error{Code: "protocol_error", Stage: "parse", SpiderID: spiderID, TaskID: taskID, Message: err.Error()}
	}
	records, err := recordsFromValue(value)
	if err != nil {
		return Result{}, &Error{Code: "protocol_error", Stage: "parse", SpiderID: spiderID, TaskID: taskID, Message: err.Error()}
	}
	redacted := make([]map[string]any, len(records))
	for i, record := range records {
		redacted[i] = redactMap(record, secrets)
	}
	errorCount := 0
	meaningful := false
	var firstError string
	for _, record := range redacted {
		if recordHasError(record) {
			errorCount++
			if firstError == "" {
				firstError = recordErrorText(record)
			}
		}
		if recordHasMeaningfulValue(record, inputKeys) {
			meaningful = true
		}
	}
	if len(redacted) > 0 && errorCount == len(redacted) {
		return Result{}, &Error{Code: "scrape_failed", Stage: "parse", SpiderID: spiderID, TaskID: taskID, Message: firstError}
	}
	status := "completed"
	if len(redacted) == 0 || !meaningful {
		status = "empty"
	} else if errorCount > 0 {
		status = "partial"
	}
	return Result{SpiderID: spiderID, TaskID: taskID, Status: status, RecordCount: len(redacted), ErrorRecordCount: errorCount, Result: redacted}, nil
}

func parseJSONValue(body []byte) (any, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return unwrapValue(value)
}

func unwrapValue(value any) (any, error) {
	for i := 0; i < 2; i++ {
		text, ok := value.(string)
		if !ok {
			return value, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded); err != nil {
			return nil, err
		}
		value = decoded
	}
	return value, nil
}

func recordsFromValue(value any) ([]map[string]any, error) {
	switch typed := value.(type) {
	case []any:
		result := make([]map[string]any, len(typed))
		for i, item := range typed {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("result record %d is not an object", i)
			}
			result[i] = object
		}
		return result, nil
	case map[string]any:
		return []map[string]any{typed}, nil
	default:
		return nil, fmt.Errorf("result must be an object or array of objects")
	}
}

func numericCode(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		var code int
		_, _ = fmt.Sscanf(typed, "%d", &code)
		return code
	default:
		return 0
	}
}

func firstValue(object map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := object[key]; ok && value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
			return value
		}
	}
	return "upstream result request failed"
}

func recordHasError(record map[string]any) bool {
	if success, ok := record["success"].(bool); ok && !success {
		return true
	}
	if value, ok := record["error"]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" && fmt.Sprint(value) != "<nil>" {
		return true
	}
	if value, ok := record["error_code"]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		return text != "" && text != "<nil>" && text != "0" && text != "200"
	}
	return false
}

func recordErrorText(record map[string]any) string {
	for _, key := range []string{"error", "error_code"} {
		if value, ok := record[key]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return "upstream task returned only error records"
}

func recordHasMeaningfulValue(record map[string]any, inputKeys []string) bool {
	ignored := map[string]bool{"input": true, "input_url": true, "url": true, "job_url": true, "keyword": true, "success": true, "error": true, "error_code": true}
	for _, key := range inputKeys {
		ignored[key] = true
	}
	for key, value := range record {
		if !ignored[key] && meaningfulValue(value) {
			return true
		}
	}
	return false
}

func meaningfulValue(value any) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.String:
		return strings.TrimSpace(rv.String()) != ""
	case reflect.Array, reflect.Slice, reflect.Map:
		return rv.Len() > 0
	case reflect.Bool:
		return rv.Bool()
	default:
		return true
	}
}

func redactMap(value map[string]any, secrets []string) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = redactValue(item, secrets)
	}
	return result
}

func redactValue(value any, secrets []string) any {
	switch typed := value.(type) {
	case string:
		result := typed
		for _, secret := range secrets {
			if secret != "" {
				result = strings.ReplaceAll(result, secret, "[redacted]")
			}
		}
		return result
	case map[string]any:
		return redactMap(typed, secrets)
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = redactValue(item, secrets)
		}
		return result
	default:
		return value
	}
}
