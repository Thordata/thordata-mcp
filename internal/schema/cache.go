package schema

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const cacheTTL = 5 * time.Minute

type refreshFlight struct {
	done   chan struct{}
	schema Schema
	err    error
}

type Cache struct {
	client   *Client
	now      func() time.Time
	mu       sync.Mutex
	value    Schema
	loadedAt time.Time
	hasValue bool
	inFlight *refreshFlight
}

func NewCache(client *Client) *Cache { return &Cache{client: client, now: time.Now} }
func (c *Cache) Current(ctxs ...context.Context) (Schema, error) {
	return c.current(false, false, ctxs...)
}
func (c *Cache) Get(ctxs ...context.Context) (Schema, error) { return c.current(false, true, ctxs...) }
func (c *Cache) Refresh(ctxs ...context.Context) (Schema, error) {
	return c.current(true, true, ctxs...)
}

func (c *Cache) current(force, reportErr bool, ctxs ...context.Context) (Schema, error) {
	ctx := context.Background()
	if len(ctxs) > 0 && ctxs[0] != nil {
		ctx = ctxs[0]
	}
	c.mu.Lock()
	if !force && c.hasValue && c.now().Sub(c.loadedAt) < cacheTTL {
		s := cloneSchema(c.value)
		c.mu.Unlock()
		return s, nil
	}
	if c.inFlight != nil {
		wait := c.inFlight
		c.mu.Unlock()
		return c.await(wait, ctx, reportErr)
	}
	wait := &refreshFlight{done: make(chan struct{})}
	c.inFlight = wait
	c.mu.Unlock()
	go c.refresh(wait)
	return c.await(wait, ctx, reportErr)
}

func (c *Cache) refresh(wait *refreshFlight) {
	var s Schema
	var err error
	if c.client == nil {
		err = fmt.Errorf("schema client is nil")
	} else {
		s, err = c.client.Fetch(context.Background())
	}
	c.mu.Lock()
	if err == nil {
		c.value, c.loadedAt, c.hasValue = cloneSchema(s), c.now(), true
		wait.schema = cloneSchema(s)
	} else {
		wait.err = err
	}
	c.inFlight = nil
	close(wait.done)
	c.mu.Unlock()
}

func (c *Cache) await(wait *refreshFlight, ctx context.Context, reportErr bool) (Schema, error) {
	select {
	case <-wait.done:
		if wait.err != nil && reportErr {
			return c.fallbackWithError(wait.err)
		}
		if wait.err != nil {
			return c.fallback(wait.err)
		}
		return cloneSchema(wait.schema), nil
	case <-ctx.Done():
		return Schema{}, ctx.Err()
	}
}

func (c *Cache) fallbackWithError(fetchErr error) (Schema, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasValue {
		return cloneSchema(c.value), fetchErr
	}
	if validate(snapshotSchema) == nil {
		return cloneSchema(snapshotSchema), fetchErr
	}
	return Schema{}, fetchErr
}
func (c *Cache) fallback(fetchErr error) (Schema, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasValue {
		return cloneSchema(c.value), nil
	}
	if snapErr := validate(snapshotSchema); snapErr == nil {
		return cloneSchema(snapshotSchema), nil
	} else {
		return Schema{}, fmt.Errorf("schema unavailable: %v (snapshot invalid: %w)", fetchErr, snapErr)
	}
}

func (c *Cache) ListDocument(ctxs ...context.Context) (map[string]any, error) {
	s, err := c.Current(ctxs...)
	if err != nil {
		return nil, err
	}
	engines := s.AllEngines()
	docs := make([]map[string]any, 0, len(engines))
	for _, e := range engines {
		docs = append(docs, map[string]any{"key": e.Key, "name": e.Name, "query_field": e.QueryField, "resource_uri": enginesURI + "/" + e.Key})
	}
	return map[string]any{"schema_version": s.SchemaVersion, "audience": s.Audience, "default_engine": s.DefaultEngine, "engines": docs, "resource_uri": enginesURI}, nil
}
func (c *Cache) EngineDocument(key string, ctxs ...context.Context) (map[string]any, error) {
	s, err := c.Current(ctxs...)
	if err != nil {
		return nil, err
	}
	for _, e := range s.AllEngines() {
		if e.Key == key {
			return map[string]any{"key": e.Key, "name": e.Name, "query_field": e.QueryField, "groups": cloneGroups(e.Groups), "resource_uri": enginesURI + "/" + e.Key}, nil
		}
	}
	return nil, fmt.Errorf("unknown engine %q; read %s", key, enginesURI)
}
