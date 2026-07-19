// adminToken lives in a page-scoped variable only - never
// localStorage/sessionStorage/cookies, same spirit as how this app
// already handles wallet private keys client-side. Lost on reload by
// design.
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
        const result = await apiAdminPost("/admin/treasury/address", { foundationAddress });
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
        const result = await apiAdminPost("/admin/treasury/sale-price", { salePriceCentsPerCCT });
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
        const result = await apiAdminPost("/admin/treasury/paused", { paused: !info.paused });
        statusEl.innerText = `Saved (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(loadTreasuryInfo, 9000);
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

async function loadPurchases() {
    const div = document.getElementById("purchaseList");
    if (!adminToken) {
        div.innerHTML = `<div class="explorer-empty">Unlock with the admin token to load.</div>`;
        return;
    }
    try {
        const purchases = await apiAdminGet("/admin/treasury/purchases");
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
        await apiAdminPost("/admin/treasury/purchase/confirm", { purchaseId });
        setTimeout(() => {
            loadPurchases();
            loadTreasuryInfo();
        }, 9000);
    } catch (e) {
        alert("Could not confirm: " + e.message);
    }
}

loadTreasuryInfo();

setInterval(() => {
    loadTreasuryInfo();
    loadPurchases();
}, 10000);
