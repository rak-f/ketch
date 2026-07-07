package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/1broseidon/ketch/httpx"
)

// firecrawlScrapeEndpoint is the Firecrawl v2 scrape API. See
// https://docs.firecrawl.dev/api-reference/endpoint/scrape.
const firecrawlScrapeEndpoint = "https://api.firecrawl.dev/v2/scrape"

// firecrawlClient fetches pages via the Firecrawl v2 scrape API instead of the
// local HTTP-fetch + readability pipeline. Firecrawl renders JavaScript
// server-side and returns clean markdown (or raw HTML), so it needs no local
// browser. Selected per operator config via scrape_backend=firecrawl; see
// NewFromConfig, which is the sole constructor of this client.
type firecrawlClient struct {
	apiKey string
	client *http.Client
}

// newFirecrawlClient builds a Firecrawl scrape client. The API key is the same
// firecrawl_api_key the search backend uses.
func newFirecrawlClient(apiKey string) *firecrawlClient {
	return &firecrawlClient{apiKey: apiKey, client: httpx.Default()}
}

type firecrawlScrapeRequest struct {
	URL         string   `json:"url"`
	Formats     []string `json:"formats"`
	Integration string   `json:"integration,omitempty"`
}

type firecrawlScrapeResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Markdown string `json:"markdown"`
		RawHTML  string `json:"rawHtml"`
		Metadata struct {
			Title string `json:"title"`
		} `json:"metadata"`
	} `json:"data"`
}

// firecrawlDoc is the extracted result of a single Firecrawl scrape.
type firecrawlDoc struct {
	Markdown string
	RawHTML  string
	Title    string
}

// scrape fetches fetchURL via Firecrawl. When wantRaw is true it requests the
// rawHtml format (for `--raw`); otherwise it requests markdown. Error handling
// mirrors the Firecrawl search backend so the CLI/MCP error taxonomy stays
// consistent across surfaces.
func (f *firecrawlClient) scrape(ctx context.Context, fetchURL string, wantRaw bool) (*firecrawlDoc, error) {
	format := "markdown"
	if wantRaw {
		format = "rawHtml"
	}

	body, err := json.Marshal(firecrawlScrapeRequest{
		URL:         fetchURL,
		Formats:     []string{format},
		Integration: "_ketch",
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, firecrawlScrapeEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.apiKey)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired {
		return nil, fmt.Errorf("firecrawl: invalid API key (set via: ketch config set firecrawl_api_key <key>)")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("firecrawl: rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, firecrawlScrapeStatusError(resp)
	}

	var fr firecrawlScrapeResponse
	if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
		return nil, fmt.Errorf("failed to decode firecrawl response: %w", err)
	}

	return &firecrawlDoc{
		Markdown: fr.Data.Markdown,
		RawHTML:  fr.Data.RawHTML,
		Title:    fr.Data.Metadata.Title,
	}, nil
}

func firecrawlScrapeStatusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if detail := strings.TrimSpace(string(body)); detail != "" {
		return fmt.Errorf("firecrawl returned status %d: %s", resp.StatusCode, detail)
	}
	return fmt.Errorf("firecrawl returned status %d", resp.StatusCode)
}
