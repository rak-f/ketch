package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/1broseidon/ketch/cache"
	"github.com/1broseidon/ketch/scrape"
)

// Production endpoints. Probes take these as parameters so tests can point
// them at httptest servers.
const (
	braveEndpoint   = "https://api.search.brave.com/res/v1/web/search"
	ddgEndpoint     = "https://html.duckduckgo.com/html/"
	grepAppEndpoint = "https://mcp.grep.app"
	exaMCPEndpoint  = "https://mcp.exa.ai/mcp"
	firecrawlSearch = "https://api.firecrawl.dev/v2/search"
	githubAPIBase   = "https://api.github.com"
	context7APIBase = "https://context7.com"
)

// ddgUA mirrors the search backend's user agent — DDG's HTML endpoint rejects
// requests with a default Go client UA.
const ddgUA = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"

// exaEndpoint appends the API key query param exactly like the search backend
// does; keyless usage is supported.
func exaEndpoint(apiKey string) string {
	endpoint := exaMCPEndpoint
	if strings.TrimSpace(apiKey) != "" {
		v := url.Values{}
		v.Set("exaApiKey", strings.TrimSpace(apiKey))
		endpoint += "?" + v.Encode()
	}
	return endpoint
}

// get performs a GET with the given headers and returns the response with its
// body unread. A nil response means the request itself failed.
func get(ctx context.Context, client *http.Client, u string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return client.Do(req)
}

// drain discards a bounded amount of body and closes it, so connections can
// be reused without buffering large responses.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}

// probeBrave checks the Brave Search API with a minimal one-result query.
func probeBrave(ctx context.Context, client *http.Client, endpoint, apiKey string) (Status, string) {
	if apiKey == "" {
		return StatusNoKey, "API key not set (get one free at https://brave.com/search/api/ then: ketch config set brave_api_key <key>)"
	}
	resp, err := get(ctx, client, endpoint+"?q=ketch&count=1&text_decorations=false&result_filter=web", map[string]string{
		"Accept":               "application/json",
		"X-Subscription-Token": apiKey,
	})
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusOK:
		return StatusOK, ""
	case http.StatusUnauthorized, http.StatusForbidden:
		return StatusMisconfigured, "API key rejected (ketch config set brave_api_key <key>)"
	case http.StatusTooManyRequests:
		return StatusOK, "reachable, key accepted (rate limited)"
	default:
		return StatusUnreachable, fmt.Sprintf("returned status %d", resp.StatusCode)
	}
}

// probeFirecrawl checks the Firecrawl v2 search API with a minimal one-result
// query. Firecrawl requires an API key, so a missing key is a clean no_key.
func probeFirecrawl(ctx context.Context, client *http.Client, endpoint, apiKey string) (Status, string) {
	if apiKey == "" {
		return StatusNoKey, "API key not set (get one free at https://firecrawl.dev then: ketch config set firecrawl_api_key <key>)"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(`{"query":"ketch","limit":1}`))
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusOK:
		return StatusOK, ""
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusPaymentRequired:
		return StatusMisconfigured, "API key rejected (ketch config set firecrawl_api_key <key>)"
	case http.StatusTooManyRequests:
		return StatusOK, "reachable, key accepted (rate limited)"
	default:
		return StatusUnreachable, fmt.Sprintf("returned status %d", resp.StatusCode)
	}
}

// probeDDG checks DuckDuckGo's HTML endpoint with a minimal query.
func probeDDG(ctx context.Context, client *http.Client, endpoint string) (Status, string) {
	resp, err := get(ctx, client, endpoint+"?q=ketch", map[string]string{"User-Agent": ddgUA})
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusOK:
		return StatusOK, ""
	case http.StatusAccepted:
		return StatusOK, "reachable (rate limited)"
	default:
		return StatusUnreachable, fmt.Sprintf("returned status %d", resp.StatusCode)
	}
}

// probeSearxng checks a SearXNG instance via the same format=json search call
// ketch uses. It specifically detects the stock-config trap where the JSON
// format is disabled: SearXNG returns 403 for format=json unless settings.yml
// enables it.
func probeSearxng(ctx context.Context, client *http.Client, baseURL string) (Status, string) {
	if baseURL == "" {
		return StatusMisconfigured, "searxng_url not set (ketch config set searxng_url <url>)"
	}
	resp, err := get(ctx, client, baseURL+"/search?q=ketch&format=json&pageno=1", nil)
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var body struct {
			Results []json.RawMessage `json:"results"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
			return StatusMisconfigured, fmt.Sprintf("returned non-JSON response — is %s a SearXNG instance?", baseURL)
		}
		return StatusOK, ""
	case http.StatusForbidden:
		return StatusMisconfigured, `format=json is blocked (HTTP 403) — enable it in the instance's settings.yml: search.formats must include "json", then restart SearXNG`
	case http.StatusTooManyRequests:
		return StatusMisconfigured, "rate limited (HTTP 429) — the SearXNG limiter is throttling ketch; consider disabling the limiter for local instances"
	default:
		return StatusUnreachable, fmt.Sprintf("returned status %d", resp.StatusCode)
	}
}

// probeMCP checks a hosted MCP endpoint (grep.app, Exa) with a tools/list
// call — the cheapest request those servers answer.
func probeMCP(ctx context.Context, client *http.Client, endpoint, name string) (Status, string) {
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusOK {
		return StatusOK, ""
	}
	return StatusUnreachable, fmt.Sprintf("%s returned status %d", name, resp.StatusCode)
}

// probeReachable checks that a base URL answers at all (used for Sourcegraph,
// where any HTTP response — including auth walls on private instances — means
// the instance is up).
func probeReachable(ctx context.Context, client *http.Client, baseURL, name string) (Status, string) {
	resp, err := get(ctx, client, baseURL, nil)
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	defer drain(resp)

	if resp.StatusCode >= http.StatusInternalServerError {
		return StatusUnreachable, fmt.Sprintf("%s returned status %d", name, resp.StatusCode)
	}
	return StatusOK, ""
}

// probeGitHub resolves a token through the standard chain (config →
// $GITHUB_TOKEN/$GH_TOKEN → gh CLI) and, only when one resolves, validates it
// against /rate_limit — an authed call that costs no search quota.
func probeGitHub(ctx context.Context, client *http.Client, apiBase string, resolve func() (token, source string)) (Status, string) {
	token, source := resolve()
	if token == "" {
		return StatusNoKey, "no token (ketch config set github_token <token>, $GITHUB_TOKEN, or gh auth login)"
	}
	resp, err := get(ctx, client, apiBase+"/rate_limit", map[string]string{
		"Authorization":        "Bearer " + token,
		"X-GitHub-Api-Version": "2022-11-28",
	})
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusOK:
		return StatusOK, "token via " + source
	case http.StatusUnauthorized:
		return StatusMisconfigured, fmt.Sprintf("token rejected (source: %s; check: gh auth status)", source)
	default:
		return StatusUnreachable, fmt.Sprintf("returned status %d", resp.StatusCode)
	}
}

// probeContext7 checks the Context7 resolve endpoint with a minimal query.
func probeContext7(ctx context.Context, client *http.Client, apiBase, apiKey string) (Status, string) {
	if apiKey == "" {
		return StatusNoKey, "API key not set (get one then: ketch config set context7_api_key <key>)"
	}
	resp, err := get(ctx, client, apiBase+"/api/v1/search?query=go", map[string]string{
		"Authorization": "Bearer " + apiKey,
	})
	if err != nil {
		return StatusUnreachable, probeErrDetail(err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusOK:
		return StatusOK, ""
	case http.StatusUnauthorized:
		return StatusMisconfigured, "API key rejected (ketch config set context7_api_key <key>)"
	default:
		return StatusUnreachable, fmt.Sprintf("returned status %d", resp.StatusCode)
	}
}

// checkBrowser verifies the configured browser binary actually resolves to a
// file on disk or PATH. No browser configured is a clean skip — rendering is
// optional.
func checkBrowser(configured string) (Status, string) {
	if configured == "" {
		return StatusSkipped, "not configured (browser rendering disabled; optional)"
	}
	bin, err := scrape.ResolveBrowserBin(configured)
	if err != nil {
		return StatusMisconfigured, err.Error()
	}
	return StatusOK, bin
}

// checkCache verifies the cache directory is writable and reports entry
// count, size, and lock state via the existing read-only stats path. It never
// opens the database for writing and never touches cache entries.
func checkCache() (Status, string) {
	path, err := cache.DBPath()
	if err != nil {
		return StatusMisconfigured, fmt.Sprintf("cannot resolve cache path: %v", err)
	}
	// Writability check on the directory, not the database: a throwaway temp
	// file, removed immediately. cache.db itself is never opened for writing.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".doctor-*")
	if err != nil {
		return StatusMisconfigured, fmt.Sprintf("cache dir not writable: %v", err)
	}
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())

	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return StatusOK, fmt.Sprintf("writable, empty (no cache database yet at %s)", path)
	}
	if c := cache.NewReadOnly(); c != nil {
		defer c.Close()
		entries, size := c.Stats()
		return StatusOK, fmt.Sprintf("%d entries, %s", entries, formatBytes(size))
	}
	// The database exists but a read-only open failed: another process (e.g. a
	// background crawl) holds the lock. That is healthy, just report it.
	var size int64
	if st, err := os.Stat(path); err == nil {
		size = st.Size()
	}
	return StatusOK, fmt.Sprintf("locked by another process (%s)", formatBytes(size))
}

// probeErrDetail compacts a transport error into a single-line detail.
func probeErrDetail(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return err.Error()
}

// formatBytes renders a byte count in the same style as `ketch cache`.
func formatBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
