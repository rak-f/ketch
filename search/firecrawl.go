package search

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

// firecrawlSearchEndpoint is the Firecrawl v2 search API. See
// https://docs.firecrawl.dev/api-reference/endpoint/search.
const firecrawlSearchEndpoint = "https://api.firecrawl.dev/v2/search"

// Firecrawl searches the web via the Firecrawl v2 search API.
type Firecrawl struct {
	apiKey string
	client *http.Client
}

// NewFirecrawl creates a new Firecrawl search backend.
func NewFirecrawl(apiKey string) *Firecrawl {
	return &Firecrawl{
		apiKey: apiKey,
		client: httpx.Default(),
	}
}

type firecrawlRequest struct {
	Query       string `json:"query"`
	Limit       int    `json:"limit"`
	Integration string `json:"integration,omitempty"`
}

type firecrawlResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Web []firecrawlResult `json:"web"`
	} `json:"data"`
}

type firecrawlResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// Search queries Firecrawl and returns up to limit web results.
func (f *Firecrawl) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if limit <= 0 {
		return []Result{}, nil
	}

	body, err := json.Marshal(firecrawlRequest{
		Query:       query,
		Limit:       limit,
		Integration: "_ketch",
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", firecrawlSearchEndpoint, bytes.NewReader(body))
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
		return nil, firecrawlStatusError(resp)
	}

	var fr firecrawlResponse
	if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
		return nil, fmt.Errorf("failed to decode firecrawl response: %w", err)
	}

	results := make([]Result, 0, limit)
	for _, r := range fr.Data.Web {
		if len(results) >= limit {
			break
		}
		if r.URL == "" {
			continue
		}
		results = append(results, Result{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Description,
		})
	}

	return results, nil
}

func firecrawlStatusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if detail := strings.TrimSpace(string(body)); detail != "" {
		return fmt.Errorf("firecrawl returned status %d: %s", resp.StatusCode, detail)
	}
	return fmt.Errorf("firecrawl returned status %d", resp.StatusCode)
}
