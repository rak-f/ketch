// Package doctor runs cheap live health checks against every ketch surface:
// search/code/docs backends, the configured browser, and the page cache.
// Probes are read-only — they never write cache entries or mutate config —
// and each is bounded by a per-probe timeout so a full run stays fast.
package doctor

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/1broseidon/ketch/config"
	"github.com/1broseidon/ketch/httpx"
)

// Status classifies the outcome of a single doctor check.
type Status string

const (
	// StatusOK means the check passed: configured, reachable, credentials accepted.
	StatusOK Status = "ok"
	// StatusNoKey means the backend needs a key/token and none is configured.
	StatusNoKey Status = "no_key"
	// StatusUnreachable means the endpoint did not answer (network error,
	// timeout, or a server-side failure status).
	StatusUnreachable Status = "unreachable"
	// StatusMisconfigured means the endpoint answered but rejected the setup
	// (invalid key, SearXNG JSON format blocked, missing browser binary, ...).
	// Detail carries the fix hint.
	StatusMisconfigured Status = "misconfigured"
	// StatusSkipped means the check does not apply (e.g. no browser configured).
	StatusSkipped Status = "skipped"
)

// Check is one line of the doctor report. The JSON schema is stable:
// {surface, backend, status, detail, latency_ms}.
type Check struct {
	Surface   string `json:"surface"`
	Backend   string `json:"backend"`
	Status    Status `json:"status"`
	Detail    string `json:"detail,omitempty"`
	LatencyMS int64  `json:"latency_ms"`

	// Required marks checks that gate the process exit code: the default
	// backend of each surface, backends with an API key explicitly configured,
	// the configured browser, and the cache. Not part of the JSON schema.
	Required bool `json:"-"`
}

// Bad reports whether the check found a problem (anything but ok/skipped).
func (c Check) Bad() bool {
	return c.Status != StatusOK && c.Status != StatusSkipped
}

// spec describes one check before it runs.
type spec struct {
	surface  string
	backend  string
	required bool
	probe    func(ctx context.Context) (Status, string)
}

// DefaultTimeout is the per-probe timeout. Probes run concurrently, so the
// whole run is bounded by roughly one timeout, not the sum.
const DefaultTimeout = 3 * time.Second

// Run executes every check concurrently, each bounded by timeout
// (DefaultTimeout if <= 0), and returns results in stable surface order.
func Run(ctx context.Context, cfg *config.Config, timeout time.Duration) []Check {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	specs := buildSpecs(cfg, httpx.Default())
	checks := make([]Check, len(specs))

	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			start := time.Now()
			status, detail := s.probe(pctx)
			checks[i] = Check{
				Surface:   s.surface,
				Backend:   s.backend,
				Status:    status,
				Detail:    detail,
				LatencyMS: time.Since(start).Milliseconds(),
				Required:  s.required,
			}
		}()
	}
	wg.Wait()
	return checks
}

// buildSpecs assembles the check list for cfg. A check is required (gates the
// exit code) when it covers the configured default backend of its surface, a
// backend whose API key is explicitly set, the configured browser, or the
// cache. Optional backends that merely lack a key stay informational.
func buildSpecs(cfg *config.Config, client *http.Client) []spec {
	braveKey := cfg.BraveAPIKey
	exaKey := cfg.ExaAPIKey
	firecrawlKey := cfg.FirecrawlAPIKey
	c7Key := cfg.Context7APIKey
	searxngURL := cfg.SearxngURL
	sourcegraphURL := cfg.SourcegraphURL
	browser := cfg.Browser
	resolveGithub := cfg.ResolveGithubToken

	return []spec{
		{"search", "brave", cfg.Backend == "brave" || braveKey != "", func(ctx context.Context) (Status, string) {
			return probeBrave(ctx, client, braveEndpoint, braveKey)
		}},
		{"search", "ddg", cfg.Backend == "ddg", func(ctx context.Context) (Status, string) {
			return probeDDG(ctx, client, ddgEndpoint)
		}},
		{"search", "searxng", cfg.Backend == "searxng", func(ctx context.Context) (Status, string) {
			return probeSearxng(ctx, client, searxngURL)
		}},
		{"search", "exa", cfg.Backend == "exa" || exaKey != "", func(ctx context.Context) (Status, string) {
			return probeMCP(ctx, client, exaEndpoint(exaKey), "exa")
		}},
		{"search", "firecrawl", cfg.Backend == "firecrawl" || firecrawlKey != "", func(ctx context.Context) (Status, string) {
			return probeFirecrawl(ctx, client, firecrawlSearch, firecrawlKey)
		}},
		{"code", "grepapp", cfg.CodeBackend == "grepapp", func(ctx context.Context) (Status, string) {
			return probeMCP(ctx, client, grepAppEndpoint, "grep.app")
		}},
		{"code", "sourcegraph", cfg.CodeBackend == "sourcegraph", func(ctx context.Context) (Status, string) {
			return probeReachable(ctx, client, sourcegraphURL, "sourcegraph")
		}},
		{"code", "github", cfg.CodeBackend == "github", func(ctx context.Context) (Status, string) {
			return probeGitHub(ctx, client, githubAPIBase, resolveGithub)
		}},
		{"docs", "context7", cfg.DocsBackend == "context7" || c7Key != "", func(ctx context.Context) (Status, string) {
			return probeContext7(ctx, client, context7APIBase, c7Key)
		}},
		{"browser", browserBackendName(browser), browser != "", func(_ context.Context) (Status, string) {
			return checkBrowser(browser)
		}},
		{"cache", "bbolt", true, func(_ context.Context) (Status, string) {
			return checkCache()
		}},
	}
}

// browserBackendName labels the browser check's backend column.
func browserBackendName(configured string) string {
	if configured == "" {
		return "none"
	}
	return configured
}
