// idle-feed.js - opens the governance floating-panel burst
// (governance-tab.js's openIdleGovBurst) automatically after a period
// of user inactivity on a logged-in page, so a wallet that's been
// sitting on a page without any action sees governance updates and
// Sponsored Posts rather than a stale, static screen. This file must
// load AFTER governance-tab.js (openIdleGovBurst/closeGovBurst/
// govTabState must already exist).
//
// Deliberately reuses the SAME burst overlay the landing page's manual
// two-tap Governance card already opens - one feed, two trigger paths
// (click, or now, idle) - rather than building a second overlay.
// govBurstOrigin() already falls back to viewport-center when no
// govTabCard element exists on the page (see that function), so this
// works on a page like outernet.html that has no Governance card UI at
// all.

const IDLE_FEED_THRESHOLD_MS = 90 * 1000; // 90s, a disclosed judgment call, not a locked spec value

let idleFeedTimer = null;

function resetIdleFeedTimer() {
    if (idleFeedTimer) clearTimeout(idleFeedTimer);
    idleFeedTimer = setTimeout(() => {
        if (typeof openIdleGovBurst === "function") {
            openIdleGovBurst();
        }
    }, IDLE_FEED_THRESHOLD_MS);
}

// A genuine interaction always resets the idle clock. It deliberately
// does NOT close an already-open burst on its own - the burst's own
// close controls (Escape, the X button, or a click on its background)
// already handle that; treating every mousemove as "close it" would
// also close it the instant someone reaches for the Mute/Report button
// inside a sponsored panel.
["mousemove", "keydown", "touchstart", "click", "scroll"].forEach((evt) => {
    document.addEventListener(evt, resetIdleFeedTimer, { passive: true });
});

resetIdleFeedTimer();
