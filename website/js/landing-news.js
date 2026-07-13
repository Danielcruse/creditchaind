// landing-news.js powers the small rectangular corner at the bottom-
// right of the public landing page - polls the public (no wallet
// needed) /governance/status/recent endpoint and pops up whichever
// status is newest, cycling through unseen ones one at a time rather
// than dumping the whole feed. Purely a marketing/awareness surface -
// no wallet or signing involved, since this page has neither.

const LANDING_NEWS_POLL_MS = 15000;
const LANDING_NEWS_DISPLAY_MS = 9000;

let landingNewsSeenIDs = new Set();
let landingNewsQueue = [];
let landingNewsShowing = false;

function ensureLandingNewsBox() {
    let box = document.getElementById("landingNewsBox");
    if (box) return box;

    box = document.createElement("div");
    box.id = "landingNewsBox";
    box.style.cssText =
        "position:fixed;bottom:18px;right:18px;max-width:320px;z-index:9998;" +
        "background:#0b1026;border:1px solid rgba(255,255,255,0.15);border-radius:12px;" +
        "padding:14px 16px;box-shadow:0 6px 24px rgba(0,0,0,0.4);color:#fff;" +
        "font-family:Arial,sans-serif;display:none;";
    box.innerHTML = `
        <div style="display:flex;justify-content:space-between;align-items:flex-start;gap:8px;">
            <div style="font-size:11px;text-transform:uppercase;letter-spacing:0.5px;opacity:0.6;">Community Update</div>
            <span id="landingNewsClose" style="cursor:pointer;opacity:0.6;">&times;</span>
        </div>
        <p id="landingNewsText" style="margin:6px 0 4px;font-size:13px;"></p>
        <p id="landingNewsMeta" style="margin:0;font-size:11px;opacity:0.55;"></p>
    `;
    document.body.appendChild(box);
    document.getElementById("landingNewsClose").addEventListener("click", () => {
        box.style.display = "none";
        landingNewsShowing = false;
    });
    return box;
}

function showNextLandingNews() {
    if (landingNewsShowing || landingNewsQueue.length === 0) return;

    const status = landingNewsQueue.shift();
    const box = ensureLandingNewsBox();
    const shortAddr = status.Address ? `cc${status.Address.slice(0, 6)}...${status.Address.slice(-4)}` : "";
    const time = status.Timestamp ? new Date(status.Timestamp * 1000).toLocaleTimeString() : "";

    document.getElementById("landingNewsText").innerText = status.Text;
    document.getElementById("landingNewsMeta").innerText = `${shortAddr} · ${time}`;
    box.style.display = "block";
    landingNewsShowing = true;

    setTimeout(() => {
        box.style.display = "none";
        landingNewsShowing = false;
        showNextLandingNews();
    }, LANDING_NEWS_DISPLAY_MS);
}

async function pollLandingNews() {
    try {
        const res = await fetch(`${API_BASE}/governance/status/recent`);
        if (!res.ok) return;
        const statuses = await res.json();

        (statuses || [])
            .slice()
            .reverse()
            .forEach((s) => {
                if (!landingNewsSeenIDs.has(s.ID)) {
                    landingNewsSeenIDs.add(s.ID);
                    landingNewsQueue.push(s);
                }
            });

        showNextLandingNews();
    } catch (e) {
        console.error(e);
    }
}

// First poll marks everything currently live as "already seen" -
// without this, a visitor loading the page for the first time would
// get hit with every historical status queued up at once.
(async () => {
    try {
        const res = await fetch(`${API_BASE}/governance/status/recent`);
        if (res.ok) {
            const statuses = await res.json();
            (statuses || []).forEach((s) => landingNewsSeenIDs.add(s.ID));
        }
    } catch (e) {
        console.error(e);
    }
    setInterval(pollLandingNews, LANDING_NEWS_POLL_MS);
})();
