# Backends

ketch has three search surfaces, each with its own backends: web search (`ketch search`), code search (`ketch code`), and library docs (`ketch docs`).

## Web Search Backends

ketch supports five web-search backends. Set the default with `ketch config set backend <name>`.

## Brave (default)

Brave Search offers a free API tier — no scraping, proper JSON API, reliable.

**Setup:**

1. Get a free API key at [brave.com/search/api](https://brave.com/search/api/)
2. Set it: `ketch config set brave_api_key <your-key>`

**Free tier limits:** 2,000 queries/month, 1 query/second.

## DuckDuckGo

Zero-config HTML scraping of DuckDuckGo's search results. No API key needed.

**Setup:** None — works out of the box.

**Limitations:** DuckDuckGo aggressively rate-limits automated requests. You may see `ddg rate limited after retries` errors under heavy use. ketch retries up to 3 times with 500ms backoff.

## SearXNG

Self-hosted metasearch engine with a JSON API. The most reliable option for heavy use.

**Setup:**

1. Run a SearXNG instance (Docker is easiest):

```sh
docker run -d -p 8081:8080 searxng/searxng
```

2. Enable JSON format in SearXNG settings (required for the API).

3. Point ketch to it:

```sh
ketch config set backend searxng
ketch config set searxng_url http://localhost:8081
```

**Recommended for:** operators running agents that search frequently, or anyone who wants full control over their search infrastructure.

## Exa

AI-oriented web search via Exa's hosted MCP endpoint. It works without configuration by default, with an optional Exa API key for authenticated usage.

**Setup:** None for hosted MCP. Optional key:

```sh
ketch config set exa_api_key <your-key>
ketch config set backend exa
```

**Recommended for:** agent workflows that benefit from Exa's clean result snippets and content-oriented search output.

## Firecrawl

Web search via the [Firecrawl](https://firecrawl.dev) v2 [search API](https://docs.firecrawl.dev/api-reference/endpoint/search) — proper JSON API, no scraping. Requires an API key.

**Setup:**

1. Get an API key at [firecrawl.dev](https://firecrawl.dev)
2. Set it: `ketch config set firecrawl_api_key <your-key>`
3. Make it the default: `ketch config set backend firecrawl`

**Recommended for:** operators already using Firecrawl for scraping who want a single provider for both search and page extraction. Pair with `--scrape` to fetch full content per result.

## Code Search Backends

`ketch code` searches real source code across open-source repositories. Set the default with `ketch config set code_backend <name>`.

### Grep (default)

The Grep MCP server (`mcp.grep.app`) — literal or regex search over 1M+ public GitHub repos. Zero config, no token.

**Setup:** None — works out of the box.

Use `--regex` to interpret the query as a regular expression.

### Sourcegraph

Grep-style search across ~1M OSS repos with exact line matches over an SSE stream. Results are filtered to non-archived, non-fork repos by default. Supports `--regex`.

**Setup:** None for the public instance. For a self-hosted instance: `ketch config set sourcegraph_url <url>`.

### GitHub

GitHub Code Search (REST `/search/code`) with a batched GraphQL call for star counts.

**Setup:** A token is required. Resolution chain: `ketch config set github_token <tok>` → `$GITHUB_TOKEN` → `$GH_TOKEN` → `gh auth token` (if the `gh` CLI is installed).

**Limits:** 30 requests/minute. Token must have `repo` scope.

## Docs Backends

`ketch docs` fetches library documentation. Set the default with `ketch config set docs_backend <name>`.

### Context7 (default)

Curated, version-aware documentation snippets.

**Setup:** Free key: `ketch config set context7_api_key <key>`.

### Local

A planned FTS5 SQLite backend for offline/private docs. Not yet implemented.
