# Configuration

ketch reads defaults from `~/.config/ketch/config.json`. Flags always override config values.

## Setup

```sh
# Create a default config file
ketch config init

# View effective config + available backends
ketch config
```

The discovery payload:

```json
{
  "config_path": "/home/user/.config/ketch/config.json",
  "backend": "brave",
  "searxng_url": "http://localhost:8081",
  "brave_api_key_set": false,
  "exa_api_key_set": false,
  "firecrawl_api_key_set": false,
  "limit": 5,
  "cache_ttl": "72h",
  "code_backend": "grepapp",
  "docs_backend": "context7",
  "context7_api_key_set": false,
  "sourcegraph_url": "https://sourcegraph.com",
  "github_token_source": "none",
  "github_token_set": false,
  "available_backends": ["brave", "ddg", "searxng", "exa", "firecrawl"],
  "available_code_backends": ["grepapp", "sourcegraph", "github"],
  "available_doc_backends": ["context7"]
}
```

`browser` is included only when set. The `*_set` booleans report key presence
only — key values are never printed. `github_token_source` reports where the
GitHub token was resolved from (`config`, `env`, `gh-cli`, or `none`), and
`github_token_set` is true whenever that chain resolved a token.
`url_rewrites` appears only when configured.

## Setting Values

```sh
ketch config set backend searxng
ketch config set brave_api_key BSA...
ketch config set searxng_url http://my-searxng:8080
ketch config set exa_api_key exa...
ketch config set firecrawl_api_key fc-...
ketch config set limit 10
ketch config set cache_ttl 4h
ketch config set browser chrome
ketch config set code_backend sourcegraph
ketch config set docs_backend context7
ketch config set context7_api_key ctx7...
ketch config set github_token ghp_...
```

## Config Keys

### Web Search

| Key | Default | Description |
|-----|---------|-------------|
| `backend` | `brave` | Default search backend: `brave`, `ddg`, `searxng`, `exa`, `firecrawl` |
| `brave_api_key` | — | Brave Search API key ([get one free](https://brave.com/search/api/)) |
| `exa_api_key` | — | Optional Exa API key for authenticated hosted MCP usage |
| `firecrawl_api_key` | — | [Firecrawl](https://docs.firecrawl.dev) API key (required for `-b firecrawl`) |
| `searxng_url` | `http://localhost:8081` | SearXNG instance URL |
| `limit` | `5` | Default max results (shared by `search`, `code`, `docs`) |

### Code & Docs Search

| Key | Default | Description |
|-----|---------|-------------|
| `code_backend` | `grepapp` | Default `ketch code` backend: `grepapp`, `sourcegraph`, `github` |
| `docs_backend` | `context7` | Default `ketch docs` backend: `context7`, `local` |
| `sourcegraph_url` | `https://sourcegraph.com` | Sourcegraph instance URL (for self-hosted) |
| `context7_api_key` | — | Context7 API key (required for `ketch docs`) |
| `github_token` | — | GitHub token for `ketch code -b github` (or use `$GITHUB_TOKEN` / `gh auth`) |

### Scraping & Cache

| Key | Default | Description |
|-----|---------|-------------|
| `cache_ttl` | `72h` | How long scraped pages stay cached (Go duration, e.g. `30m`, `4h`) |
| `browser` | — | Browser for JS-rendered pages: `chrome`, `chromium`, or absolute path |
| `url_rewrites` | — | Ordered regex rewrite rules applied before every fetch (see below) |

Secrets (`brave_api_key`, `exa_api_key`, `firecrawl_api_key`, `context7_api_key`, `github_token`) are stored in
plaintext in `config.json`; protect the file accordingly.

## URL Rewrites

`url_rewrites` is an ordered list of `{match, replace}` regex rules applied
transparently before any fetch in `scrape`, `search --scrape`, and `crawl`.
Use it to redirect URLs without touching the agent surface — for example,
routing Reddit links to the old UI:

```sh
ketch config set url_rewrites '[{"match":"www.reddit.com","replace":"old.reddit.com"}]'
```

The original URL is preserved in output as `url:`; the fetched URL appears as
`fetched_url:` when it differs. Rules are applied in order.

## Browser Rendering

JS-rendered pages are automatically detected and re-fetched via headless Chrome. Configure once, then scrape and crawl commands use it transparently.

```sh
# Use Chrome from PATH
ketch config set browser chrome

# Or use an absolute path
ketch config set browser /usr/bin/google-chrome-stable

# Download Chromium to ketch's cache dir
ketch browser install

# Check browser config and availability
ketch browser status
```

When a browser is configured, ketch automatically detects JS-rendered pages (React SPAs, Angular apps, Salesforce Lightning, etc.) and falls back to headless rendering. Static pages are always fetched via plain HTTP for speed.

## Page Cache

Scraped and crawled pages are cached in a single bbolt database at the platform cache directory:

| OS | Path |
|----|------|
| Linux | `~/.cache/ketch/cache.db` |
| macOS | `~/Library/Caches/ketch/cache.db` |
| Windows | `%LocalAppData%/ketch/cache.db` |

```sh
# View cache stats
ketch cache

# Clear all cached pages
ketch cache clear

# Bypass cache for a single scrape
ketch scrape https://example.com --no-cache

# Bypass cache for a crawl (force re-fetch everything)
ketch crawl https://example.com --no-cache
```
