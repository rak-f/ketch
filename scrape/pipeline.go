package scrape

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/1broseidon/ketch/config"
	"github.com/1broseidon/ketch/extract"
	"github.com/1broseidon/ketch/httpx"
	"github.com/1broseidon/ketch/urlrewrite"
)

// This file owns the cache-aware scrape pipeline shared by the CLI (cmd/) and
// the MCP server (mcp/). It used to live as private helpers in cmd/scrape.go
// with a drifting copy in mcp/scrape.go; now both call these.

// PageCache is the subset of the cache API the scrape pipeline needs.
// *cache.Cache implements it (the cache package imports scrape, so the
// dependency points this way via an interface). Callers may pass nil to
// bypass caching entirely.
type PageCache interface {
	Get(url string) (*Page, string)
	Put(url string, page *Page, source string)
	GetRaw(url string) (rawHTML, source string, page *Page)
	PutRaw(url string, page *Page, source, rawHTML string)
}

// Sentinel errors for selector scrapes so callers can classify failures
// (CLI exit codes, MCP error kinds) without string matching.
var (
	// ErrBadSelector wraps selector-extraction failures (typically an invalid
	// CSS selector) — a caller-input problem, not an upstream fault.
	ErrBadSelector = errors.New("selector extraction failed")
	// ErrSelectorNoMatch reports that the selector matched no elements.
	ErrSelectorNoMatch = errors.New("no elements matched selector")
)

// NewFromConfig builds a Scraper from cfg: compiled URL rewriter, optional
// browser binary, and operator-configured SPA markers. The rewriter's regexes
// are compiled once here — callers should construct one Scraper and reuse it.
// The returned Scraper is safe for concurrent use and must be Closed by the
// caller.
func NewFromConfig(cfg *config.Config) (*Scraper, error) {
	rw, err := urlrewrite.NewRewriter(cfg.URLRewrites)
	if err != nil {
		return nil, fmt.Errorf("invalid url_rewrites: %w", err)
	}
	s := NewWithConfig(cfg.Browser, rw, cfg.SPAMarkers)
	if cfg.ScrapeBackend == "firecrawl" {
		if cfg.FirecrawlAPIKey == "" {
			return nil, fmt.Errorf("firecrawl: API key not set (get one free at https://firecrawl.dev then: ketch config set firecrawl_api_key <key>)")
		}
		s.fc = newFirecrawlClient(cfg.FirecrawlAPIKey)
	}
	return s, nil
}

// CachedScrape checks the cache first, falls back to fetch+extract.
// Hits are bypassed when the entry was extracted from an unrendered JS shell
// and a browser is now available to do better, or when the entry predates
// source tracking (a one-time migration once a browser is configured).
// The cache is keyed by the rewritten URL so original and rewritten URLs
// share one cache entry.
func (s *Scraper) CachedScrape(ctx context.Context, pc PageCache, url string) (*Page, error) {
	key := s.Rewrite(url)
	if pc != nil {
		if page, source := pc.Get(key); page != nil && !CacheStaleForBrowser(source, s.HasBrowser()) {
			return page, nil
		}
	}

	page, source, err := s.Scrape(ctx, url)
	if err != nil {
		return nil, err
	}

	if pc != nil {
		pc.Put(key, page, source)
	}
	return page, nil
}

// CachedScrapeRaw is the raw-HTML path. It routes through ScrapeConditional so
// one fetch yields Page + RawHTML + Source (the markdown path's Scrape
// discards the body). Raw lookup is a hit only when RawHTML is non-empty — a
// markdown-only entry does not poison a raw request. On a raw miss against an
// existing markdown entry, the refetch back-fills RawHTML while preserving the
// cached Page (one fetch, both representations cached). A nil pc skips cache
// read/write and returns the fresh fetch result directly.
func (s *Scraper) CachedScrapeRaw(ctx context.Context, pc PageCache, url string) (*Page, string, string, error) {
	key := s.Rewrite(url)
	if pc != nil {
		if rawHTML, source, page := pc.GetRaw(key); page != nil {
			return page, rawHTML, source, nil
		}
	}

	result, err := s.ScrapeConditional(ctx, url, "", "")
	if err != nil {
		return nil, "", "", err
	}
	if result.NotModified {
		return nil, "", "", fmt.Errorf("unexpected 304 Not Modified without cached ETag for %s", url)
	}

	if pc != nil {
		pc.PutRaw(key, result.Page, result.Source, result.RawHTML)
	}
	return result.Page, result.RawHTML, result.Source, nil
}

// CachedScrapeForce is the forced-browser markdown path. It always renders via
// the browser, reusing a cache entry only when that entry is itself a browser
// render (force-browser selects the rendering pipeline, not cache freshness —
// bypass the cache for that). HTTP/shell/markdown-only entries never satisfy a
// forced request, which is precisely the anti-poisoning guard.
func (s *Scraper) CachedScrapeForce(ctx context.Context, pc PageCache, url string) (*Page, error) {
	key := s.Rewrite(url)
	if pc != nil {
		if page, source := pc.Get(key); page != nil && source == SourceBrowser {
			return page, nil
		}
	}
	page, _, err := s.BrowserScrape(ctx, url)
	if err != nil {
		return nil, err
	}
	if pc != nil {
		pc.Put(key, page, SourceBrowser)
	}
	return page, nil
}

// CachedScrapeRawForce is the forced-browser raw path: render unconditionally
// and emit the rendered HTML. A cache hit is honored only for a prior browser
// render (GetRaw already requires non-empty RawHTML). BrowserScrape runs
// extractor.Extract internally; the resulting markdown Page is unused here,
// but that work is dwarfed by render cost — don't split the API to avoid it.
func (s *Scraper) CachedScrapeRawForce(ctx context.Context, pc PageCache, url string) (*Page, string, string, error) {
	key := s.Rewrite(url)
	if pc != nil {
		if rawHTML, source, page := pc.GetRaw(key); page != nil && source == SourceBrowser {
			return page, rawHTML, source, nil
		}
	}
	page, html, err := s.BrowserScrape(ctx, url)
	if err != nil {
		return nil, "", "", err
	}
	if pc != nil {
		pc.PutRaw(key, page, SourceBrowser, html)
	}
	return page, html, SourceBrowser, nil
}

// ScrapeMarkdown picks the markdown fetch path. Under the Firecrawl backend it
// delegates to the Firecrawl scrape API (forceBrowser doesn't apply — Firecrawl
// renders server-side); otherwise it is a forced browser render or the
// auto-detecting CachedScrape.
func (s *Scraper) ScrapeMarkdown(ctx context.Context, pc PageCache, url string, forceBrowser bool) (*Page, error) {
	if s.fc != nil {
		return s.firecrawlScrape(ctx, pc, url)
	}
	if forceBrowser {
		return s.CachedScrapeForce(ctx, pc, url)
	}
	return s.CachedScrape(ctx, pc, url)
}

// ScrapeRaw picks the raw-HTML fetch path. Under the Firecrawl backend it
// requests Firecrawl's rawHtml format; otherwise it is a forced browser render
// or the auto-detecting CachedScrapeRaw.
func (s *Scraper) ScrapeRaw(ctx context.Context, pc PageCache, url string, forceBrowser bool) (*Page, string, string, error) {
	if s.fc != nil {
		return s.firecrawlScrapeRaw(ctx, pc, url)
	}
	if forceBrowser {
		return s.CachedScrapeRawForce(ctx, pc, url)
	}
	return s.CachedScrapeRaw(ctx, pc, url)
}

// firecrawlScrape fetches url as markdown via the Firecrawl backend, honoring
// the page cache. Entries are tagged SourceFirecrawl so they never collide
// with local-pipeline entries for the same URL; a markdown hit additionally
// requires non-empty Markdown so a prior --raw entry (rawHtml only) doesn't
// satisfy a markdown request.
func (s *Scraper) firecrawlScrape(ctx context.Context, pc PageCache, url string) (*Page, error) {
	key := s.Rewrite(url)
	if pc != nil {
		if page, source := pc.Get(key); page != nil && source == SourceFirecrawl && page.Markdown != "" {
			return page, nil
		}
	}
	doc, err := s.fc.scrape(ctx, key, false)
	if err != nil {
		return nil, err
	}
	page := &Page{URL: url, Title: doc.Title, Markdown: doc.Markdown}
	if key != url {
		page.FetchedURL = key
	}
	if pc != nil {
		pc.Put(key, page, SourceFirecrawl)
	}
	return page, nil
}

// firecrawlScrapeRaw is the Firecrawl backend's --raw path: it requests the
// rawHtml format and caches it as a SourceFirecrawl raw entry.
func (s *Scraper) firecrawlScrapeRaw(ctx context.Context, pc PageCache, url string) (*Page, string, string, error) {
	key := s.Rewrite(url)
	if pc != nil {
		if rawHTML, source, page := pc.GetRaw(key); page != nil && source == SourceFirecrawl {
			return page, rawHTML, source, nil
		}
	}
	doc, err := s.fc.scrape(ctx, key, true)
	if err != nil {
		return nil, "", "", err
	}
	page := &Page{URL: url, Title: doc.Title}
	if key != url {
		page.FetchedURL = key
	}
	if pc != nil {
		pc.PutRaw(key, page, SourceFirecrawl, doc.RawHTML)
	}
	return page, doc.RawHTML, SourceFirecrawl, nil
}

// ScrapeSelector fetches rawURL and returns only elements matching the CSS
// selector, converted to markdown — bypassing readability extraction and the
// page cache. Under forceBrowser it renders via the browser and selects
// against the rendered DOM; otherwise it fetches plain HTTP with JS-shell
// auto-detection. The URL is rewritten before fetch so selector scrapes share
// the canonical URL-rewrite path with Scrape/ScrapeConditional.
func (s *Scraper) ScrapeSelector(ctx context.Context, rawURL, selector string, forceBrowser bool) (*Page, error) {
	fetchURL := s.Rewrite(rawURL)
	html, err := s.fetchHTMLForSelector(ctx, rawURL, fetchURL, forceBrowser)
	if err != nil {
		return nil, err
	}
	markdown, err := extract.ExtractSelector(html, selector)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSelector, err)
	}
	if markdown == "" {
		return nil, fmt.Errorf("%w %q", ErrSelectorNoMatch, selector)
	}
	page := &Page{URL: rawURL, Title: extract.Title(html), Markdown: markdown}
	if fetchURL != rawURL {
		page.FetchedURL = fetchURL
	}
	return page, nil
}

// fetchHTMLForSelector returns the HTML to run a CSS selector against. Under
// forceBrowser it renders via the browser (then selects the rendered DOM);
// otherwise it does the plain fetch with JS-shell auto-detection. rawURL is
// passed to BrowserScrape, which rewrites internally; fetchURL is the
// already-rewritten URL for the plain Fetch path.
func (s *Scraper) fetchHTMLForSelector(ctx context.Context, rawURL, fetchURL string, forceBrowser bool) (string, error) {
	if forceBrowser {
		_, html, err := s.BrowserScrape(ctx, rawURL)
		if err != nil {
			return "", fmt.Errorf("browser fetch failed: %w", err)
		}
		return html, nil
	}
	html, err := s.Fetch(ctx, fetchURL)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	html, _ = s.MaybeBrowserFetch(ctx, fetchURL, html)
	return html, nil
}

// FetchLLMSTxt attempts to fetch /llms.txt from the given base URL. It only
// probes bare domains (path empty or "/") and returns the content and true on
// success. All errors are silently swallowed — this is a best-effort check.
func FetchLLMSTxt(ctx context.Context, baseURL string) (string, bool) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", false
	}
	if u.Path != "" && u.Path != "/" {
		return "", false
	}

	// Cap llms.txt probes at 5s — they're best-effort and shouldn't delay
	// the real scrape if a host ignores the request.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	llmsURL := u.Scheme + "://" + u.Host + "/llms.txt"
	req, err := http.NewRequestWithContext(probeCtx, "GET", llmsURL, nil)
	if err != nil {
		return "", false
	}
	resp, err := httpx.Default().Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		return "", false
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return "", false
	}
	return string(b), true
}
