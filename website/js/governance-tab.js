// governance-tab.js - the landing page's interactive Governance card,
// and (via idle-feed.js on logged-in pages) the same floating-panel
// burst opened automatically after a period of user inactivity. First
// tap: fetches recent community status posts (GET
// /governance/status/recent, same public/no-wallet endpoint
// landing-news.js already polls) and reveals them inline in the card
// with a "light coming into existence" glow. Second tap (or an idle
// timeout, see openIdleGovBurst): the card bursts into floating,
// semi-transparent panels drifting across the page - a bottom-left
// button links to the full governance.html page, a top-right exit icon
// closes the overlay back to the resting state.
//
// Sponsored Posts (core.SponsoredPost, api/sponsored_post.go) are woven
// into this SAME feed rather than a separate ad surface - each
// rendered panel is tagged Sponsored, always shows Reason (why this
// viewer is seeing it - "Shown to everyone" or "Shown because you
// delegate to <validator>" - never a silent placement), and carries a
// Mute button so a viewer can stop seeing a specific validator's
// sponsored content going forward.

const GOV_TAB_MAX_PANELS = 7;
// GOV_TAB_MAX_SPONSORED_PANELS caps how many of GOV_TAB_MAX_PANELS a
// single burst may spend on Sponsored Posts - always alongside, never
// instead of, real governance statuses, so an idle-triggered burst
// never reads as ad-heavy.
const GOV_TAB_MAX_SPONSORED_PANELS = 2;
const GOV_TAB_MUTED_VALIDATORS_KEY = "cc_muted_sponsor_validators";

let govTabState = "idle"; // "idle" | "lit" | "burst"
let govTabStatuses = [];
let govTabSponsoredEntries = [];

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

// mutedSponsorValidators/isValidatorMuted/muteValidator - a viewer's own
// per-browser mute list (localStorage, same "per-viewer convenience,
// never authoritative" status as getDisplayCurrency in currency.js).
// Muting is by VALIDATOR (the "community" a Sponsored Post is scoped
// to), not by individual post, so muting one unwanted post's validator
// also silences any other post that validator later sponsors.
function mutedSponsorValidators() {
    try {
        return JSON.parse(localStorage.getItem(GOV_TAB_MUTED_VALIDATORS_KEY) || "[]");
    } catch (e) {
        return [];
    }
}

function isValidatorMuted(validatorId) {
    return mutedSponsorValidators().includes(validatorId);
}

function muteValidator(validatorId) {
    const muted = mutedSponsorValidators();
    if (!muted.includes(validatorId)) {
        muted.push(validatorId);
        localStorage.setItem(GOV_TAB_MUTED_VALIDATORS_KEY, JSON.stringify(muted));
    }
    govTabSponsoredEntries = govTabSponsoredEntries.filter((e) => e.ValidatorID !== validatorId);
    // Re-render immediately if the burst is already open, so muting
    // takes visible effect without needing to close/reopen.
    if (govTabState === "burst") {
        closeGovBurst();
        openGovBurst();
        govTabState = "burst";
    }
}

// fetchSponsoredFeed fetches every Sponsored Post the given wallet
// address (or "" for an anonymous/logged-out visitor, who only sees
// global-reach posts) is eligible to see right now, filtered to
// already-muted validators client-side.
async function fetchSponsoredFeed(viewerAddress) {
    try {
        const res = await fetch(`${API_BASE}/sponsored-posts/feed?viewer=${encodeURIComponent(viewerAddress || "")}`);
        if (!res.ok) return [];
        const data = await res.json();
        return (data || []).filter((e) => !e.ValidatorID || !isValidatorMuted(e.ValidatorID));
    } catch (e) {
        console.error(e);
        return [];
    }
}

// openIdleGovBurst is idle-feed.js's entry point (see that file) - the
// idle-triggered counterpart to handleGovTabClick's manual two-tap
// path. Fetches both the ordinary governance feed and the Sponsored
// Post feed for whichever wallet (if any) is present in this browser,
// then opens the exact same burst overlay handleGovTabClick's second
// tap does - one feed, two trigger paths.
async function openIdleGovBurst() {
    if (govTabState === "burst") return;

    const viewerAddress = typeof getWalletAddress === "function" ? getWalletAddress() : null;
    const [statuses, sponsored] = await Promise.all([
        fetchGovTabStatuses(),
        fetchSponsoredFeed(viewerAddress),
    ]);
    govTabStatuses = statuses;
    govTabSponsoredEntries = sponsored;

    openGovBurst();
    govTabState = "burst";
}

async function handleGovTabClick(e) {
    if (e) e.stopPropagation();

    const card = document.getElementById("govTabCard");
    if (!card) return;

    if (govTabState === "idle") {
        const viewerAddress = typeof getWalletAddress === "function" ? getWalletAddress() : null;
        const [statuses, sponsored] = await Promise.all([
            fetchGovTabStatuses(),
            fetchSponsoredFeed(viewerAddress),
        ]);
        govTabStatuses = statuses;
        govTabSponsoredEntries = sponsored;
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

    // Interleave Sponsored Posts into the same feed rather than a
    // separate ad surface - capped at GOV_TAB_MAX_SPONSORED_PANELS so
    // an idle-triggered burst never reads as ad-heavy, and always
    // placed alongside (never replacing) real governance statuses.
    const sponsoredSlice = govTabSponsoredEntries.slice(0, GOV_TAB_MAX_SPONSORED_PANELS)
        .map((entry) => ({ ...entry, sponsored: true }));
    const remainingSlots = Math.max(0, GOV_TAB_MAX_PANELS - sponsoredSlice.length);
    const statusSlice = govTabStatuses.slice(0, remainingSlots).map((s) => ({ ...s, sponsored: false }));

    const statuses = statusSlice.length + sponsoredSlice.length > 0
        ? [...statusSlice, ...sponsoredSlice]
        : [{ ID: "placeholder", Address: "", Text: "Be the first to post a community update once governance is live.", Timestamp: 0, sponsored: false }];

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

        const time = status.CreatedAt || status.Timestamp
            ? new Date((status.CreatedAt || status.Timestamp) * 1000).toLocaleString()
            : "";

        if (status.sponsored) {
            // Sponsored panels are visually distinct (badge) and always
            // show WHY this viewer is seeing it (Reason - never a
            // silent placement) plus a Mute control for that specific
            // validator's future sponsored content.
            panel.classList.add("gov-float-panel-sponsored");
            const muteBtn = status.ValidatorID
                ? `<button type="button" class="gov-float-panel-mute" onclick="event.stopPropagation();muteValidator('${status.ValidatorID}')">Mute</button>`
                : "";
            panel.innerHTML = `
                <div class="gov-float-panel-meta">
                    <span class="gov-float-panel-sponsored-badge">Sponsored</span>
                    ${time ? "&middot; " + time : ""}
                </div>
                <div class="gov-float-panel-text">${escapeHtml(status.Text)}</div>
                <div class="gov-float-panel-reason">${escapeHtml(status.Reason || "")}</div>
                ${muteBtn}
            `;
        } else {
            panel.innerHTML = `
                <div class="gov-float-panel-meta">${status.Address ? shortGovAddr(status.Address) : "Community"}${time ? " &middot; " + time : ""}</div>
                <div class="gov-float-panel-text">${escapeHtml(status.Text)}</div>
            `;
        }
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
    fullBtn.href = "governance";
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
