# Thordata MCP Server

Connect [Thordata](https://www.thordata.com/serp-api/mcp?utm_source=MCP&utm_term=mcp) SERP to MCP-compatible AI agents and clients for real-time search, usage history, and statistics.

The Thordata SERP MCP Server exposes Thordata search capabilities through the Model Context Protocol (MCP), making it easy to integrate live search tools into agents, workflows, and MCP clients. In addition to real-time SERP requests, it also provides access to search history, usage statistics, and engine schema resources for richer integrations.

## Quick start

Most users connect to the **hosted Thordata MCP endpoint** — no installation, no local build. You only need a Thordata SERP API key.

### 1. Get your API key

Sign up at [Thordata](https://www.thordata.com/serp-api/mcp?utm_source=MCP&utm_term=mcp) and get your SERP API key from the dashboard.

### 2. Connect your MCP client

Point your MCP client at the hosted endpoint and put your API key in the URL path. Your key is used per request; Thordata never stores a shared credential on your behalf.

The default connection URL is:

```text
https://mcp.thordata.com/YOUR_API_KEY/mcp
```

Replace `YOUR_API_KEY` with your Thordata SERP API key and use this MCP client configuration:

```json
{
  "mcpServers": {
    "thordata": {
      "url": "https://mcp.thordata.com/YOUR_API_KEY/mcp"
    }
  }
}
```

If your client supports custom headers, you can instead use the Bearer configuration:

```json
{
  "mcpServers": {
    "thordata": {
      "url": "https://mcp.thordata.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_API_KEY"
      }
    }
  }
}
```

For a Claude Code one-line install, run:

```text
claude mcp add --transport http thordata https://mcp.thordata.com/YOUR_API_KEY/mcp
```

### 3. Make your first request

A typical call flow is:

1. Read the `thordata://engines` resource to list supported engines.
2. Read `thordata://engines/<engine-key>` for the engine you want.
3. Build `params` from that engine's schema.
4. Call the `search` tool.

## What you can do

- **`search`** — run a real-time SERP search on any supported engine.
- **`list_engines`** — see the current engine list, query field, and schema resources.
- **`history`** — query SERP request history for your own account.
- **`statistics`** — query SERP usage statistics for your own account.

History and statistics are scoped to the API key you send; you cannot query another user's data.

## Authentication

Supported token delivery methods:

- Default: `https://mcp.thordata.com/YOUR_API_KEY/mcp` (token in the path).
- Optional: `Authorization: Bearer YOUR_API_KEY` with `https://mcp.thordata.com/mcp`.
- Compatible: `X-Thordata-Serp-Token: YOUR_API_KEY`.

Notes:

- The hosted path-token URL is the default connection method.
- The Bearer form is available for clients that support custom headers.
- A path token can appear in access logs and referrers, so the hosted endpoint always uses HTTPS.
- Query-string tokens are not accepted.
- Your API key is forwarded per request and is never included in MCP result metadata; it is redacted from returned data and error text.
- Your engine availability and access level are decided by the Thordata SERP service using your API key.

## Tools

### `list_engines`

Returns the current schema version, audience, default engine, engine list, query field for each engine, and its resource URI.

### `search`

Executes a Thordata SERP search request.

| Parameter | Required | Description |
| --- | --- | --- |
| `engine` | No | Thordata engine key such as `google`, `google_images`, `google_maps`, or `bing`. The schema default is used when omitted. |
| `q` | No | Convenience search input. It is mapped to the engine's declared query field when that field is not `q`. |
| `json` | No | Response format: `1`, `2`, or `3`. Defaults to `1`. |
| `params` | No | Engine-specific parameters defined by the engine resource. Unsupported keys are ignored. |
| `response_mode` | No | `complete` or `compact`. Compact mode removes common metadata fields. |

A successful result uses this envelope:

```json
{
  "ok": true,
  "status": 200,
  "engine": "google",
  "data": {}
}
```

### `history`

Queries SERP request history for the API-key owner.

| Parameter | Required | Description |
| --- | --- | --- |
| `page` | No | Page number. Defaults to `1`. |
| `page_size` | No | Page size. Defaults to `20`. |
| `search_query` | No | Search query filter. |
| `search_engine` | No | Thordata engine-key filter. |
| `status` | No | `all`, `success`, or `error`. Defaults to `all`. |
| `start_time` | No | Start time in Unix seconds. |
| `end_time` | No | End time in Unix seconds. |
| `timezone` | No | IANA timezone or numeric offset. |

### `statistics`

Queries SERP usage statistics for the API-key owner.

| Parameter | Required | Description |
| --- | --- | --- |
| `start_date` | Yes | Start date in `YYYY-MM-DD`. |
| `end_date` | Yes | End date in `YYYY-MM-DD`. |
| `engines` | No | Comma-separated engine string or string array. |
| `timezone` | No | IANA timezone or offset such as `+08:00`. |

## Resources

| Resource | Description |
| --- | --- |
| `thordata://engines` | Index of the current supported engine definitions. |
| `thordata://engines/<engine-key>` | Groups, fields, controls, defaults, options, and conditions for one engine. |

## Building correct parameters

Only fields declared by the selected engine schema are sent. When you build a request:

- The final `engine` value is always the selected engine key.
- Top-level `q` and `json` override values with the same names in `params`; `json` defaults to `1`.
- Null values, empty strings, and empty arrays are omitted. Numeric zero and boolean `false` are preserved.

Serialization rules by field type:

- `string` and `options` values are sent as they are; Google Flights `departure_id` and `arrival_id` are normalized to uppercase.
- `boolean` (`switch`) values are serialized as `true` or `false`.
- `number` values are serialized as strings.
- `array` (`multi_select`) values are joined with commas, and every element must be a scalar. The country restriction field `cr` is declared as an array in the current schemas, so it is sent comma-joined exactly as given.
- `object` values are serialized as JSON.
- Unknown parameters are ignored, and the selected engine remains authoritative.

Response format:

- `json=1`: structured JSON only.
- `json=2`: JSON and HTML when supported.
- `json=3`: HTML only when supported.

`complete` mode returns the full result; `compact` mode strips common metadata fields.

## Error handling

The server normalizes responses and surfaces failures as MCP tool errors:

- Successful business codes are treated as success; all other non-success codes, non-2xx responses, transport failures, and error text are treated as failures.
- Double-encoded JSON payloads are unwrapped, and user tokens are redacted before results or errors are returned to you.
