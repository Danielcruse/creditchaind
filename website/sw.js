// sw.js - service worker for the installable Outernet PWA.
//
// Deliberately network-first, not cache-first, for every request this
// worker touches - a financial app showing a stale balance because a
// cache-first strategy served yesterday's JS is a much worse failure
// mode than "no offline support yet." This mirrors a real bug fixed
// tonight (website/js/api.js and network-config.js kept serving old
// code after deploy until a hard cache-bust) - a cache-first service
// worker would have made that failure mode permanent instead of just
// slow to notice.
//
// Scope is deliberately narrow: only same-origin GET requests for
// static file types (html/js/css/json/png/etc, matched by extension
// below) are intercepted at all. Anything else - every API call, every
// POST, every cross-origin request - passes straight through
// untouched, network-only, no caching. Wallet balances, explorer data,
// and every signed transaction must always be genuinely live.
//
// CACHE_VERSION must be bumped whenever this file itself changes -
// activate() below deletes every cache not matching the current
// version, so a stale worker's cache never lingers past one page load
// after a new version deploys.
const CACHE_VERSION = "outernet-v1";

const STATIC_EXT_PATTERN = /\.(html|js|css|json|png|jpg|jpeg|svg|webp|woff2?|ico)$/i;

self.addEventListener("install", (event) => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const names = await caches.keys();
      await Promise.all(
        names.filter((n) => n !== CACHE_VERSION).map((n) => caches.delete(n))
      );
      await self.clients.claim();
    })()
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;

  // Only ever touch same-origin GETs for static asset types - every
  // other request (API calls, POSTs, cross-origin) is left completely
  // alone, network-only, exactly as if this worker didn't exist.
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (!STATIC_EXT_PATTERN.test(url.pathname)) return;

  event.respondWith(
    (async () => {
      try {
        const fresh = await fetch(req);
        if (fresh && fresh.ok) {
          const cache = await caches.open(CACHE_VERSION);
          cache.put(req, fresh.clone());
        }
        return fresh;
      } catch (err) {
        // Offline (or the network request otherwise failed) - fall
        // back to whatever was last successfully cached, if anything.
        const cached = await caches.match(req);
        if (cached) return cached;
        throw err;
      }
    })()
  );
});
