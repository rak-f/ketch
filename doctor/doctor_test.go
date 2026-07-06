package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1broseidon/ketch/config"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

// --- searxng ---

func TestProbeSearxngOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("format param = %q, want json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"t","url":"u","content":"c"}]}`))
	}))
	defer ts.Close()

	status, detail := probeSearxng(testCtx(t), ts.Client(), ts.URL)
	if status != StatusOK {
		t.Fatalf("status = %q (detail %q), want ok", status, detail)
	}
}

func TestProbeSearxngFormatJSONBlocked(t *testing.T) {
	// Stock SearXNG returns 403 for format=json unless settings.yml enables
	// the json format — the #1 setup trap. Must be its own misconfigured
	// status with the settings.yml fix hint, not a generic unreachable.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "403 Forbidden", http.StatusForbidden)
	}))
	defer ts.Close()

	status, detail := probeSearxng(testCtx(t), ts.Client(), ts.URL)
	if status != StatusMisconfigured {
		t.Fatalf("status = %q, want misconfigured", status)
	}
	for _, want := range []string{"format=json", "settings.yml"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q should mention %q", detail, want)
		}
	}
}

func TestProbeSearxngNonJSONBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>hello</html>"))
	}))
	defer ts.Close()

	status, detail := probeSearxng(testCtx(t), ts.Client(), ts.URL)
	if status != StatusMisconfigured {
		t.Fatalf("status = %q (detail %q), want misconfigured", status, detail)
	}
	if !strings.Contains(detail, "SearXNG") {
		t.Errorf("detail %q should question the instance", detail)
	}
}

func TestProbeSearxngUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	url := ts.URL
	ts.Close() // nothing listening anymore

	status, _ := probeSearxng(testCtx(t), http.DefaultClient, url)
	if status != StatusUnreachable {
		t.Fatalf("status = %q, want unreachable", status)
	}
}

func TestProbeSearxngNoURL(t *testing.T) {
	status, _ := probeSearxng(testCtx(t), http.DefaultClient, "")
	if status != StatusMisconfigured {
		t.Fatalf("status = %q, want misconfigured", status)
	}
}

// --- brave ---

func TestProbeBraveNoKey(t *testing.T) {
	// Must classify without any network call: the handler fails the test.
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("no-key probe must not hit the network")
	}))
	defer ts.Close()

	status, detail := probeBrave(testCtx(t), ts.Client(), ts.URL, "")
	if status != StatusNoKey {
		t.Fatalf("status = %q, want no_key", status)
	}
	if !strings.Contains(detail, "brave_api_key") {
		t.Errorf("detail %q should carry the config hint", detail)
	}
}

func TestProbeFirecrawlNoKey(t *testing.T) {
	// Must classify without any network call: the handler fails the test.
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("no-key probe must not hit the network")
	}))
	defer ts.Close()

	status, detail := probeFirecrawl(testCtx(t), ts.Client(), ts.URL, "")
	if status != StatusNoKey {
		t.Fatalf("status = %q, want no_key", status)
	}
	if !strings.Contains(detail, "firecrawl_api_key") {
		t.Errorf("detail %q should carry the config hint", detail)
	}
}

func TestProbeFirecrawlStatuses(t *testing.T) {
	cases := []struct {
		name string
		code int
		want Status
	}{
		{"ok", http.StatusOK, StatusOK},
		{"invalid key", http.StatusUnauthorized, StatusMisconfigured},
		{"payment required", http.StatusPaymentRequired, StatusMisconfigured},
		{"rate limited", http.StatusTooManyRequests, StatusOK},
		{"server error", http.StatusBadGateway, StatusUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Method; got != http.MethodPost {
					t.Errorf("method = %q, want POST", got)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer k" {
					t.Errorf("Authorization = %q, want Bearer k", got)
				}
				w.WriteHeader(tc.code)
			}))
			defer ts.Close()

			status, _ := probeFirecrawl(testCtx(t), ts.Client(), ts.URL, "k")
			if status != tc.want {
				t.Fatalf("status = %q, want %q", status, tc.want)
			}
		})
	}
}

func TestProbeBraveStatuses(t *testing.T) {
	cases := []struct {
		name string
		code int
		want Status
	}{
		{"ok", http.StatusOK, StatusOK},
		{"invalid key", http.StatusUnauthorized, StatusMisconfigured},
		{"rate limited", http.StatusTooManyRequests, StatusOK},
		{"server error", http.StatusBadGateway, StatusUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-Subscription-Token"); got != "k" {
					t.Errorf("token header = %q, want k", got)
				}
				w.WriteHeader(tc.code)
			}))
			defer ts.Close()

			status, _ := probeBrave(testCtx(t), ts.Client(), ts.URL, "k")
			if status != tc.want {
				t.Fatalf("status = %q, want %q", status, tc.want)
			}
		})
	}
}

// --- ddg ---

func TestProbeDDGStatuses(t *testing.T) {
	cases := []struct {
		name string
		code int
		want Status
	}{
		{"ok", http.StatusOK, StatusOK},
		{"rate limited", http.StatusAccepted, StatusOK},
		{"blocked", http.StatusForbidden, StatusUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
			}))
			defer ts.Close()

			status, _ := probeDDG(testCtx(t), ts.Client(), ts.URL)
			if status != tc.want {
				t.Fatalf("status = %q, want %q", status, tc.want)
			}
		})
	}
}

// --- MCP endpoints (grepapp/exa) ---

func TestProbeMCP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer ts.Close()

	if status, detail := probeMCP(testCtx(t), ts.Client(), ts.URL, "grep.app"); status != StatusOK {
		t.Fatalf("status = %q (detail %q), want ok", status, detail)
	}
}

func TestProbeMCPServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	status, detail := probeMCP(testCtx(t), ts.Client(), ts.URL, "exa")
	if status != StatusUnreachable {
		t.Fatalf("status = %q, want unreachable", status)
	}
	if !strings.Contains(detail, "exa") {
		t.Errorf("detail %q should name the backend", detail)
	}
}

// --- sourcegraph reachability ---

func TestProbeReachable(t *testing.T) {
	cases := []struct {
		name string
		code int
		want Status
	}{
		{"ok", http.StatusOK, StatusOK},
		{"auth wall still reachable", http.StatusUnauthorized, StatusOK},
		{"server down", http.StatusBadGateway, StatusUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
			}))
			defer ts.Close()

			status, _ := probeReachable(testCtx(t), ts.Client(), ts.URL, "sourcegraph")
			if status != tc.want {
				t.Fatalf("status = %q, want %q", status, tc.want)
			}
		})
	}
}

// --- github ---

func TestProbeGitHubNoToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("no-token probe must not hit the network")
	}))
	defer ts.Close()

	status, detail := probeGitHub(testCtx(t), ts.Client(), ts.URL, func() (string, string) { return "", "none" })
	if status != StatusNoKey {
		t.Fatalf("status = %q, want no_key", status)
	}
	if !strings.Contains(detail, "gh auth login") {
		t.Errorf("detail %q should carry the resolution hint", detail)
	}
}

func TestProbeGitHubTokenAccepted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth header = %q", got)
		}
		if r.URL.Path != "/rate_limit" {
			t.Errorf("path = %q, want /rate_limit", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	status, detail := probeGitHub(testCtx(t), ts.Client(), ts.URL, func() (string, string) { return "tok", "gh-cli" })
	if status != StatusOK {
		t.Fatalf("status = %q, want ok", status)
	}
	if !strings.Contains(detail, "gh-cli") {
		t.Errorf("detail %q should report the token source", detail)
	}
}

func TestProbeGitHubTokenRejected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	status, _ := probeGitHub(testCtx(t), ts.Client(), ts.URL, func() (string, string) { return "bad", "env" })
	if status != StatusMisconfigured {
		t.Fatalf("status = %q, want misconfigured", status)
	}
}

// --- context7 ---

func TestProbeContext7(t *testing.T) {
	t.Run("no key", func(t *testing.T) {
		status, _ := probeContext7(testCtx(t), http.DefaultClient, "http://unused.invalid", "")
		if status != StatusNoKey {
			t.Fatalf("status = %q, want no_key", status)
		}
	})
	t.Run("key rejected", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer ts.Close()
		status, _ := probeContext7(testCtx(t), ts.Client(), ts.URL, "bad")
		if status != StatusMisconfigured {
			t.Fatalf("status = %q, want misconfigured", status)
		}
	})
	t.Run("ok", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer k" {
				t.Errorf("auth header = %q", got)
			}
			_, _ = w.Write([]byte(`{"results":[]}`))
		}))
		defer ts.Close()
		status, _ := probeContext7(testCtx(t), ts.Client(), ts.URL, "k")
		if status != StatusOK {
			t.Fatalf("status = %q, want ok", status)
		}
	})
}

// --- browser ---

func TestCheckBrowser(t *testing.T) {
	t.Run("unconfigured is a clean skip", func(t *testing.T) {
		status, _ := checkBrowser("")
		if status != StatusSkipped {
			t.Fatalf("status = %q, want skipped", status)
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		status, _ := checkBrowser(filepath.Join(t.TempDir(), "no-such-browser"))
		if status != StatusMisconfigured {
			t.Fatalf("status = %q, want misconfigured", status)
		}
	})
	t.Run("existing binary", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "chrome")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		status, detail := checkBrowser(bin)
		if status != StatusOK {
			t.Fatalf("status = %q (detail %q), want ok", status, detail)
		}
		if detail != bin {
			t.Errorf("detail = %q, want resolved path %q", detail, bin)
		}
	})
}

// --- exit gating: which checks are required ---

func findSpec(t *testing.T, specs []spec, surface, backend string) spec {
	t.Helper()
	for _, s := range specs {
		if s.surface == surface && s.backend == backend {
			return s
		}
	}
	t.Fatalf("spec %s/%s not found", surface, backend)
	return spec{}
}

func TestBuildSpecsRequiredGating(t *testing.T) {
	cfg := config.Defaults() // backend=brave, code=grepapp, docs=context7
	cfg.Backend = "searxng"
	specs := buildSpecs(&cfg, http.DefaultClient)

	if s := findSpec(t, specs, "search", "searxng"); !s.required {
		t.Error("default search backend must be required")
	}
	if s := findSpec(t, specs, "search", "brave"); s.required {
		t.Error("brave without a key and not default must be informational")
	}
	if s := findSpec(t, specs, "search", "ddg"); s.required {
		t.Error("ddg not default must be informational")
	}
	if s := findSpec(t, specs, "code", "grepapp"); !s.required {
		t.Error("default code backend must be required")
	}
	if s := findSpec(t, specs, "code", "github"); s.required {
		t.Error("github not default must be informational")
	}
	if s := findSpec(t, specs, "docs", "context7"); !s.required {
		t.Error("default docs backend must be required")
	}
	if s := findSpec(t, specs, "cache", "bbolt"); !s.required {
		t.Error("cache must always be required")
	}
	if s := findSpec(t, specs, "browser", "none"); s.required {
		t.Error("unconfigured browser must not gate the exit code")
	}
}

func TestBuildSpecsKeyedBackendIsRequired(t *testing.T) {
	cfg := config.Defaults()
	cfg.Backend = "ddg"
	cfg.BraveAPIKey = "k" // explicitly configured → a broken brave should gate
	cfg.Browser = "chrome"
	specs := buildSpecs(&cfg, http.DefaultClient)

	if s := findSpec(t, specs, "search", "brave"); !s.required {
		t.Error("brave with an explicit key must be required even when not default")
	}
	if s := findSpec(t, specs, "browser", "chrome"); !s.required {
		t.Error("configured browser must be required")
	}
}

func TestCheckBad(t *testing.T) {
	for status, want := range map[Status]bool{
		StatusOK:            false,
		StatusSkipped:       false,
		StatusNoKey:         true,
		StatusUnreachable:   true,
		StatusMisconfigured: true,
	} {
		if got := (Check{Status: status}).Bad(); got != want {
			t.Errorf("Bad(%q) = %v, want %v", status, got, want)
		}
	}
}
