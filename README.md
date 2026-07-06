# ketch

[![GitHub Stars](https://img.shields.io/github/stars/1broseidon/ketch?style=social)](https://github.com/1broseidon/ketch/stargazers)
[![Go Reference](https://pkg.go.dev/badge/github.com/1broseidon/ketch.svg)](https://pkg.go.dev/github.com/1broseidon/ketch)
[![Go Report Card](https://goreportcard.com/badge/github.com/1broseidon/ketch)](https://goreportcard.com/report/github.com/1broseidon/ketch)
[![Latest Release](https://img.shields.io/github/v/release/1broseidon/ketch)](https://github.com/1broseidon/ketch/releases/latest)

A stateless CLI for web search, code search, library docs, and scraping — one binary, no daemon, no API server to run.

## Why ketch

Most research tooling for agents means wiring up several provider SDKs, each with its own auth and response shape. ketch collapses that into one binary with three research surfaces:

- `ketch search` — web search (Brave, DuckDuckGo, SearXNG, Exa, or Firecrawl)
- `ketch code` — grep real OSS source across public repos (Grep, Sourcegraph, or GitHub Code Search)
- `ketch docs` — curated, version-aware library documentation (Context7)

Plus `ketch scrape` and `ketch crawl` to turn any URL or site into clean markdown.

It's built for two audiences at once:

- **Humans**, who want a fast terminal tool for the same job `curl | pandoc` or a browser tab would otherwise do.
- **AI agents**, who want structured, predictable output (`--json` everywhere), documented exit codes for control flow, and a single `ketch config` call to discover what backends are active — no environment probing, no per-provider glue code.

An operator configures the backend once (`ketch config set backend searxng`); every agent invocation afterward just calls `ketch search` or `ketch scrape` without knowing or caring which provider is behind it.

## Install

```sh
# Homebrew
brew install 1broseidon/tap/ketch

# go install
go install github.com/1broseidon/ketch@latest

# Or download a prebuilt binary (linux/darwin/windows, amd64/arm64)
# from https://github.com/1broseidon/ketch/releases
```

## Quickstart

```sh
$ ketch scrape https://go.dev/doc/effective_go
---
url: https://go.dev/doc/effective_go
title: Effective Go - The Go Programming Language
words: 16582
---
1. [Documentation](https://go.dev/doc/)
2. [Effective Go](https://go.dev/doc/effective_go)
...
## Introduction

Go is an open-source programming language that focuses on simplicity, reliability, and efficiency...
```

Search real OSS code with zero configuration:

```sh
$ ketch code "http.NewRequestWithContext" --lang go --limit 2
---
query: http.NewRequestWithContext
lang: go
backend: grepapp
result_count: 2
---
harness/harness  registry/app/remote/clients/registry/client.go  (line 207)
  req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildPingURL(c.url), nil)
  https://github.com/harness/harness/blob/main/registry/app/remote/clients/registry/client.go
...
```

Web search needs a backend configured first — the default (`brave`) requires a free API key:

```sh
ketch config set brave_api_key <key>
ketch search "golang error handling"
ketch search "golang error handling" --scrape   # fetch + extract full content per result
```

Every command takes `--json` for structured output:

```sh
ketch scrape https://example.com --json
# {"url":"https://example.com","title":"Example Domain","markdown":"..."}
```

## Commands

| Command | What it does |
|---|---|
| `search` | Web search — Brave, DuckDuckGo, SearXNG, Exa, or Firecrawl |
| `code` | Grep real OSS source — Grep (default), Sourcegraph, or GitHub Code Search |
| `docs` | Library/framework docs — Context7 (curated, version-aware snippets) |
| `scrape` | Fetch URL(s) and extract clean markdown; concurrent batch, JSON array, file, or stdin input |
| `crawl` | BFS or sitemap crawl with optional background execution and status tracking |
| `browser` | Manage headless Chrome for JS-rendered pages (`install`, `status`) |
| `config` | Show effective config as JSON, or `init` / `set` / `path` |
| `cache` | Show page-cache stats, or `clear` |
| `doctor` | Live health check of every backend, the browser, and the cache — exit `0` healthy, `5` when a configured surface is broken |
| `mcp` | Run ketch as an MCP server over stdio (`mcp serve`) — the five research surfaces as tools |
| `version` | Print version, commit, build date |

Every command supports `-h/--help` for its full flag list; `--json` is the only flag global to every command. Full flag reference lives at [1broseidon.github.io/ketch](https://1broseidon.github.io/ketch/).

### Backends

| Surface | Default | Also available | Setup |
|---|---|---|---|
| `search` | `brave` | `ddg`, `searxng`, `exa`, `firecrawl` | Brave and Firecrawl need a free key (`ketch config set brave_api_key <key>` / `firecrawl_api_key`); `ddg`, `searxng`, and `exa` work with zero config |
| `code` | `grepapp` | `sourcegraph`, `github` | Grep and Sourcegraph need nothing; GitHub uses `gh auth login`, `$GITHUB_TOKEN`, or `ketch config set github_token <tok>` |
| `docs` | `context7` | `local` (planned, not yet implemented) | Free key: `ketch config set context7_api_key <key>` |

## Why it works well for agents

- **Stateless, single binary.** No daemon, no server to keep alive — call, get a result, exit.
- **Documented exit codes**, not just stderr text, for scripted control flow: `2` bad input, `3` not found, `4` upstream/network failure, `5` missing precondition (e.g. no API key), `6` cancelled (SIGINT/SIGTERM).
- **Automatic JS-rendering fallback.** `ketch scrape` and `ketch crawl` detect JS-shell pages (React/Vue/Svelte SPAs, streaming hydration frameworks) and transparently re-fetch via headless Chrome when needed — same output shape either way.
- **Smart input detection on `scrape`.** Single URL, multiple positional args, a JSON array, a file of URLs, or stdin — no `--batch` flag required.
- **Page cache.** Fetches are cached (bbolt, default TTL 72h); repeat scrapes and crawls return instantly. `--no-cache` bypasses it.
- **One discovery call.** `ketch config` returns the full effective configuration and active backends as JSON, so an agent can inspect capabilities without parsing `--help` text.

## Configuration

ketch reads defaults from `~/.config/ketch/config.json`. Flags always override config values.

```sh
ketch config init                          # write a default config file
ketch config set backend searxng           # set a default backend
ketch config set searxng_url http://my-searxng:8080
ketch config set browser chrome            # enable browser fallback for JS-rendered pages
ketch config                               # print effective config + available backends
ketch config path                          # print the config file path
```

Other configurable keys include per-backend API keys and URLs (`brave_api_key`, `context7_api_key`, `github_token`, `sourcegraph_url`, `exa_api_key`, `firecrawl_api_key`), `cache_ttl`, `url_rewrites` (regex rewrite rules applied before fetch), and `spa_markers` (extra JS-shell detection tokens). See the [config reference](https://1broseidon.github.io/ketch/) for the full list.

## Agent integration

Point an agent's system prompt at ketch instead of teaching it individual search/scrape APIs:

```markdown
Use `ketch` for external research — web pages, OSS code, library docs.
- `ketch search "query"` / `ketch search "query" --scrape` for web results with optional full content
- `ketch scrape <url> [url...]` for clean markdown from one or more URLs
- `ketch code "query" --lang go` for real OSS code with repo/line context
- `ketch docs "query" --library /org/repo` for version-aware library docs
- All commands support `--json`. `ketch config` reports active backends.
```

The operator configures backends once; the agent's prompt never needs to mention which provider is behind `ketch search`.

For a fuller agent playbook — surface routing, token budgets, error-code control flow, a deep-research recipe, and guided backend setup — install the bundled skill from [`skills/ketch/`](./skills/ketch/) (works with any agent that loads `SKILL.md`-style skills, e.g. Claude Code).

### MCP server

For agents that speak MCP instead of shelling out, `ketch mcp serve` runs the same five surfaces — `search`, `code`, `docs`, `scrape`, `crawl` — as MCP tools over stdio, using the same config and backends as the CLI. To register it with Claude Code:

```sh
claude mcp add ketch -- ketch mcp serve
```

Tool errors carry the exit-code taxonomy as stable message prefixes: `[validation]`, `[not_found]`, `[upstream]`, `[precondition]`, `[cancelled]`.

### Claude Code plugin

The CLI on PATH is all ketch needs — but if you want Claude Code to wire up the MCP server and the [bundled skill](./skills/ketch/) in one step, this repo doubles as a plugin marketplace:

```sh
claude plugin marketplace add 1broseidon/ketch
claude plugin install ketch@ketch
```

The plugin registers `ketch mcp serve` as an MCP server and installs the ketch research skill. It does not bundle the binary: `ketch` >= v0.10.0 must be on PATH (`brew install 1broseidon/tap/ketch` or `go install github.com/1broseidon/ketch@latest`).

## Contributing

Issues and pull requests are welcome at [github.com/1broseidon/ketch](https://github.com/1broseidon/ketch).

## License

[MIT](./LICENSE)
