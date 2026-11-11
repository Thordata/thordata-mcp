# Thordata MCP Server

Connect [Thordata](https://www.thordata.com/products/serp-api/mcp-server?op=flora&from=github) SERP to MCP-compatible AI agents and clients for real-time search, usage history, and statistics.

The Thordata SERP MCP Server exposes Thordata search capabilities through the Model Context Protocol (MCP), making it easy to integrate live search tools into agents, workflows, and MCP clients. In addition to real-time SERP requests, it also provides access to search history, usage statistics, and engine schema resources for richer integrations.

## Official MCP Registry

This server is published in the official MCP Registry as
`io.github.Thordata/thordata-mcp`.

[View the official Registry entry](https://registry.modelcontextprotocol.io/v0.1/servers?search=io.github.Thordata%2Fthordata-mcp)

The Registry entry points to the hosted Thordata MCP endpoint and provides
standardized connection metadata for compatible clients.


## Quick start

Most users connect to the **hosted Thordata MCP endpoint** — no installation, no local build. You only need a Thordata SERP API key.

### 1. Get your API key

Sign up at [Thordata](https://www.thordata.com/products/serp-api/mcp-server?op=flora&from=github) and get your SERP API key from the dashboard.

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

### Dataset tools

Dataset tools use the same unified API key as SERP tools. The path token, `Authorization: Bearer`, and `X-API-Key` all carry the same key, so no separate dataset credential is required:

```json
{
  "mcpServers": {
    "thordata": {
      "url": "https://mcp.thordata.com/YOUR_API_KEY/mcp"
    }
  }
}
```

If your client can set headers, either form works:

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

`X-API-Key` is still supported and overrides the path token for dataset tools when both are present. If no API key is supplied, the dataset tools return `dataset_credential_required`.

`fetch_dataset` is page-based: each call returns at most 10 records and charges the wallet once for that page. The first call creates an `order_id`; keep calling with `order_id`, `cursor`, and `page_no` until `next_cursor` is empty. All pages share one order in the wallet and backend.

Use `list_datasets` to see the dataset packages currently available to your account. `get_dataset_info` returns the price and limits for one `dataset_key`. Neither call charges the wallet.

### Web Scraper tools

Web Scraper uses the same Dataset/Web Scraper API key. The MCP server supports exactly these seven products:

| `spider_id` | Required parameters | Optional parameters |
| --- | --- | --- |
| `linkedin_company_information_by-url` | `url` | — |
| `linkedin_job_listings_information_by-job-url` | `job_url` | — |
| `youtube_video-post_by-keyword` | `keyword` | `num_of_posts` |
| `facebook_profile_by-profiles-url` | `url` | — |
| `youtube_video-post_by-explore` | `url` | `all_tabs` |
| `twitter_profile_by-profileurl` | `url` | — |
| `amazon_product_by-category-url` | `url`, `page_turning` | `sort_by` |

Read `thordata://web-scrapers` or call `list_web_scrapers` to obtain the current public field schema. Then call `fetch_web_scraper` with the selected `spider_id` and its `parameters` object. The server validates the fields against the public schema, creates one remote task, polls it, downloads the final JSON and returns the records.

`fetch_web_scraper` waits up to five minutes. `max_wait_seconds` may be set from 30 to 300, with 300 as the default. A timeout stops this MCP call but leaves the remote task running; the error includes the `task_id`, and the server never automatically resubmits an uncertain task. `include_errors` defaults to `true`, and `file_name` defaults to `{{TasksID}}`.

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
- **`list_web_scrapers`** — list the seven supported Web Scraper products and their current parameters.
- **`fetch_web_scraper`** — run one supported Web Scraper and wait for its final JSON result.

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
| `thordata://web-scrapers` | Index of the supported Web Scraper products and parameters. |
| `thordata://web-scrapers/<spider_id>` | Current fields, required flags, defaults and options for one Web Scraper product. |

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
