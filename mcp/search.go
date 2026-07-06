package mcp

import (
	"context"

	"github.com/1broseidon/ketch/extract"
	"github.com/1broseidon/ketch/search"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SearchInput is the input schema for the "search" tool. It mirrors the
// per-invocation flags of `ketch search`; config-level settings (API keys,
// default backend/limit) stay operator-configured.
type SearchInput struct {
	Query      string `json:"query" jsonschema:"the search query"`
	Backend    string `json:"backend,omitempty" jsonschema:"search backend: brave, ddg, searxng, exa, or firecrawl (default: the configured backend)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"max number of results (default: the configured limit)"`
	SearxngURL string `json:"searxng_url,omitempty" jsonschema:"override the configured SearXNG instance URL (searxng backend only)"`
	Scrape     bool   `json:"scrape,omitempty" jsonschema:"also fetch each result URL and fill its content field with extracted markdown"`
	Trim       bool   `json:"trim,omitempty" jsonschema:"strip markdown formatting from scraped content, keep text only (with scrape)"`
	MaxChars   int    `json:"max_chars,omitempty" jsonschema:"truncate each result's scraped content to N characters (with scrape; 0 = disabled)"`
}

// SearchOutput is the output schema for the "search" tool. Results carries
// the same result objects as the CLI's `ketch search --json` (which emits
// them as a bare array; MCP structured content needs the object wrapper).
type SearchOutput struct {
	Results []search.Result `json:"results"`
}

func (s *Server) registerSearchTool() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name: "search",
		Description: "Search the web using Brave, DuckDuckGo, SearXNG, or Exa (default: the configured backend) and return results (title, url, description). " +
			"Set scrape=true to also fetch each result and include its content as markdown." + errTaxonomy,
		Annotations: readOnlyOpenWorld(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in SearchInput) (*mcpsdk.CallToolResult, SearchOutput, error) {
		if in.Query == "" {
			return nil, SearchOutput{}, errf(kindValidation, "query is required")
		}
		backend := in.Backend
		if backend == "" {
			backend = s.cfg.Backend
		}
		limit := in.Limit
		if limit <= 0 {
			limit = s.cfg.Limit
		}

		searcher, err := search.NewFromConfig(s.cfg, backend, in.SearxngURL)
		if err != nil {
			return nil, SearchOutput{}, backendErrf(err, search.ErrUnknownBackend)
		}

		results, err := searcher.Search(ctx, in.Query, limit)
		if err != nil {
			return nil, SearchOutput{}, upstreamErrf(err, "search failed")
		}

		if in.Scrape {
			s.scrapeSearchResults(ctx, results, in.Trim, in.MaxChars)
		}

		return nil, SearchOutput{Results: results}, nil
	})
}

// scrapeSearchResults fills each result's Content with extracted markdown,
// like `ketch search --scrape`. Individual fetch failures leave that
// result's content empty rather than failing the whole call.
func (s *Server) scrapeSearchResults(ctx context.Context, results []search.Result, trim bool, maxChars int) {
	pc := s.pageCache(false)
	for i, r := range results {
		page, err := s.scraper.CachedScrape(ctx, pc, r.URL)
		if err != nil {
			continue
		}
		if page.FetchedURL != "" {
			results[i].FetchedURL = page.FetchedURL
		}
		results[i].Content = extract.PostProcess(page.Markdown, trim, maxChars)
	}
}
