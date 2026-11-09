package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

type serpTokenKey struct{}
type datasetKeyKey struct{}

// WithToken stores the SERP credential in request context only.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, serpTokenKey{}, token)
}

func TokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(serpTokenKey{}).(string)
	return token, ok && token != ""
}

// WithDatasetKey stores the ac_api_keys credential used by dataset tools.
func WithDatasetKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, datasetKeyKey{}, key)
}

func DatasetKeyFromContext(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(datasetKeyKey{}).(string)
	return key, ok && key != ""
}

func ExtractToken(r *http.Request) (string, error) {
	if r == nil {
		return "", fmt.Errorf("request is nil")
	}
	for key := range r.URL.Query() {
		if strings.EqualFold(key, "token") {
			return "", fmt.Errorf("token query parameter is not allowed")
		}
	}
	if value := r.Header.Get("Authorization"); value != "" {
		parts := strings.Fields(value)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			return "", fmt.Errorf("malformed Authorization header")
		}
		return parts[1], nil
	}
	if value := r.Header.Get("X-Thordata-Serp-Token"); value != "" {
		if strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n") {
			return "", fmt.Errorf("malformed X-Thordata-Serp-Token header")
		}
		return value, nil
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 2 && parts[1] == "mcp" && IsLikelyToken(parts[0]) {
		return parts[0], nil
	}
	return "", fmt.Errorf("missing SERP token")
}

// ExtractDatasetKey reads the dataset credential. API Keys and SERP Tokens are
// unified, so the same token from X-API-Key, Bearer, or the path is accepted.
func ExtractDatasetKey(r *http.Request) (string, error) {
	if r == nil {
		return "", fmt.Errorf("request is nil")
	}
	for _, header := range []string{"X-API-Key", "X-Thordata-Dataset-Key"} {
		if value := r.Header.Get(header); value != "" {
			value = strings.TrimSpace(value)
			if value == "" || strings.ContainsAny(value, " \t\r\n") {
				return "", fmt.Errorf("malformed dataset API key header")
			}
			return value, nil
		}
	}
	// Unified token: the SERP token/path token is also the dataset API key.
	if token, err := ExtractToken(r); err == nil {
		return token, nil
	}
	return "", fmt.Errorf("missing dataset API key")
}

// ExtractCredentials returns both credentials independently. At least one is
// required; SERP-only and dataset-only clients are both valid.
func ExtractCredentials(r *http.Request) (serpToken, datasetKey string, err error) {
	datasetKey, _ = ExtractDatasetKey(r)
	serpToken, serpErr := ExtractToken(r)
	if datasetKey == "" && serpErr != nil {
		return "", "", serpErr
	}
	if serpErr != nil && datasetKey != "" {
		serpToken = ""
	}
	return serpToken, datasetKey, nil
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serpToken, datasetKey, err := ExtractCredentials(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		ctx := r.Context()
		if serpToken != "" {
			ctx = WithToken(ctx, serpToken)
		}
		if datasetKey != "" {
			ctx = WithDatasetKey(ctx, datasetKey)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// IsLikelyToken keeps the existing path-token compatibility check.
func IsLikelyToken(value string) bool {
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	for _, prefix := range []string{"sk_", "api_", "tok_", "key_", "auth_"} {
		if strings.HasPrefix(value, prefix) {
			return len(value) > len(prefix)
		}
	}
	if len(value) >= 24 {
		var letter, digit bool
		for _, ch := range value {
			letter = letter || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
			digit = digit || (ch >= '0' && ch <= '9')
		}
		return letter && digit
	}
	return len(value) >= 10 && strings.Contains(value, "-")
}
