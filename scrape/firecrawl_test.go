package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFirecrawlScrapeMarkdownParses(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"markdown":"# Example Domain\n\nBody text.","metadata":{"title":"Example Domain","sourceURL":"https://example.com"}}}`)
	}))
	defer server.Close()

	f := &firecrawlClient{apiKey: "k", client: &http.Client{Transport: &fcRewriteTransport{target: server.URL}}}
	doc, err := f.scrape(context.Background(), "https://example.com", false)
	if err != nil {
		t.Fatalf("scrape error: %v", err)
	}
	if doc.Title != "Example Domain" {
		t.Errorf("title = %q, want %q", doc.Title, "Example Domain")
	}
	if !strings.HasPrefix(doc.Markdown, "# Example Domain") {
		t.Errorf("markdown = %q, want it to start with the heading", doc.Markdown)
	}
}

func TestFirecrawlScrapeRawRequestsRawHTML(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		formats, _ := body["formats"].([]any)
		if len(formats) != 1 || formats[0] != "rawHtml" {
			t.Errorf("formats = %v, want [rawHtml]", body["formats"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"rawHtml":"<html><body>hi</body></html>","metadata":{"title":"T"}}}`)
	}))
	defer server.Close()

	f := &firecrawlClient{apiKey: "k", client: &http.Client{Transport: &fcRewriteTransport{target: server.URL}}}
	doc, err := f.scrape(context.Background(), "https://example.com", true)
	if err != nil {
		t.Fatalf("scrape error: %v", err)
	}
	if doc.RawHTML != "<html><body>hi</body></html>" {
		t.Errorf("rawHTML = %q", doc.RawHTML)
	}
}

func TestFirecrawlScrapeRequestShape(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["url"] != "https://go.dev" {
			t.Errorf("url = %v, want https://go.dev", body["url"])
		}
		if body["integration"] != "_ketch" {
			t.Errorf("integration = %v, want _ketch", body["integration"])
		}
		if formats, _ := body["formats"].([]any); len(formats) != 1 || formats[0] != "markdown" {
			t.Errorf("formats = %v, want [markdown]", body["formats"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"markdown":"x","metadata":{"title":"t"}}}`)
	}))
	defer server.Close()

	f := &firecrawlClient{apiKey: "test-key", client: &http.Client{Transport: &fcRewriteTransport{target: server.URL}}}
	if _, err := f.scrape(context.Background(), "https://go.dev", false); err != nil {
		t.Fatalf("scrape error: %v", err)
	}
}

func TestFirecrawlScrapeInvalidKey(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	f := &firecrawlClient{apiKey: "bad", client: &http.Client{Transport: &fcRewriteTransport{target: server.URL}}}
	_, err := f.scrape(context.Background(), "https://example.com", false)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "firecrawl_api_key") {
		t.Errorf("error %q should carry the config hint", err.Error())
	}
}

func TestFirecrawlScrapeServerError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	f := &firecrawlClient{apiKey: "k", client: &http.Client{Transport: &fcRewriteTransport{target: server.URL}}}
	if _, err := f.scrape(context.Background(), "https://example.com", false); err == nil {
		t.Fatal("expected error for 500 response")
	}
}

// fcRewriteTransport redirects requests to a test server while preserving the
// request path, mirroring the search package's test transport.
type fcRewriteTransport struct{ target string }

func (t *fcRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = t.target[len("http://"):]
	return http.DefaultTransport.RoundTrip(req)
}
