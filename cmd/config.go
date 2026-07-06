package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/1broseidon/ketch/config"
	"github.com/1broseidon/ketch/urlrewrite"
	"github.com/spf13/cobra"
)

// configInfo is the discovery payload returned by `ketch config`.
// The *_set booleans report key presence only — key values are never printed.
// github_token_set follows the same resolution chain as github_token_source
// (config → $GITHUB_TOKEN/$GH_TOKEN → gh CLI): it is true iff the source is
// not "none".
type configInfo struct {
	ConfigPath            string            `json:"config_path"`
	Backend               string            `json:"backend"`
	SearxngURL            string            `json:"searxng_url"`
	BraveAPIKeySet        bool              `json:"brave_api_key_set"`
	ExaAPIKeySet          bool              `json:"exa_api_key_set"`
	FirecrawlAPIKeySet    bool              `json:"firecrawl_api_key_set"`
	Limit                 int               `json:"limit"`
	CacheTTL              string            `json:"cache_ttl"`
	Browser               string            `json:"browser,omitempty"`
	CodeBackend           string            `json:"code_backend"`
	DocsBackend           string            `json:"docs_backend"`
	Context7APIKeySet     bool              `json:"context7_api_key_set"`
	SourcegraphURL        string            `json:"sourcegraph_url"`
	GithubTokenSource     string            `json:"github_token_source"`
	GithubTokenSet        bool              `json:"github_token_set"`
	URLRewrites           []urlrewrite.Rule `json:"url_rewrites,omitempty"`
	SPAMarkers            []string          `json:"spa_markers,omitempty"`
	AvailableBackends     []string          `json:"available_backends"`
	AvailableCodeBackends []string          `json:"available_code_backends"`
	AvailableDocBackends  []string          `json:"available_doc_backends"`
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or manage configuration",
	Long:  `Display effective configuration as JSON, or manage the config file. The default output is a discovery payload showing all effective settings and available backends.`,
	RunE:  runConfigShow,
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a default config file",
	RunE:  runConfigInit,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value",
	Args:  exitArgs(cobra.ExactArgs(2)),
	RunE:  runConfigSet,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	RunE:  runConfigPath,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configPathCmd)
}

func runConfigShow(_ *cobra.Command, _ []string) error {
	c := config.Load()
	path, _ := config.Path()
	_, ghSource := c.ResolveGithubToken()

	info := configInfo{
		ConfigPath:            path,
		Backend:               c.Backend,
		SearxngURL:            c.SearxngURL,
		BraveAPIKeySet:        c.BraveAPIKey != "",
		ExaAPIKeySet:          c.ExaAPIKey != "",
		FirecrawlAPIKeySet:    c.FirecrawlAPIKey != "",
		Limit:                 c.Limit,
		CacheTTL:              c.CacheTTL,
		Browser:               c.Browser,
		CodeBackend:           c.CodeBackend,
		DocsBackend:           c.DocsBackend,
		Context7APIKeySet:     c.Context7APIKey != "",
		SourcegraphURL:        c.SourcegraphURL,
		GithubTokenSource:     ghSource,
		GithubTokenSet:        ghSource != "none",
		URLRewrites:           c.URLRewrites,
		SPAMarkers:            c.SPAMarkers,
		AvailableBackends:     config.AvailableBackends(),
		AvailableCodeBackends: config.AvailableCodeBackends(),
		AvailableDocBackends:  config.AvailableDocBackends(),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(info)
}

func runConfigInit(_ *cobra.Command, _ []string) error {
	path, err := config.Path()
	if err != nil {
		return err
	}

	if _, err := os.Stat(path); err == nil {
		return exitErrf(ExitPrecondition, "config already exists: %s", path)
	}

	if err := config.Save(config.Defaults()); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "created %s\n", path)
	return nil
}

func runConfigSet(_ *cobra.Command, args []string) error {
	c := config.Load()
	key, value := args[0], args[1]

	if err := applyConfigSet(&c, key, value); err != nil {
		return err
	}

	if err := config.Save(c); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "set %s = %s\n", key, value)
	return nil
}

// applyConfigSet is a flat key→field dispatch; its cyclomatic complexity scales
// with the number of config keys, not with any real branching depth.
//
//nolint:gocyclo // one arm per config key; splitting it would obscure, not clarify
func applyConfigSet(c *config.Config, key, value string) error {
	switch key {
	case "backend":
		c.Backend = value
	case "searxng_url":
		c.SearxngURL = value
	case "brave_api_key":
		c.BraveAPIKey = value
	case "exa_api_key":
		c.ExaAPIKey = value
	case "firecrawl_api_key":
		c.FirecrawlAPIKey = value
	case "limit":
		return setLimit(c, value)
	case "cache_ttl":
		return setCacheTTL(c, value)
	case "browser":
		c.Browser = value
	case "code_backend":
		c.CodeBackend = value
	case "docs_backend":
		c.DocsBackend = value
	case "context7_api_key":
		c.Context7APIKey = value
	case "sourcegraph_url":
		c.SourcegraphURL = value
	case "github_token":
		c.GithubToken = value
	case "url_rewrites":
		return setURLRewrites(c, value)
	case "spa_markers":
		return setSPAMarkers(c, value)
	default:
		return exitErrf(ExitValidation, "unknown key: %s (valid: backend, searxng_url, brave_api_key, exa_api_key, firecrawl_api_key, limit, cache_ttl, browser, code_backend, docs_backend, context7_api_key, sourcegraph_url, github_token, url_rewrites, spa_markers)", key)
	}
	return nil
}

func setLimit(c *config.Config, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return exitErrf(ExitValidation, "limit must be an integer: %w", err)
	}
	c.Limit = n
	return nil
}

func setCacheTTL(c *config.Config, value string) error {
	if _, err := time.ParseDuration(value); err != nil {
		return exitErrf(ExitValidation, "cache_ttl must be a duration (e.g. 1h, 30m): %w", err)
	}
	c.CacheTTL = value
	return nil
}

func setURLRewrites(c *config.Config, value string) error {
	var rules []urlrewrite.Rule
	if err := json.Unmarshal([]byte(value), &rules); err != nil {
		return exitErrf(ExitValidation, "url_rewrites must be a JSON array of {match, replace}: %w", err)
	}
	if _, err := urlrewrite.NewRewriter(rules); err != nil {
		return exitErrf(ExitValidation, "%w", err)
	}
	c.URLRewrites = rules
	return nil
}

// setSPAMarkers parses a JSON array of substrings that, when found in a page's
// HTML, mark it as a JS-rendered shell needing browser rendering (matched
// alongside the built-in markers). An empty array ([]) clears the list. Blank
// markers are rejected — a "" marker would match every page.
func setSPAMarkers(c *config.Config, value string) error {
	var markers []string
	if err := json.Unmarshal([]byte(value), &markers); err != nil {
		return exitErrf(ExitValidation, "spa_markers must be a JSON array of strings: %w", err)
	}
	for i, m := range markers {
		if strings.TrimSpace(m) == "" {
			return exitErrf(ExitValidation, "spa_markers[%d] is blank; markers must be non-empty substrings", i)
		}
	}
	c.SPAMarkers = markers
	return nil
}

func runConfigPath(_ *cobra.Command, _ []string) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
