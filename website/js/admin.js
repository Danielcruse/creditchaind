const wallet = requireWallet();

// adminToken lives in a page-scoped variable only - never
// localStorage/sessionStorage/cookies, same spirit as how this app
// already handles wallet private keys client-side. Lost on reload by
// design. Only used as a fallback on networks where no admin wallet
// (ADMIN_WALLET_ADDRESS) has been configured yet - see loadAdminWalletStatus.
let adminToken = null;

async function apiAdminGet(path) {
    const res = await fetch(API_BASE + path, {
        headers: { "X-Admin-Token": adminToken || "" },
    });
    if (!res.ok) {
        throw new Error((await res.text()) || `GET ${path} failed (${res.status})`);
    }
    return res.json();
}

async function apiAdminPost(path, body) {
    const res = await fetch(API_BASE + path, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
            "X-Admin-Token": adminToken || "",
        },
        body: JSON.stringify(body || {}),
    });
    if (!res.ok) {
        throw new Error((await res.text()) || `POST ${path} failed (${res.status})`);
    }
    return res.json();
}

function setAdminToken() {
    const value = document.getElementById("adminTokenInput").value.trim();
    if (!value) {
        document.getElementById("adminTokenStatus").innerText = "Enter a token first.";
        return;
    }
    adminToken = value;
    document.getElementById("adminTokenStatus").innerText = "Token set for this tab.";
    loadPurchases();
}

// loadAdminWalletStatus compares the wallet logged into this browser
// against the network's configured admin wallet (GET /admin/whoami is
// public - the address is inherently visible on-chain anyway once any
// admin transaction lands), so an operator gets a clear "you're logged
// in as the right/wrong wallet" signal before trying an action and
// hitting a confusing 403.
async function loadAdminWalletStatus() {
    document.getElementById("myWalletAddressDisplay").innerText = wallet ? wallet.address : "(not logged in)";
    try {
        const info = await apiGet("/admin/whoami");
        const configured = info.adminWalletAddress || "";
        document.getElementById("configuredAdminAddressDisplay").innerText = configured || "(not configured - legacy admin token mode)";

        const statusEl = document.getElementById("adminWalletMatchStatus");
        if (!configured) {
            statusEl.innerText = "No admin wallet configured on this network yet - use the Admin Token below instead.";
        } else if (wallet && wallet.address === configured) {
            statusEl.innerText = "✓ This wallet is the configured admin wallet - every action below will sign correctly.";
        } else {
            statusEl.innerText = "⚠ This wallet is NOT the configured admin wallet - actions below will be rejected. Log in as the admin wallet instead.";
        }
    } catch (e) {
        console.error(e);
    }
}

// loadTreasuryInfo is public - no admin token needed, matches
// HandleTreasuryInfo's own auth (none).
async function loadTreasuryInfo() {
    try {
        const info = await apiGet("/treasury/info");
        document.getElementById("treasuryAddressDisplay").innerText = info.foundationAddress || "(not set)";
        document.getElementById("salePriceDisplay").innerText = info.salePriceCentsPerCCT;
        document.getElementById("remainingAllocationDisplay").innerText = info.remainingAllocation;
        document.getElementById("pausedDisplay").innerText = info.paused ? "PAUSED" : "ACTIVE";
    } catch (e) {
        console.error(e);
    }
}

async function saveTreasuryAddress() {
    const foundationAddress = document.getElementById("treasuryAddressInput").value.trim();
    const statusEl = document.getElementById("treasuryAddressStatus");
    if (!foundationAddress) {
        statusEl.innerText = "Enter an address first.";
        return;
    }
    try {
        const result = await apiPostSigned("/admin/treasury/address", { foundationAddress });
        statusEl.innerText = `Saved (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(loadTreasuryInfo, 9000);
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

async function saveSalePrice() {
    const salePriceCentsPerCCT = parseInt(document.getElementById("salePriceInput").value, 10);
    const statusEl = document.getElementById("salePriceStatus");
    if (isNaN(salePriceCentsPerCCT) || salePriceCentsPerCCT < 0) {
        statusEl.innerText = "Enter a valid price.";
        return;
    }
    try {
        const result = await apiPostSigned("/admin/treasury/sale-price", { salePriceCentsPerCCT });
        statusEl.innerText = `Saved (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(loadTreasuryInfo, 9000);
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

async function togglePaused() {
    const statusEl = document.getElementById("pausedStatus");
    try {
        const info = await apiGet("/treasury/info");
        const result = await apiPostSigned("/admin/treasury/paused", { paused: !info.paused });
        statusEl.innerText = `Saved (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(loadTreasuryInfo, 9000);
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

async function loadPurchases() {
    const div = document.getElementById("purchaseList");
    try {
        const purchases = await apiPostSigned("/admin/treasury/purchases", {});
        const pending = (purchases || []).filter((p) => p.Status === "AWAITING_PAYMENT");

        if (pending.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No pending purchase requests.</div>`;
            return;
        }

        div.innerHTML = pending
            .map(
                (p) => `
            <div class="explorer-row">
                <span>${p.BuyerAddress.slice(0, 8)}...${p.BuyerAddress.slice(-6)}</span>
                <span>${p.CCTAmount} CCT</span>
                <span>${p.PriceAtRequest} cents/CCT</span>
                <span>requested ${new Date(p.CreatedAt * 1000).toLocaleString()}</span>
                <button onclick="confirmPurchase('${p.ID}')">Confirm</button>
            </div>
        `
            )
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<div class="explorer-empty">Could not load: ${e.message}</div>`;
    }
}

async function confirmPurchase(purchaseId) {
    if (!confirm("Confirm this purchase? This transfers CCT from the treasury to the buyer once the next block confirms it.")) {
        return;
    }
    try {
        await apiPostSigned("/admin/treasury/purchase/confirm", { purchaseId });
        setTimeout(() => {
            loadPurchases();
            loadTreasuryInfo();
        }, 9000);
    } catch (e) {
        alert("Could not confirm: " + e.message);
    }
}

// loadAnnouncements is public - no admin token needed, matches
// HandleListAnnouncements' own auth (none). Posting still requires
// admin auth (postAnnouncement below).
async function loadAnnouncements() {
    const div = document.getElementById("announcementList");
    try {
        const announcements = await apiGet("/announcements/recent");

        if (!announcements || announcements.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No announcements posted yet.</div>`;
            return;
        }

        div.innerHTML = announcements
            .map(
                (a) => `
            <div class="explorer-row" style="display:block;">
                <strong>${a.Title}</strong>
                <p style="margin:4px 0;">${a.Text}</p>
                <span style="opacity:0.6;">${new Date(a.CreatedAt * 1000).toLocaleString()}</span>
            </div>
        `
            )
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<div class="explorer-empty">Could not load: ${e.message}</div>`;
    }
}

async function postAnnouncement() {
    const title = document.getElementById("announcementTitleInput").value.trim();
    const text = document.getElementById("announcementTextInput").value.trim();
    const statusEl = document.getElementById("announcementPostStatus");

    if (!title || !text) {
        statusEl.innerText = "Enter both a title and text.";
        return;
    }

    try {
        const result = await apiPostSigned("/announcements/create", { title, text });
        statusEl.innerText = `Posted (tx ${result.txId}) - takes effect once the next block confirms it.`;
        document.getElementById("announcementTitleInput").value = "";
        document.getElementById("announcementTextInput").value = "";
        setTimeout(loadAnnouncements, 9000);
    } catch (e) {
        statusEl.innerText = "Could not post: " + e.message;
    }
}

async function withdrawGenesisPool() {
    const pool = document.getElementById("genesisPoolSelect").value;
    const to = document.getElementById("genesisPoolToInput").value.trim();
    const amount = parseInt(document.getElementById("genesisPoolAmountInput").value, 10);
    const statusEl = document.getElementById("genesisPoolWithdrawStatus");

    if (!to) {
        statusEl.innerText = "Enter a destination address.";
        return;
    }
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    if (!confirm(`Withdraw ${amount} CCT from ${pool} to ${to}? Takes effect once the next block confirms it.`)) {
        return;
    }

    try {
        const result = await apiPostSigned("/admin/genesis-pools/withdraw", { pool, to, amount });
        statusEl.innerText = `Submitted (tx ${result.txId}) - takes effect once the next block confirms it.`;
        document.getElementById("genesisPoolToInput").value = "";
        document.getElementById("genesisPoolAmountInput").value = "";
    } catch (e) {
        statusEl.innerText = "Could not withdraw: " + e.message;
    }
}

loadAdminWalletStatus();
loadTreasuryInfo();
loadPurchases();
loadAnnouncements();

setInterval(() => {
    loadTreasuryInfo();
    loadPurchases();
    loadAnnouncements();
}, 10000);
