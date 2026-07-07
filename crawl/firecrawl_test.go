package crawl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fcRewriteTransport redirects requests to a test server while preserving the
// request path, so the const Firecrawl endpoints (and absolute `next` cursors)
// resolve to the httptest server.
type fcRewriteTransport struct{ target string }

func (t *fcRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = t.target[len("http://"):]
	return http.DefaultTransport.RoundTrip(req)
}

func TestCrawlFirecrawlStreamsAndDedups(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	mux := http.NewServeMux()
	// Start: return the job id.
	mux.HandleFunc("/v2/crawl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("start method = %q, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"id":"job-1"}`)
	})
	// Status page 1: still scraping, one doc, a `next` cursor to page 2.
	mux.HandleFunc("/v2/crawl/job-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"status":"scraping","next":%q,"data":[
			{"markdown":"one","metadata":{"title":"One","sourceURL":"https://site.com/a"}}
		]}`, server.URL+"/v2/crawl/job-1/p2")
	})
	// Status page 2: completed, includes the page-1 doc again (must be deduped)
	// plus a new one.
	mux.HandleFunc("/v2/crawl/job-1/p2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"status":"completed","data":[
			{"markdown":"one","metadata":{"title":"One","sourceURL":"https://site.com/a"}},
			{"markdown":"two","metadata":{"title":"Two","sourceURL":"https://site.com/b"}}
		]}`)
	})
	server = httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Transport: &fcRewriteTransport{target: server.URL}}
	var mu sync.Mutex
	var got []Result
	err := crawlFirecrawl(context.Background(), client, "https://site.com", "k", Options{Depth: 2}, func(r Result) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("crawlFirecrawl error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("emitted %d results, want 2 (deduped): %+v", len(got), got)
	}
	if got[0].URL != "https://site.com/a" || got[0].Page.Markdown != "one" {
		t.Errorf("result[0] = %+v", got[0])
	}
	if got[1].URL != "https://site.com/b" || got[1].Page.Title != "Two" {
		t.Errorf("result[1] = %+v", got[1])
	}
	for _, r := range got {
		if r.Source != "firecrawl" || r.Status != "new" {
			t.Errorf("result %q: source=%q status=%q, want firecrawl/new", r.URL, r.Source, r.Status)
		}
	}
}

func TestCrawlFirecrawlRequestShape(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/crawl", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["url"] != "https://site.com" {
			t.Errorf("url = %v", body["url"])
		}
		if body["maxDiscoveryDepth"] != float64(3) {
			t.Errorf("maxDiscoveryDepth = %v, want 3", body["maxDiscoveryDepth"])
		}
		if body["limit"] != float64(25) {
			t.Errorf("limit = %v, want 25", body["limit"])
		}
		if body["integration"] != "_ketch" {
			t.Errorf("integration = %v, want _ketch", body["integration"])
		}
		inc, _ := body["includePaths"].([]any)
		if len(inc) != 1 || inc[0] != "/docs" {
			t.Errorf("includePaths = %v, want [/docs]", body["includePaths"])
		}
		exc, _ := body["excludePaths"].([]any)
		if len(exc) != 1 || exc[0] != "/admin" {
			t.Errorf("excludePaths = %v, want [/admin]", body["excludePaths"])
		}
		so, _ := body["scrapeOptions"].(map[string]any)
		formats, _ := so["formats"].([]any)
		if len(formats) != 1 || formats[0] != "markdown" {
			t.Errorf("scrapeOptions.formats = %v, want [markdown]", so["formats"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"id":"job-2"}`)
	})
	mux.HandleFunc("/v2/crawl/job-2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"status":"completed","data":[]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Transport: &fcRewriteTransport{target: server.URL}}
	opts := Options{Depth: 3, Limit: 25, Allow: []string{"/docs"}, Deny: []string{"/admin"}}
	if err := crawlFirecrawl(context.Background(), client, "https://site.com", "test-key", opts, func(Result) {}); err != nil {
		t.Fatalf("crawlFirecrawl error: %v", err)
	}
}

func TestCrawlFirecrawlDepthZeroSent(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/crawl", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		// Depth 0 (seed-only) must be sent explicitly: omitting maxDiscoveryDepth
		// would make Firecrawl run an unbounded default-depth crawl.
		v, ok := body["maxDiscoveryDepth"]
		if !ok {
			t.Fatal("maxDiscoveryDepth missing; depth 0 must be sent, not omitted")
		}
		if v != float64(0) {
			t.Errorf("maxDiscoveryDepth = %v, want 0", v)
		}
		fmt.Fprint(w, `{"success":true,"id":"job-d0"}`)
	})
	mux.HandleFunc("/v2/crawl/job-d0", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"status":"completed","data":[]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Transport: &fcRewriteTransport{target: server.URL}}
	if err := crawlFirecrawl(context.Background(), client, "https://site.com", "k", Options{Depth: 0}, func(Result) {}); err != nil {
		t.Fatalf("crawlFirecrawl error: %v", err)
	}
}

func TestCrawlFirecrawlToleratesTransientPollError(t *testing.T) {
	// Not parallel: shortens the shared poll interval to keep the retry fast.
	orig := firecrawlCrawlPollInterval
	firecrawlCrawlPollInterval = 5 * time.Millisecond
	defer func() { firecrawlCrawlPollInterval = orig }()

	var calls int
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/crawl", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"id":"job-t"}`)
	})
	mux.HandleFunc("/v2/crawl/job-t", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			http.Error(w, "bad gateway", http.StatusBadGateway) // transient blip
			return
		}
		fmt.Fprint(w, `{"success":true,"status":"completed","data":[
			{"markdown":"ok","metadata":{"title":"Ok","sourceURL":"https://site.com/x"}}
		]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Transport: &fcRewriteTransport{target: server.URL}}
	var got []Result
	err := crawlFirecrawl(context.Background(), client, "https://site.com", "k", Options{Depth: 1}, func(r Result) {
		got = append(got, r)
	})
	if err != nil {
		t.Fatalf("crawlFirecrawl should tolerate a transient 502, got: %v", err)
	}
	if len(got) != 1 || got[0].URL != "https://site.com/x" {
		t.Fatalf("expected 1 page after retry, got %+v", got)
	}
}

func TestCrawlFirecrawlFailedStatus(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/crawl", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"id":"job-3"}`)
	})
	mux.HandleFunc("/v2/crawl/job-3", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"status":"failed","data":[]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Transport: &fcRewriteTransport{target: server.URL}}
	err := crawlFirecrawl(context.Background(), client, "https://site.com", "k", Options{}, func(Result) {})
	if err == nil {
		t.Fatal("expected error for failed crawl status")
	}
}

func TestCrawlFirecrawlInvalidKey(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/crawl", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Transport: &fcRewriteTransport{target: server.URL}}
	err := crawlFirecrawl(context.Background(), client, "https://site.com", "bad", Options{}, func(Result) {})
	if err == nil {
		t.Fatal("expected error for 401 start response")
	}
}
