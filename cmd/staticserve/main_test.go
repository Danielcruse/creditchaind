package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newTestWebsite(t *testing.T) http.Dir {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":    "<h1>home</h1>",
		"wallet.html":   "<h1>wallet</h1>",
		"css/style.css": "body{}",
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return http.Dir(dir)
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	root := newTestWebsite(t)
	handler := cleanURLHandler(root, http.FileServer(root))
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestCleanURLHandler_ServesExtensionLessPage(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + "/wallet")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /wallet, got %d", resp.StatusCode)
	}
}

func TestCleanURLHandler_RedirectsOldHtmlURL(t *testing.T) {
	srv := newTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Get(srv.URL + "/wallet.html")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("expected 301 for /wallet.html, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/wallet" {
		t.Fatalf("expected redirect to /wallet, got %q", loc)
	}
}

func TestCleanURLHandler_RedirectsIndexHtmlToRoot(t *testing.T) {
	srv := newTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Get(srv.URL + "/index.html")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("expected 301 for /index.html, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}
}

func TestCleanURLHandler_RedirectPreservesQueryString(t *testing.T) {
	srv := newTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Get(srv.URL + "/wallet.html?tab=history")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "/wallet?tab=history" {
		t.Fatalf("expected redirect to /wallet?tab=history, got %q", loc)
	}
}

func TestCleanURLHandler_StaticAssetsUnaffected(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + "/css/style.css")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for a real static asset, got %d", resp.StatusCode)
	}
}

func TestCleanURLHandler_RootServesIndexDirectly(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d", resp.StatusCode)
	}
}

func TestCleanURLHandler_UnknownExtensionLessPathIs404(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + "/nonexistent-page")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for a page that doesn't exist, got %d", resp.StatusCode)
	}
}

func newTestServerWithNoCache(t *testing.T) *httptest.Server {
	t.Helper()
	root := newTestWebsite(t)
	handler := noCacheHandler(cleanURLHandler(root, http.FileServer(root)))
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// TestNoCacheHandler_SetsRevalidateHeader is the regression test for
// the real gap this closes: a deployed change (network-config.js's
// mainnet-defaulting fix, 2026-08-22) sat invisible in already-cached
// browsers - including real visitors to service.creditchain.site -
// since this server previously sent no Cache-Control at all.
func TestNoCacheHandler_SetsRevalidateHeader(t *testing.T) {
	srv := newTestServerWithNoCache(t)

	resp, err := http.Get(srv.URL + "/wallet")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("expected Cache-Control: no-cache, got %q", got)
	}
}

// TestNoCacheHandler_StillAnswersConditionalRequestWith304 proves
// no-cache ("may store, must revalidate") doesn't defeat
// http.FileServer's existing conditional-GET support the way no-store
// would - an unchanged file still answers with a cheap, bodyless 304
// rather than a full re-download every single load.
func TestNoCacheHandler_StillAnswersConditionalRequestWith304(t *testing.T) {
	srv := newTestServerWithNoCache(t)

	first, err := http.Get(srv.URL + "/css/style.css")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	defer first.Body.Close()
	lastModified := first.Header.Get("Last-Modified")
	if lastModified == "" {
		t.Fatalf("setup issue: expected http.FileServer to set Last-Modified")
	}

	req, err := http.NewRequest("GET", srv.URL+"/css/style.css", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("If-Modified-Since", lastModified)

	second, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional request failed: %v", err)
	}
	defer second.Body.Close()

	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("expected 304 for a conditional request against an unchanged file, got %d", second.StatusCode)
	}
	if got := second.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("expected the 304 itself to still carry Cache-Control: no-cache, got %q", got)
	}
}
