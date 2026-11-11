package webscraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	schemaTTL          = 5 * time.Minute
	defaultSchemaLimit = 5 * time.Second
	maxSchemaResponse  = 16 << 20
)

type ProductInfo struct {
	SpiderID string
	Domain   string
	Name     string
	Fields   []string
}

type Option struct {
	Label string `json:"label"`
	Value any    `json:"value"`
}

type Field struct {
	Key          string   `json:"key"`
	Label        string   `json:"label,omitempty"`
	Type         string   `json:"type"`
	Control      string   `json:"control"`
	Required     bool     `json:"required"`
	DefaultValue any      `json:"default_value"`
	Description  string   `json:"description,omitempty"`
	Options      []Option `json:"options,omitempty"`
}

type Warning struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

type Schema struct {
	SchemaVersion     string           `json:"schema_version"`
	Resource          string           `json:"resource"`
	SpiderID          string           `json:"spider_id"`
	SpiderName        string           `json:"spider_name"`
	ScraperID         int              `json:"scraper_id,omitempty"`
	ProductID         int              `json:"product_id,omitempty"`
	ProductType       string           `json:"product_type"`
	ExecutionPath     string           `json:"execution_path"`
	Parameters        []Field          `json:"parameters"`
	GeneralParameters []map[string]any `json:"general_parameters"`
	ValidationRules   []map[string]any `json:"validation_rules"`
	Warnings          []Warning        `json:"warnings"`
}

type schemaEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type operation struct {
	Value         string `json:"value"`
	Name          string `json:"name"`
	SpiderID      string `json:"spider_id"`
	SpiderName    string `json:"spider_name"`
	ScraperID     int    `json:"scraper_id"`
	ProductID     int    `json:"product_id"`
	ProductType   string `json:"product_type"`
	ExecutionPath string `json:"execution_path"`
}

var productOrder = []string{
	"linkedin_company_information_by-url",
	"linkedin_job_listings_information_by-job-url",
	"youtube_video-post_by-keyword",
	"facebook_profile_by-profiles-url",
	"youtube_video-post_by-explore",
	"twitter_profile_by-profileurl",
	"amazon_product_by-category-url",
}

var products = map[string]ProductInfo{
	"linkedin_company_information_by-url":          {SpiderID: "linkedin_company_information_by-url", Domain: "linkedin.com", Name: "LinkedIn Company Information Scraper: URL", Fields: []string{"url"}},
	"linkedin_job_listings_information_by-job-url": {SpiderID: "linkedin_job_listings_information_by-job-url", Domain: "linkedin.com", Name: "LinkedIn Job Listing Scraper: Job URL", Fields: []string{"job_url"}},
	"youtube_video-post_by-keyword":                {SpiderID: "youtube_video-post_by-keyword", Domain: "youtube.com", Name: "YouTube Video Post Scraper: Keyword", Fields: []string{"keyword", "num_of_posts"}},
	"facebook_profile_by-profiles-url":             {SpiderID: "facebook_profile_by-profiles-url", Domain: "facebook.com", Name: "Facebook Profile Scraper: Profile URL", Fields: []string{"url"}},
	"youtube_video-post_by-explore":                {SpiderID: "youtube_video-post_by-explore", Domain: "youtube.com", Name: "YouTube Video Post Scraper: Explore", Fields: []string{"url", "all_tabs"}},
	"twitter_profile_by-profileurl":                {SpiderID: "twitter_profile_by-profileurl", Domain: "x.com", Name: "Twitter(X) Profile Scraper: Profile URL", Fields: []string{"url"}},
	"amazon_product_by-category-url":               {SpiderID: "amazon_product_by-category-url", Domain: "amazon.com", Name: "Amazon Product Details Scraper: Category URL", Fields: []string{"url", "page_turning", "sort_by"}},
}

func AllowedSpiders() []ProductInfo {
	result := make([]ProductInfo, 0, len(productOrder))
	for _, id := range productOrder {
		result = append(result, cloneProduct(products[id]))
	}
	return result
}

func Product(id string) (ProductInfo, bool) {
	product, ok := products[strings.TrimSpace(id)]
	if !ok {
		return ProductInfo{}, false
	}
	return cloneProduct(product), true
}

func cloneProduct(product ProductInfo) ProductInfo {
	product.Fields = append([]string(nil), product.Fields...)
	return product
}

type SchemaClient struct {
	Endpoint   string
	HTTPClient *http.Client
	Timeout    time.Duration
}

func NewSchemaClient(endpoint string, timeout time.Duration) *SchemaClient {
	if timeout <= 0 {
		timeout = defaultSchemaLimit
	}
	return &SchemaClient{Endpoint: strings.TrimRight(endpoint, "/"), HTTPClient: &http.Client{Timeout: timeout}, Timeout: timeout}
}

func (c *SchemaClient) FetchOperations(ctx context.Context) ([]operation, error) {
	var envelope schemaEnvelope
	if err := c.get(ctx, "/operations", &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("web scraper schema API code %d: %s", envelope.Code, envelope.Msg)
	}
	var data struct {
		SchemaVersion string      `json:"schema_version"`
		Resource      string      `json:"resource"`
		Operations    []operation `json:"operations"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("decode web scraper operations: %w", err)
	}
	if data.SchemaVersion != "1" || data.Resource != "web_scraper" {
		return nil, fmt.Errorf("invalid web scraper operations schema")
	}
	return data.Operations, nil
}

func (c *SchemaClient) FetchSchema(ctx context.Context, id string) (Schema, error) {
	if _, ok := Product(id); !ok {
		return Schema{}, fmt.Errorf("product_unavailable: unsupported spider_id %q", id)
	}
	var envelope schemaEnvelope
	path := "/parameters?spider_id=" + url.QueryEscape(id)
	if err := c.get(ctx, path, &envelope); err != nil {
		return Schema{}, err
	}
	if envelope.Code != 0 {
		return Schema{}, fmt.Errorf("web scraper schema API code %d: %s", envelope.Code, envelope.Msg)
	}
	var schema Schema
	if err := json.Unmarshal(envelope.Data, &schema); err != nil {
		return Schema{}, fmt.Errorf("decode web scraper parameters: %w", err)
	}
	if err := validateSchema(schema, id); err != nil {
		return Schema{}, err
	}
	return schema, nil
}

func (c *SchemaClient) get(ctx context.Context, path string, target any) error {
	if c == nil || c.Endpoint == "" {
		return fmt.Errorf("web scraper schema endpoint is empty")
	}
	requestCtx := ctx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, c.Endpoint+path, nil)
	if err != nil {
		return fmt.Errorf("create web scraper schema request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultSchemaLimit}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("fetch web scraper schema: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("web scraper schema endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSchemaResponse+1))
	if err != nil {
		return fmt.Errorf("read web scraper schema response: %w", err)
	}
	if len(body) > maxSchemaResponse {
		return fmt.Errorf("web scraper schema response exceeds %d bytes", maxSchemaResponse)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode web scraper schema response: %w", err)
	}
	return nil
}

func validateOperations(operations []operation) (map[string]bool, error) {
	available := make(map[string]bool, len(productOrder))
	for _, item := range operations {
		product, ok := Product(item.SpiderID)
		if !ok || item.Value != item.SpiderID || item.SpiderName != product.Domain || item.ProductType != "text" || item.ExecutionPath != "/builder" {
			continue
		}
		if strings.TrimSpace(item.Name) == "" {
			return nil, fmt.Errorf("invalid web scraper operation %q", item.SpiderID)
		}
		available[item.SpiderID] = true
	}
	return available, nil
}

func validateSchema(schema Schema, id string) error {
	product, ok := Product(id)
	if !ok {
		return fmt.Errorf("product_unavailable: unsupported spider_id %q", id)
	}
	if schema.SchemaVersion != "1" || schema.Resource != "web_scraper" || schema.SpiderID != id || schema.SpiderName != product.Domain || schema.ProductType != "text" || schema.ExecutionPath != "/builder" {
		return fmt.Errorf("schema_unavailable: invalid schema metadata for %q", id)
	}
	if len(schema.GeneralParameters) != 0 || len(schema.ValidationRules) != 0 {
		return fmt.Errorf("schema_unavailable: unsupported general parameters or validation rules for %q", id)
	}
	allowed := make(map[string]bool, len(product.Fields))
	for _, key := range product.Fields {
		allowed[key] = true
	}
	seen := make(map[string]bool, len(schema.Parameters))
	for i := range schema.Parameters {
		field := &schema.Parameters[i]
		if field.Key == "" || !allowed[field.Key] || seen[field.Key] {
			return fmt.Errorf("schema_unavailable: invalid or duplicate field %q", field.Key)
		}
		seen[field.Key] = true
		if strings.TrimSpace(field.Label) == "" {
			field.Label = field.Key
		}
		expectedType, expectedControl := expectedFieldType(field.Key)
		if field.Type != expectedType || field.Control != expectedControl {
			return fmt.Errorf("schema_unavailable: unsupported field type for %q", field.Key)
		}
		expectedRequired := field.Key != "num_of_posts" && field.Key != "all_tabs" && field.Key != "sort_by"
		if field.Required != expectedRequired {
			return fmt.Errorf("schema_unavailable: invalid required flag for %q", field.Key)
		}
		if field.Control == "select" && len(field.Options) == 0 {
			return fmt.Errorf("schema_unavailable: options missing for %q", field.Key)
		}
		seenOptions := map[string]bool{}
		for _, option := range field.Options {
			key, err := scalarKey(option.Value)
			if err != nil || strings.TrimSpace(option.Label) == "" || seenOptions[key] {
				return fmt.Errorf("schema_unavailable: invalid option for %q", field.Key)
			}
			seenOptions[key] = true
		}
		if field.DefaultValue != nil {
			if field.Key == "num_of_posts" {
				if _, ok := positiveInteger(field.DefaultValue); !ok {
					field.DefaultValue = nil
				}
			} else if _, err := normalizeFieldValue(*field, field.DefaultValue); err != nil {
				return fmt.Errorf("schema_unavailable: invalid default for %q", field.Key)
			}
		}
	}
	for _, key := range product.Fields {
		if !seen[key] {
			return fmt.Errorf("schema_unavailable: field %q is missing", key)
		}
	}
	return nil
}

func expectedFieldType(key string) (string, string) {
	switch key {
	case "page_turning":
		return "number", "number"
	case "all_tabs", "sort_by":
		return "options", "select"
	default:
		return "string", "input"
	}
}

func ValidateParameters(schema Schema, input map[string]any) (map[string]any, error) {
	if _, ok := Product(schema.SpiderID); !ok {
		return nil, fmt.Errorf("invalid_parameters: unsupported spider_id %q", schema.SpiderID)
	}
	fields := make(map[string]Field, len(schema.Parameters))
	for _, field := range schema.Parameters {
		fields[field.Key] = field
	}
	for key := range input {
		if key == "__proto__" || key == "constructor" || key == "prototype" || fields[key].Key == "" {
			return nil, fmt.Errorf("invalid_parameters: unknown field %q", key)
		}
	}
	result := make(map[string]any, len(schema.Parameters))
	for _, field := range schema.Parameters {
		value, exists := input[field.Key]
		if !exists {
			value = field.DefaultValue
		}
		if value == nil || (field.Type == "string" && strings.TrimSpace(fmt.Sprint(value)) == "") {
			if field.Required {
				return nil, fmt.Errorf("invalid_parameters: %s is required", field.Key)
			}
			continue
		}
		normalized, err := normalizeFieldValue(field, value)
		if err != nil {
			return nil, fmt.Errorf("invalid_parameters: %s: %w", field.Key, err)
		}
		result[field.Key] = normalized
	}
	return result, nil
}

func normalizeFieldValue(field Field, value any) (any, error) {
	switch field.Type {
	case "string":
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" || text != strings.TrimSpace(text) {
			return nil, fmt.Errorf("must be a non-empty string")
		}
		if field.Key == "url" || field.Key == "job_url" {
			parsed, err := url.Parse(text)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
				return nil, fmt.Errorf("must be a public HTTP or HTTPS URL")
			}
		}
		if field.Key == "num_of_posts" {
			if n, ok := positiveInteger(text); !ok {
				return nil, fmt.Errorf("must be a positive integer")
			} else {
				return strconv.Itoa(n), nil
			}
		}
		return text, nil
	case "number":
		n, ok := positiveInteger(value)
		if !ok {
			return nil, fmt.Errorf("must be a positive integer")
		}
		return n, nil
	case "options":
		text := fmt.Sprint(value)
		for _, option := range field.Options {
			if fmt.Sprint(option.Value) == text {
				return text, nil
			}
		}
		return nil, fmt.Errorf("must match one of the schema options")
	default:
		return nil, fmt.Errorf("unsupported field type %q", field.Type)
	}
}

func positiveInteger(value any) (int, bool) {
	var text string
	switch x := value.(type) {
	case json.Number:
		text = x.String()
	case string:
		text = strings.TrimSpace(x)
	case int:
		return x, x > 0
	case int8:
		return int(x), x > 0
	case int16:
		return int(x), x > 0
	case int32:
		return int(x), x > 0
	case int64:
		return int(x), x > 0 && int64(int(x)) == x
	case uint:
		return int(x), x > 0 && uint(int(x)) == x
	case uint64:
		return int(x), x > 0 && uint64(int(x)) == x
	case float64:
		return int(x), x > 0 && x == float64(int(x))
	default:
		return 0, false
	}
	n, err := strconv.Atoi(text)
	return n, err == nil && n > 0
}

func scalarKey(value any) (string, error) {
	if value == nil {
		return "", fmt.Errorf("option value is nil")
	}
	return fmt.Sprintf("%T:%v", value, value), nil
}

type cacheEntry struct {
	value    Schema
	loadedAt time.Time
}

type schemaFlight struct {
	done  chan struct{}
	value Schema
	err   error
}

type Cache struct {
	client    *SchemaClient
	now       func() time.Time
	mu        sync.Mutex
	values    map[string]cacheEntry
	flights   map[string]*schemaFlight
	catalog   map[string]bool
	catalogAt time.Time
}

func NewCache(client *SchemaClient) *Cache {
	return &Cache{client: client, now: time.Now, values: make(map[string]cacheEntry), flights: make(map[string]*schemaFlight)}
}

func (c *Cache) Get(ctx context.Context, id string) (Schema, error) {
	if _, ok := Product(id); !ok {
		return Schema{}, fmt.Errorf("product_unavailable: unsupported spider_id %q", id)
	}
	if err := c.refreshCatalog(ctx, false); err != nil {
		return Schema{}, err
	}
	c.mu.Lock()
	if !c.catalog[id] {
		c.mu.Unlock()
		return Schema{}, fmt.Errorf("product_unavailable: %s is not available", id)
	}
	if entry, ok := c.values[id]; ok && c.now().Sub(entry.loadedAt) < schemaTTL {
		value := entry.value
		c.mu.Unlock()
		return value, nil
	}
	if flight, ok := c.flights[id]; ok {
		c.mu.Unlock()
		select {
		case <-flight.done:
			return flight.value, flight.err
		case <-ctx.Done():
			return Schema{}, ctx.Err()
		}
	}
	flight := &schemaFlight{done: make(chan struct{})}
	c.flights[id] = flight
	c.mu.Unlock()
	go c.refreshSchema(id, flight)
	select {
	case <-flight.done:
		return flight.value, flight.err
	case <-ctx.Done():
		return Schema{}, ctx.Err()
	}
}

func (c *Cache) List(ctx context.Context) ([]Schema, error) {
	if err := c.refreshCatalog(ctx, false); err != nil {
		return nil, err
	}
	result := make([]Schema, 0, len(productOrder))
	for _, id := range productOrder {
		c.mu.Lock()
		available := c.catalog[id]
		c.mu.Unlock()
		if !available {
			continue
		}
		schema, err := c.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, schema)
	}
	return result, nil
}

func (c *Cache) refreshCatalog(ctx context.Context, force bool) error {
	c.mu.Lock()
	if !force && c.catalog != nil && c.now().Sub(c.catalogAt) < schemaTTL {
		c.mu.Unlock()
		return nil
	}
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return fmt.Errorf("schema_unavailable: web scraper schema client is nil")
	}
	operations, err := client.FetchOperations(ctx)
	if err != nil {
		c.mu.Lock()
		hasCatalog := c.catalog != nil
		c.mu.Unlock()
		if hasCatalog {
			return nil
		}
		return fmt.Errorf("schema_unavailable: %w", err)
	}
	available, err := validateOperations(operations)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.catalog = available
	c.catalogAt = c.now()
	for id := range c.values {
		if !available[id] {
			delete(c.values, id)
		}
	}
	c.mu.Unlock()
	return nil
}

func (c *Cache) refreshSchema(id string, flight *schemaFlight) {
	value, err := c.client.FetchSchema(context.Background(), id)
	c.mu.Lock()
	if err == nil {
		c.values[id] = cacheEntry{value: value, loadedAt: c.now()}
		flight.value = value
	} else if previous, ok := c.values[id]; ok {
		flight.value = previous.value
		err = nil
	}
	flight.err = err
	delete(c.flights, id)
	close(flight.done)
	c.mu.Unlock()
}
