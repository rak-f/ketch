# Changelog

This page mirrors the canonical [`CHANGELOG.md`](https://github.com/1broseidon/ketch/blob/main/CHANGELOG.md) in the repo root. Versions follow [Semantic Versioning](https://semver.org/) and match the published git tags.

## Unreleased

**Added**

- `firecrawl` web search backend via the [Firecrawl](https://docs.firecrawl.dev) v2 search API, configured with `ketch config set firecrawl_api_key <key>` and selected with `-b firecrawl`. Reports `firecrawl_api_key_set` in `ketch config` discovery and is covered by a live `ketch doctor` probe.
- `exa` web search backend via Exa's hosted MCP endpoint, with optional `exa_api_key` config for authenticated usage.

## v0.9.3 — 2026-05-29

**Added**

- `grepapp` code search backend (Grep MCP, `mcp.grep.app`) — keyless, literal/regex search across 1M+ public GitHub repos. Now the default for `ketch code` (was `sourcegraph`).
- `ketch code --regex` interprets the query as a regular expression. Supported on `grepapp` and `sourcegraph`; `github` rejects it because REST code search is literal-only.

**Changed**

- `code.Searcher` interface refactored from positional params to a `Query` struct so backend options can grow without signature churn.

**Fixed**

- Documentation drift across README, CLAUDE.md, and the site reference: corrected the `ketch code` default backend, scoped `-b/--backend` to `search`/`code`/`docs`, documented previously-missing flags and the `version` command, and synced the `ketch config` discovery JSON example with real output.

## v0.9.2 — 2026-05-24

**Added**

- Differentiated exit codes so scripts and agents can distinguish failure classes: `2` validation/bad input, `3` not found, `4` upstream/network, `5` precondition (missing API key/token), `6` interrupted.

**Changed**

- `ketch crawl` no longer swallows Ctrl+C as exit 0. SIGINT during a foreground crawl exits `6` while still printing the summary collected before shutdown.
- `-b/--backend` is no longer a persistent root flag — it lives on `search` (matching the existing `code` and `docs` local flags). `ketch -b ddg search "q"` and `ketch search -b ddg "q"` both still work.

## v0.9.1 — 2026-05-22

**Added**

- `url_rewrites` config: an ordered list of `{match, replace}` regex rules applied transparently before any fetch in `scrape`, `search --scrape`, and `crawl`. Redirect URLs without touching the agent surface (e.g. `www.reddit.com` → `old.reddit.com`). The original URL is preserved in output as `url:`; the fetched URL appears as `fetched_url:` when different.

**Changed**

- `crawl.Crawl()` now takes a `*scrape.Scraper` from the caller (`Options.BrowserBin` removed). Affects only direct importers of the `crawl` package — the CLI is unchanged.

**Fixed**

- Broken example URLs in the README (#8, thanks @abhmul).

## v0.9.0 — 2026-05-12

**Changed**

- **Breaking.** Reusable packages moved from `pkg/<pkg>` to the module root. Import paths change from `github.com/1broseidon/ketch/pkg/<pkg>` to `github.com/1broseidon/ketch/<pkg>`.
- VitePress documentation site moved from `docs/` to `site/`, freeing `docs/` for the docs-search Go package. Site URL is unaffected.

## v0.8.1 — 2026-05-12

**Fixed**

- Page cache no longer returns unrendered JS-shell content after a browser is configured. Entries record their fetch source (`http` / `http_shell` / `browser`); JS-shell hits are bypassed once a browser is available, and pre-existing entries migrate in place. Fixes #7.

## v0.8.0 — 2026-05-02

**Changed**

- Reusable packages moved from `internal/` to `pkg/` (`cache`, `code`, `config`, `crawl`, `docs`, `extract`, `httpx`, `scrape`, `search`, `updatecheck`). Pure rename — exposes them for import by external Go programs.

## v0.7.1 — 2026-04-21

**Fixed**

- `ketch docs --resolve <name>` returned HTTP 400 after an upstream Context7 API change. Query param renamed (`?q=` → `?query=`), results moved into a `{"results": [...]}` envelope, and field names updated. `ketch docs <query>` and `--library` were unaffected.

## v0.7.0 — 2026-04-21

**Added**

- `ketch version` command and `--version` flag — reports build version, commit, and date.
- Passive update reminder when a newer release exists (cached 24h, throttled). Honors `KETCH_NO_UPDATE_NOTIFIER=1`, `CI`, `--json`, and non-TTY stderr.
- Ctrl+C (SIGINT) and SIGTERM cancel the root context, so foreground `ketch crawl` drains gracefully.

**Changed**

- HTTP stack tuned for crawling: a shared `*http.Transport` with a 30s timeout, `MaxIdleConnsPerHost=16`, HTTP/2, and a keep-alive dialer, reused by every backend.
- `context.Context` plumbed through the scraper, browser, crawler, and sitemap/llms.txt fetches — cancellation reaches into Rod and `http.Client.Do`.
- All HTTP response bodies capped at 20 MiB via `io.LimitReader`.

## v0.6.0 — 2026-04-11

**Added**

- `ketch scrape` smart input detection: multiple args, JSON array, file (one URL per line), or stdin pipe — auto-detected, no extra flags.
- `--concurrency N` on `ketch scrape` (default 5) — semaphore-based worker pool.
- `--select` and `--no-llms-txt` now propagate to multi-URL scraping.

**Changed**

- `search.Searcher.Search` and `docs.Searcher.Search` now take `context.Context` as the first param, consistent with `code.Searcher`.

## v0.5.0 — 2026-04-11

**Added**

- `ketch scrape --select <css>` — CSS selector extraction, bypasses readability (with browser fallback for JS-rendered pages).
- `ketch scrape --max-chars N` — truncate markdown output to N Unicode code points.
- `ketch scrape --trim` — strip markdown formatting while preserving content text (typically 30–40% token reduction).
- `ketch search/code/docs --minimal` — one result per line, tab-separated, pipe-friendly.
- llms.txt auto-detection: bare domain URLs check `/llms.txt` and return it directly when found. Disable with `--no-llms-txt`.

## v0.4.0 — 2026-04-11

**Added**

- `ketch code -b github` — GitHub Code Search backend. Token resolution: explicit config → `$GITHUB_TOKEN` → `$GH_TOKEN` → `gh auth token`.
- GitHub backend populates star counts via a single batched GraphQL call.
- `github_token_source` field in the `ketch config` discovery payload (the token itself is never printed).

**Changed**

- `code.Searcher.Search` takes `context.Context` as its first arg; backends use `http.NewRequestWithContext` for cancellation.

## v0.3.0 — 2026-04-10

**Added**

- `ketch code` command — code search via the Sourcegraph streaming SSE API. Zero config.
- `ketch docs` command — library documentation search via Context7. Requires an API key.
- Config keys: `code_backend`, `docs_backend`, `context7_api_key`, `sourcegraph_url`.

## v0.2.0

Browser rendering, crawl, and cache overhaul.

- **Browser rendering**: JS-rendered pages (React, Angular, Salesforce Lightning) automatically detected and re-fetched via headless Chrome using Rod.
  - `ketch config set browser chrome` — configure browser
  - `ketch browser install` — download Chromium
  - `ketch browser status` — check browser availability
  - Transparent fallback — agents see the same output format
- **Crawl command**: BFS and sitemap-based site crawling.
  - `ketch crawl <url>` — BFS crawl with configurable depth and concurrency
  - `ketch crawl <url> --sitemap` — sitemap-based crawl
  - `ketch crawl <url> --background` — detached process with status tracking
  - `ketch crawl status` / `ketch crawl stop` — monitor and control background crawls
- **Cache backend**: migrated from filesystem to an embedded bbolt database.
  - Single `cache.db` file; `Store` interface for future backends
  - Default TTL changed to 72h; shared cache between scrape and crawl

## v0.1.0

Initial release.

- Search via Brave, DuckDuckGo, or SearXNG
- Scrape URLs to clean markdown (readability + html-to-markdown)
- Concurrent batch scraping
- YAML frontmatter + markdown output format
- JSON config at `~/.config/ketch/config.json`
- TTL-based page cache with platform-correct paths
- `ketch config` discovery payload for agent introspection
- `--json` flag on all commands
- GoReleaser + Homebrew tap publishing
