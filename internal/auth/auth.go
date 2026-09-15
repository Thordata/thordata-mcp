package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

type contextKey struct{}

// WithToken stores the credential in request context only.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, contextKey{}, token)
}

func TokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(contextKey{}).(string)
	return token, ok && token != ""
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

// IsLikelyToken 根据凭据形态区分 token 与平台名称。
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
	// 保留既有的 path-token 形式，同时拒绝普通平台路径。
	return len(value) >= 10 && strings.Contains(value, "-")
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := ExtractToken(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithToken(r.Context(), token)))
	})
}
