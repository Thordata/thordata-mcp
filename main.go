package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
	"thordata-mcp/internal/auth"
	"thordata-mcp/internal/schema"
	"thordata-mcp/internal/serp"
	"thordata-mcp/tools"
)

const (
	serverName    = "thordata-mcp"
	serverVersion = "0.1.0"
)

type config struct {
	ListenAddr         string        `yaml:"listen_addr"`
	SchemaEndpoint     string        `yaml:"schema_endpoint"`
	SerpEndpoint       string        `yaml:"serp_endpoint"`
	HistoryEndpoint    string        `yaml:"history_endpoint"`
	StatisticsEndpoint string        `yaml:"statistics_endpoint"`
	SchemaTimeout      time.Duration `yaml:"-"`
	Timeout            time.Duration `yaml:"-"`
	ShutdownTimeout    time.Duration `yaml:"-"`
	SchemaTimeoutMS    int           `yaml:"schema_timeout_ms"`
	TimeoutMS          int           `yaml:"timeout_ms"`
	ShutdownTimeoutMS  int           `yaml:"shutdown_timeout_ms"`
}

func main() {
	cfg, err := loadConfig(configPath())
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: routes(mcpHandler(cfg))}
	log.Printf("%s listening on %s", serverName, cfg.ListenAddr)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	if err := runServer(context.Background(), srv, cfg.ShutdownTimeout, signals); err != nil {
		log.Fatal(err)
	}
	signal.Stop(signals)
}

func configPath() string {
	if path := os.Getenv("THORDATA_MCP_CONFIG"); path != "" {
		return path
	}
	return filepath.Join("configs", "config.yaml")
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8800"
	}
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 120000
	}
	if cfg.SchemaTimeoutMS <= 0 {
		cfg.SchemaTimeoutMS = 30000
	}
	if cfg.ShutdownTimeoutMS <= 0 {
		cfg.ShutdownTimeoutMS = 10000
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	cfg.SchemaTimeout = time.Duration(cfg.SchemaTimeoutMS) * time.Millisecond
	cfg.ShutdownTimeout = time.Duration(cfg.ShutdownTimeoutMS) * time.Millisecond
	return cfg, nil
}

func routes(mcpHandlers ...http.Handler) http.Handler {
	var mcpHandler http.Handler
	if len(mcpHandlers) > 0 {
		mcpHandler = mcpHandlers[0]
	}
	protected := auth.Middleware(mcpHandler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			handleRoot(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			handleHealth(w, r)
		case mcpHandler != nil && (r.URL.Path == "/mcp" || isCompatMCPPath(r.URL.Path)):
			protected.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func isCompatMCPPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 2 && parts[1] == "mcp" && parts[0] != ""
}

func mcpHandler(cfg config) http.Handler {
	cache := schema.NewCache(schema.NewClient(cfg.SchemaEndpoint, cfg.SchemaTimeout))
	toolSet := tools.New(cache, serp.NewClient(cfg.SerpEndpoint), cfg.HistoryEndpoint, cfg.StatisticsEndpoint, cfg.Timeout)
	s := server.NewMCPServer(serverName, serverVersion, server.WithToolCapabilities(true), server.WithResourceCapabilities(true, true), server.WithRecovery(), server.WithOutputSchemaValidation())
	s.AddTool(toolSet.ListEnginesTool(), toolSet.HandleListEngines)
	s.AddTool(toolSet.SearchTool(), toolSet.HandleSearch)
	s.AddTool(toolSet.HistoryTool(), toolSet.HandleHistory)
	s.AddTool(toolSet.StatisticsTool(), toolSet.HandleStatistics)
	index := mcp.NewResource("thordata://engines", "Thor SERP engines", mcp.WithMIMEType("application/json"))
	s.AddResource(index, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		doc, err := cache.ListDocument(ctx)
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(doc)
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "application/json", Text: string(body)}}, nil
	})
	template := mcp.NewResourceTemplate("thordata://engines/{engine}", "Thor engine schema", mcp.WithTemplateMIMEType("application/json"))
	s.AddResourceTemplate(template, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		const prefix = "thordata://engines/"
		if !strings.HasPrefix(req.Params.URI, prefix) {
			return nil, fmt.Errorf("invalid resource URI")
		}
		key := strings.TrimPrefix(req.Params.URI, prefix)
		doc, err := cache.EngineDocument(key)
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(doc)
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "application/json", Text: string(body)}}, nil
	})
	return server.NewStreamableHTTPServer(s, server.WithStateLess(true), server.WithDisableStreaming(true), server.WithHTTPContextFunc(mcpHTTPContext))
}

func mcpHTTPContext(ctx context.Context, r *http.Request) context.Context {
	if token, err := auth.ExtractToken(r); err == nil {
		return auth.WithToken(ctx, token)
	}
	if token, ok := auth.TokenFromContext(r.Context()); ok {
		return auth.WithToken(ctx, token)
	}
	return ctx
}

func runServer(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration, signals <-chan os.Signal) error {
	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()

	select {
	case err := <-serverErr:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-signals:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-serverErr; err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	}
}

func handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":      serverName,
		"version":   serverVersion,
		"transport": "streamable-http",
		"mcp_path":  "/mcp",
		"status":    "ok",
	})
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
