# Self-hosting (developer notes)

This document is for people working on the MCP service itself. End users do not need it: they connect to the hosted endpoint described in [../README.md](../README.md).

## Requirements

- Go installed locally.
- A Thordata SERP API key (passed per request; not stored in config).

## Run locally

```text
git clone https://github.com/Thordata/thordata-mcp.git
cd thordata-mcp
go mod download
go run .
```

By default, the service loads `configs/config.yaml`. Set `THORDATA_MCP_CONFIG` to use another configuration file.

A production configuration looks like:

```yaml
listen_addr: ":8800"
schema_endpoint: "https://api.thordata.com/serp/playground/schema?lang=en"
serp_endpoint: "https://scraperapi.thordata.com/request"
history_endpoint: "https://api.thordata.com/mcp/serp/history"
statistics_endpoint: "https://api.thordata.com/mcp/serp/statistics"
schema_timeout_ms: 30000
timeout_ms: 120000
shutdown_timeout_ms: 10000
```

After startup, the service exposes:

- `GET /` — server name, version, transport, MCP path, and status.
- `GET /healthz` — returns `{"ok":true}`.
- `POST | GET | DELETE /mcp` — used with an Authorization or token header.
- `POST | GET | DELETE /<user-api-key>/mcp` — path-token form for clients that cannot send headers.

For local self-testing, point your MCP client at `http://127.0.0.1:8800/mcp` (or `http://127.0.0.1:8800/<your-key>/mcp` for the path-token form).

## Configuration fields

| Field | Description |
| --- | --- |
| `listen_addr` | HTTP listen address for the MCP service. |
| `schema_endpoint` | Endpoint returning the current engine and field schema. |
| `serp_endpoint` | Thordata SERP search endpoint. |
| `history_endpoint` | Endpoint for the authenticated user's SERP history. |
| `statistics_endpoint` | Endpoint for the authenticated user's SERP statistics. |
| `schema_timeout_ms` | Timeout for loading the remote schema. Defaults to 30000 ms. |
| `timeout_ms` | Timeout for SERP, history, and statistics upstream requests. Defaults to 120000 ms. |
| `shutdown_timeout_ms` | Graceful HTTP server shutdown timeout. Defaults to 10000 ms. |

The YAML decoder rejects unknown fields. Non-positive timeout values use the built-in defaults.

## Development checks

```text
go test ./...
go vet ./...
go build ./...
```

The generated executable is ignored by Git. Tests use local HTTP test servers and do not require a production API key.

## How the service works

- Loads the current engine and parameter definitions from the schema endpoint, caching a valid schema for five minutes and sharing concurrent refreshes.
- Falls back to a bundled engine snapshot when no remote or cached schema is available.
- Forwards each user's API key per request instead of storing a shared customer credential.
- The service is designed for stateless MCP Streamable HTTP and does not persist a shared user API key.
