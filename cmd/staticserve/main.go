// staticserve is the plain static-file server for website/ - separate
// from the creditchaind blockchain node (api.StartServer), matching
// the two-systemd-service split already running in production
// (creditchain.service = the node, creditchain-web.service = this).
//
// Serves clean, extension-less URLs (e.g. /wallet instead of
// /wallet.html) and 301-redirects the old *.html form to it - see
// cleanURLHandler below.
package main

import (
	"log"
	"net/http"
	"os"
	"path"
	"strings"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	dir := os.Getenv("WEBSITE_DIR")
	if dir == "" {
		dir = "website"
	}

	root := http.Dir(dir)
	handler := noCacheHandler(cleanURLHandler(root, http.FileServer(root)))

	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	if certFile != "" && keyFile != "" {
		log.Printf("staticserve: serving %s on :%s (TLS)\n", dir, port)
		log.Fatal(http.ListenAndServeTLS(":"+port, certFile, keyFile, handler))
		return
	}

	log.Println("WARNING: TLS_CERT_FILE/TLS_KEY_FILE not set - serving plain HTTP. Fine for local dev, not for a real deployment (terminate TLS here or at a reverse proxy in front of it).")
	log.Printf("staticserve: serving %s on :%s\n", dir, port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

// noCacheHandler forces every response to revalidate with the server
// before a browser reuses a cached copy (Cache-Control: no-cache means
// "may store, must revalidate" - NOT "don't cache at all", that's
// no-store). http.FileServer already sets Last-Modified from each
// file's mtime and answers a conditional If-Modified-Since request with
// a bodyless 304, so this doesn't cost a full re-download on an
// unchanged file - it only guarantees the browser actually ASKS the
// server first, rather than silently trusting a stale disk copy for
// however long its own heuristic-caching guess decided was safe. Found
// necessary the hard way: this server sent no Cache-Control at all,
// which let a real deploy (network-config.js's mainnet-defaulting fix,
// 2026-08-22) sit invisible in already-cached browsers - including
// service.creditchain.site's very own real visitors - until they
// happened to hard-refresh. The existing "?v=xxx" cache-busting
// convention on script/style tags stays the right tool for a LONG,
// cacheable freshness window (CDN-friendly, zero server cost); this is
// the backstop for whenever that discipline gets missed on one file, as
// it did here.
func noCacheHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// cleanURLHandler lets every page under root be reached without its
// .html extension (e.g. /wallet instead of /wallet.html), and
// permanently redirects the old .html URL to the clean one so existing
// bookmarks/search-engine links still resolve instead of 404ing
// outright. index.html collapses to the site root ("/") rather than
// "/index", matching how every internal link on the site already
// treats the homepage.
func cleanURLHandler(root http.Dir, fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path

		if strings.HasSuffix(p, ".html") {
			clean := strings.TrimSuffix(p, ".html")
			if clean == "/index" {
				clean = "/"
			}
			if r.URL.RawQuery != "" {
				clean += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, clean, http.StatusMovedPermanently)
			return
		}

		if candidate := htmlCandidate(root, p); candidate != "" {
			// Serve the matching page directly rather than redirecting -
			// a visible redirect on every single navigation would be
			// both slower and would leak the .html extension right back
			// into the address bar, defeating the point of a clean URL.
			r2 := *r
			r2.URL.Path = candidate
			fileServer.ServeHTTP(w, &r2)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}

// htmlCandidate returns the "<p>.html" path to serve for an
// extension-less request, or "" if that doesn't apply - either because
// p already names something with its own extension (a css/js/svg/png
// asset, or an explicit .html request, already handled above), or
// because no matching .html file actually exists on disk (a genuine
// 404, or a directory request, which the fileServer's own index.html
// handling covers instead).
func htmlCandidate(root http.Dir, p string) string {
	if p == "" || strings.HasSuffix(p, "/") {
		return ""
	}
	if strings.Contains(path.Base(p), ".") {
		return ""
	}

	candidate := p + ".html"
	f, err := root.Open(candidate)
	if err != nil {
		return ""
	}
	f.Close()
	return candidate
}
