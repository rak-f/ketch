package crawl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/1broseidon/ketch/scrape"
)

// firecrawlCrawlEndpoint is the Firecrawl v2 crawl API. A POST starts an async
// crawl job; GET <endpoint>/{id} polls its status and results. See
// https://docs.firecrawl.dev/api-reference/endpoint/crawl-post.
const firecrawlCrawlEndpoint = "https://api.firecrawl.dev/v2/crawl"

// firecrawlCrawlPollInterval is how long to wait between status polls of an
// in-progress crawl job. A var (not const) so tests can shorten it.
var firecrawlCrawlPollInterval = 2 * time.Second

// maxCrawlPollErrors is how many consecutive status-poll failures to tolerate
// before giving up. Firecrawl's status endpoint can return a transient 5xx
// while a large job is processing; a single blip shouldn't abort a crawl that
// is otherwise progressing.
const maxCrawlPollErrors = 3

type firecrawlCrawlRequest struct {
	URL string `json:"url"`
	// Limit caps total pages; omitted (0) lets Firecrawl apply its own default.
	Limit int `json:"limit,omitempty"`
	// MaxDiscoveryDepth is sent unconditionally (no omitempty): ketch's Depth==0
	// means "seed only", which Firecrawl also honors as maxDiscoveryDepth 0 —
	// omitting it would instead trigger an unbounded default-depth crawl.
	MaxDiscoveryDepth int                         `json:"maxDiscoveryDepth"`
	IncludePaths      []string                    `json:"includePaths,omitempty"`
	ExcludePaths      []string                    `json:"excludePaths,omitempty"`
	ScrapeOptions     firecrawlCrawlScrapeOptions `json:"scrapeOptions"`
	Integration       string                      `json:"integration,omitempty"`
}

type firecrawlCrawlScrapeOptions struct {
	Formats []string `json:"formats"`
}

type firecrawlCrawlStartResponse struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
}

type firecrawlCrawlDoc struct {
	Markdown string `json:"markdown"`
	Metadata struct {
		Title     string `json:"title"`
		SourceURL string `json:"sourceURL"`
		URL       string `json:"url"`
	} `json:"metadata"`
}

type firecrawlCrawlStatusResponse struct {
	Success bool                `json:"success"`
	Status  string              `json:"status"`
	Next    string              `json:"next"`
	Data    []firecrawlCrawlDoc `json:"data"`
}

// crawlFirecrawl runs a whole-site crawl through the Firecrawl v2 crawl API and
// streams each scraped page to fn as a Result — the same callback the local BFS
// crawler uses, so cmd/ and mcp/ consume both backends identically. It starts
// the async job, then polls until the job reaches a terminal state, emitting
// newly-arrived pages (deduplicated by URL) as they appear. Context
// cancellation stops the polling promptly.
//
// opts maps to Firecrawl crawl params: Depth→maxDiscoveryDepth, Limit→limit
// (0 = Firecrawl's own default), Deny→excludePaths (both regex), Allow→
// includePaths (Firecrawl treats these as regexes; a plain path substring
// works as one). The local-only Concurrency knob doesn't apply — Firecrawl
// parallelizes server-side.
func crawlFirecrawl(ctx context.Context, client *http.Client, seed, apiKey string, opts Options, fn func(Result)) error {
	id, err := firecrawlStartCrawl(ctx, client, apiKey, seed, opts)
	if err != nil {
		return err
	}

	statusURL := firecrawlCrawlEndpoint + "/" + id
	seen := make(map[string]bool)
	pollErrs := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := firecrawlDrain(ctx, client, apiKey, statusURL, seen, fn)
		if err != nil {
			// Context cancellation is terminal; a transient upstream error is
			// retried a bounded number of times before giving up.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			pollErrs++
			if pollErrs >= maxCrawlPollErrors {
				return err
			}
		} else {
			pollErrs = 0
			switch status {
			case "completed":
				return nil
			case "failed", "cancelled":
				return fmt.Errorf("firecrawl crawl %s: %s", id, status)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(firecrawlCrawlPollInterval):
		}
	}
}

// firecrawlStartCrawl submits the crawl job and returns its ID.
func firecrawlStartCrawl(ctx context.Context, client *http.Client, apiKey, seed string, opts Options) (string, error) {
	reqBody, err := json.Marshal(firecrawlCrawlRequest{
		URL:               seed,
		Limit:             opts.Limit,
		MaxDiscoveryDepth: opts.Depth,
		IncludePaths:      opts.Allow,
		ExcludePaths:      opts.Deny,
		ScrapeOptions:     firecrawlCrawlScrapeOptions{Formats: []string{"markdown"}},
		Integration:       "_ketch",
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, firecrawlCrawlEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("firecrawl request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := firecrawlCrawlStatusErr(resp); err != nil {
		return "", err
	}

	var start firecrawlCrawlStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&start); err != nil {
		return "", fmt.Errorf("failed to decode firecrawl crawl response: %w", err)
	}
	if start.ID == "" {
		return "", fmt.Errorf("firecrawl crawl: no job id in response")
	}
	return start.ID, nil
}

// firecrawlDrain walks the paginated status/results for a crawl job, emitting
// every not-yet-seen page via fn, and returns the job's current status. It
// follows the response's `next` cursor to drain all currently-available
// results in one pass; the seen set makes repeated passes (across poll cycles)
// idempotent.
//
// The base status URL carries only summary metadata; the actual scraped pages
// live under its `next` cursor (e.g. ?skip=0), so following `next` is required
// to see any data. An in-progress job's `next` cursor points back at itself
// once the caller is caught up, so the visited set stops us from re-fetching
// the same cursor and spinning. Fresh results that arrive later are picked up
// on the next poll cycle (and deduped via seen).
func firecrawlDrain(ctx context.Context, client *http.Client, apiKey, statusURL string, seen map[string]bool, fn func(Result)) (string, error) {
	status := ""
	visited := make(map[string]bool)
	for next := statusURL; next != "" && !visited[next]; {
		if err := ctx.Err(); err != nil {
			return status, err
		}
		visited[next] = true
		page, err := firecrawlGetCrawl(ctx, client, apiKey, next)
		if err != nil {
			return status, err
		}
		status = page.Status
		for i := range page.Data {
			d := &page.Data[i]
			u := d.Metadata.SourceURL
			if u == "" {
				u = d.Metadata.URL
			}
			if u == "" || seen[u] {
				continue
			}
			seen[u] = true
			fn(Result{
				Page:   &scrape.Page{URL: u, Title: d.Metadata.Title, Markdown: d.Markdown},
				Status: "new",
				Source: "firecrawl",
				URL:    u,
			})
		}
		next = page.Next
	}
	return status, nil
}

// firecrawlGetCrawl fetches one page of a crawl job's status/results.
func firecrawlGetCrawl(ctx context.Context, client *http.Client, apiKey, statusURL string) (*firecrawlCrawlStatusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := firecrawlCrawlStatusErr(resp); err != nil {
		return nil, err
	}

	var status firecrawlCrawlStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode firecrawl crawl status: %w", err)
	}
	return &status, nil
}

// firecrawlCrawlStatusErr classifies non-2xx crawl responses, mirroring the
// scrape/search backends so the CLI/MCP error taxonomy stays consistent.
func firecrawlCrawlStatusErr(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusPaymentRequired:
		return fmt.Errorf("firecrawl: invalid API key (set via: ketch config set firecrawl_api_key <key>)")
	case http.StatusTooManyRequests:
		return fmt.Errorf("firecrawl: rate limited")
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if detail := strings.TrimSpace(string(body)); detail != "" {
			return fmt.Errorf("firecrawl returned status %d: %s", resp.StatusCode, detail)
		}
		return fmt.Errorf("firecrawl returned status %d", resp.StatusCode)
	}
}
