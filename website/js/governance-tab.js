// governance-tab.js - the landing page's interactive Governance card.
// First tap: fetches recent community status posts (GET
// /governance/status/recent, same public/no-wallet endpoint
// landing-news.js already polls) and reveals them inline in the card
// with a "light coming into existence" glow. Second tap: the card
// bursts into floating, semi-transparent panels drifting across the
// page - a bottom-left button links to the full governance.html page,
// a top-right exit icon closes the overlay back to the resting state.

const GOV_TAB_MAX_PANELS = 7;

let govTabState = "idle"; // "idle" | "lit" | "burst"
let govTabStatuses = [];

function shortGovAddr(addr) {
    return addr ? `cc${addr.slice(0, 6)}...${addr.slice(-4)}` : "Anonymous";
}

async function fetchGovTabStatuses() {
    try {
        const res = await fetch(`${API_BASE}/governance/status/recent`);
        if (!res.ok) return [];
        const data = await res.json();
        return data || [];
    } catch (e) {
        console.error(e);
        return [];
    }
}

async function handleGovTabClick(e) {
    if (e) e.stopPropagation();

    const card = document.getElementById("govTabCard");
    if (!card) return;

    if (govTabState === "idle") {
        govTabStatuses = await fetchGovTabStatuses();
        renderGovTabInline();
        card.classList.add("lit");
        govTabState = "lit";
        return;
    }

    if (govTabState === "lit") {
        openGovBurst();
        govTabState = "burst";
        return;
    }

    // burst: a stray click on the (now-covered) card just closes it,
    // same as the exit icon - a safety toggle, not the primary path.
    closeGovBurst();
}

function renderGovTabInline() {
    const statusEl = document.getElementById("govTabStatus");
    if (!statusEl) return;

    if (govTabStatuses.length === 0) {
        statusEl.innerText = "No community updates yet - be the first to post once governance is live.";
        return;
    }

    const latest = govTabStatuses[0];
    statusEl.innerText = `"${latest.Text}" - ${shortGovAddr(latest.Address)}`;
}

function govBurstOrigin() {
    const card = document.getElementById("govTabCard");
    if (!card) return { x: window.innerWidth / 2, y: window.innerHeight / 2 };
    const rect = card.getBoundingClientRect();
    return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
}

function openGovBurst() {
    let overlay = document.getElementById("govBurstOverlay");
    if (overlay) return;

    overlay = document.createElement("div");
    overlay.id = "govBurstOverlay";
    overlay.className = "gov-burst-overlay";

    const origin = govBurstOrigin();
    const statuses = govTabStatuses.length > 0
        ? govTabStatuses.slice(0, GOV_TAB_MAX_PANELS)
        : [{ ID: "placeholder", Address: "", Text: "Be the first to post a community update once governance is live.", Timestamp: 0 }];

    const panelCount = statuses.length;
    statuses.forEach((status, i) => {
        const panel = document.createElement("div");
        panel.className = "gov-float-panel";

        // Spread panels across a loose grid of the viewport, then
        // jitter within each cell so they don't line up too neatly -
        // still reads as "floating across the page," not a grid.
        const cols = Math.max(2, Math.ceil(Math.sqrt(panelCount)));
        const row = Math.floor(i / cols);
        const col = i % cols;
        const cellW = 100 / cols;
        const cellH = 70 / Math.ceil(panelCount / cols);
        const jitterX = (Math.random() - 0.5) * cellW * 0.5;
        const jitterY = (Math.random() - 0.5) * cellH * 0.5;
        const leftPct = Math.min(82, Math.max(4, col * cellW + cellW / 2 + jitterX));
        const topPct = Math.min(72, Math.max(12, row * cellH + cellH / 2 + jitterY + 14));

        panel.style.left = `${leftPct}%`;
        panel.style.top = `${topPct}%`;

        // --ox/--oy: where this panel visually "bursts in from" -
        // the governance card's own position, so every panel appears
        // to fly outward from the tab that was tapped.
        const panelX = (leftPct / 100) * window.innerWidth;
        const panelY = (topPct / 100) * window.innerHeight;
        panel.style.setProperty("--ox", `${origin.x - panelX}px`);
        panel.style.setProperty("--oy", `${origin.y - panelY}px`);
        panel.style.setProperty("--fx1", `${(Math.random() - 0.5) * 40}px`);
        panel.style.setProperty("--fy1", `${(Math.random() - 0.5) * 30}px`);
        panel.style.setProperty("--fx2", `${(Math.random() - 0.5) * 40}px`);
        panel.style.setProperty("--fy2", `${(Math.random() - 0.5) * 30}px`);
        panel.style.setProperty("--delay", `${i * 0.08}s`);
        panel.style.setProperty("--dur", `${8 + Math.random() * 4}s`);

        const time = status.Timestamp ? new Date(status.Timestamp * 1000).toLocaleString() : "";
        panel.innerHTML = `
            <div class="gov-float-panel-meta">${status.Address ? shortGovAddr(status.Address) : "Community"}${time ? " &middot; " + time : ""}</div>
            <div class="gov-float-panel-text">${status.Text}</div>
        `;
        overlay.appendChild(panel);
    });

    const closeBtn = document.createElement("button");
    closeBtn.id = "govBurstCloseBtn";
    closeBtn.className = "gov-burst-close";
    closeBtn.setAttribute("aria-label", "Close governance preview");
    closeBtn.innerHTML = "&times;";
    closeBtn.addEventListener("click", (e) => {
        e.stopPropagation();
        closeGovBurst();
    });
    overlay.appendChild(closeBtn);

    const fullBtn = document.createElement("a");
    fullBtn.id = "govBurstFullBtn";
    fullBtn.className = "gov-burst-full";
    fullBtn.href = "governance.html";
    fullBtn.setAttribute("aria-label", "Open the full Governance page");
    fullBtn.innerText = "View full Governance →";
    overlay.appendChild(fullBtn);

    overlay.addEventListener("click", (e) => {
        if (e.target === overlay) closeGovBurst();
    });

    document.body.appendChild(overlay);
}

function closeGovBurst() {
    const overlay = document.getElementById("govBurstOverlay");
    if (overlay) overlay.remove();

    const card = document.getElementById("govTabCard");
    if (card) card.classList.remove("lit");

    govTabState = "idle";
}

document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && govTabState === "burst") closeGovBurst();
});
