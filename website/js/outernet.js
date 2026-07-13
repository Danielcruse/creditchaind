const wallet = requireWallet();

if (wallet) {
    document.getElementById("wallet").innerText = formatAddress(wallet.address);

    const username = localStorage.getItem("username");
    if (username) {
        document.getElementById("username").innerText = username;
    }
}

// The full block-height/hash/peer-count/validator/TPS/supply detail
// used to be duplicated here - it's now only on network.html, which
// nothing on this dashboard repeats. The wallet-hero's "Network Live"
// status line is the one piece still shown here, since it's a glance-
// able health indicator, not a duplicate detail view.
async function loadNetworkStatus() {
    try {
        const data = await apiGet("/network");
        document.querySelector(".network-live").innerText = `● ${data.status}`;
    } catch (e) {
        console.error(e);
    }
}

// loadWallet updates each field independently (not one big all-or-
// nothing try/catch) - a single failed request (a slow/dropped
// derived-address computation, one endpoint timing out) used to leave
// every field, including ones that succeeded, stuck at the "TODO"
// placeholder forever. Each piece now fails on its own without
// blocking the others.
// Node ID/voting power/reputation/credit tier/validator status used to
// be duplicated here too - that detail now lives only on profile.html.
// This dashboard keeps just the total balance (wallet-hero, an
// actionable figure) and a one-line holdings summary (Assets card,
// which links out to assets.html for the real per-token breakdown).
async function loadWallet() {
    if (!wallet) return;

    let cctBalance = 0;
    let ethBalance = 0;
    let usdcBalance = 0;

    try {
        const tokenBalance = await apiGet(`/token/balance?address=${wallet.address}`);
        document.getElementById("balance").innerText = `${tokenBalance.balance} CCT`;
        cctBalance = tokenBalance.balance;
    } catch (e) {
        console.error(e);
    }

    try {
        const ethTestAddress = await getEthTestAddress(wallet);
        const ethTestBalance = await apiGet(`/ethtest/balance?address=${ethTestAddress}`);
        ethBalance = ethTestBalance.balance;
    } catch (e) {
        console.error(e);
    }

    try {
        const usdcTestAddress = await getUsdcTestAddress(wallet);
        const usdcTestBalance = await apiGet(`/usdctest/balance?address=${usdcTestAddress}`);
        usdcBalance = usdcTestBalance.balance;
    } catch (e) {
        console.error(e);
    }

    const summaryEl = document.getElementById("assetsSummaryLine");
    if (summaryEl) {
        const held = [cctBalance > 0 ? "CCT" : null, ethBalance > 0 ? "ETH" : null, usdcBalance > 0 ? "USDC" : null].filter(
            Boolean
        );
        summaryEl.innerText =
            held.length > 0
                ? `You hold ${held.length} asset${held.length === 1 ? "" : "s"}: ${held.join(", ")}.`
                : "No assets held yet - claim some from the faucet to get started.";
    }
}

// Dashboard preview only - last 5 blocks/transactions, never the
// whole chain. Anything beyond that belongs on the real explorer
// page (explorer.html), which has proper pagination and search.
async function loadExplorer() {
    try {
        const data = await apiGet("/explorer?blocksLimit=5&txLimit=5");

        const blocks = data.blocks || [];
        document.getElementById("blocks").innerText =
            blocks.length === 0
                ? "No blocks yet."
                : blocks
                      .map((b) => `#${b.Index}  ${b.Hash}  (${(b.Transactions || []).length} tx)`)
                      .join("\n");
    } catch (e) {
        console.error(e);
    }
}

// txDetailCache holds the last-rendered "my transactions" list keyed
// by ID, so clicking a row to open the detail modal doesn't need a
// second round trip - loadMyTransactions already fetched everything
// the modal needs.
const txDetailCache = new Map();

function shortTxId(id) {
    if (!id) return "-";
    return id.length > 16 ? `${id.slice(0, 8)}...${id.slice(-6)}` : id;
}

// txSymbol pulls the token/network a transaction actually moved out
// of its Data payload - mirrors explorer.js's txTypeLabel, kept as a
// separate smaller copy here since the dashboard preview only needs
// the symbol, not the full label.
function txSymbol(tx) {
    let data = {};
    if (tx.Data) {
        try {
            data = JSON.parse(tx.Data);
        } catch (e) {
            data = {};
        }
    }
    if (tx.Type === "STAKE_ETH" || tx.Type === "UNSTAKE_ETH") return data.symbol || "ETHTEST";
    if (tx.Type === "EXCHANGE") return data.symbol || "CCT";
    return data.symbol || "CCT";
}

// loadMyTransactions shows only transactions where the logged-in
// wallet is sender or recipient - merging still-pending mempool
// entries (status "pending") with confirmed on-chain history (status
// "confirmed") from the same address-profile lookup the Explorer's
// search box already uses, so this doesn't need its own backend
// endpoint.
async function loadMyTransactions() {
    if (!wallet) return;

    const div = document.getElementById("transactions");

    try {
        const [mempool, searchResult] = await Promise.all([
            apiGet("/mempool"),
            apiGet(`/explorer/search?q=${wallet.address}`),
        ]);

        const mine = (address) => address === wallet.address;

        const pending = (mempool || [])
            .filter((tx) => mine(tx.From) || mine(tx.To))
            .map((tx) => ({ ...tx, _status: "pending" }));

        const confirmed =
            searchResult && searchResult.type === "address"
                ? (searchResult.address.transactions || []).map((tx) => ({ ...tx, _status: "confirmed" }))
                : [];

        const combined = [...pending, ...confirmed].slice(0, 5);

        txDetailCache.clear();
        combined.forEach((tx) => txDetailCache.set(tx.ID, tx));

        if (combined.length === 0) {
            div.innerHTML = "No transactions yet.";
            return;
        }

        div.innerHTML = combined
            .map((tx) => {
                const other = mine(tx.From) ? tx.To : tx.From;
                const statusColor = tx._status === "confirmed" ? "#00ff95" : "#ffb020";
                // tx.summary (only present on confirmed TransactionViews,
                // see explorer_summary.go) already describes system-only
                // transactions (faucet claims, block rewards) correctly -
                // fall back to a generic Sent/Received line only when it's
                // missing (pending mempool entries, which are raw
                // core.Transaction with no summary yet).
                const line = tx.summary
                    ? tx.summary
                    : `${mine(tx.From) ? "Sent" : "Received"} ${tx.Amount} ${txSymbol(tx)}${other ? ` ${mine(tx.From) ? "to" : "from"} ${formatAddress(other)}` : ""}`;
                return `
                    <p style="cursor:pointer;margin:4px 0;" onclick="openTxDetail('${tx.ID}')">
                        <span style="color:${statusColor};">&#9679;</span>
                        ${line}
                        <br><span style="opacity:0.55;font-size:11px;">${shortTxId(tx.ID)}</span>
                    </p>
                `;
            })
            .join("");
    } catch (e) {
        console.error(e);
    }
}

function openTxDetail(id) {
    const tx = txDetailCache.get(id);
    if (!tx) return;

    const mine = (address) => wallet && address === wallet.address;
    const direction = mine(tx.From) ? "Sent" : "Received";
    const other = mine(tx.From) ? tx.To : tx.From;
    const statusLabel = tx._status === "confirmed" ? "Delivered" : "Pending";
    const statusColor = tx._status === "confirmed" ? "#00ff95" : "#ffb020";

    document.getElementById("txDetailBody").innerHTML = `
        <p><strong>${tx.summary || `${direction} ${tx.Amount} ${txSymbol(tx)}`}</strong></p>
        <p>To/From: <span style="word-break:break-all;">${other ? formatAddress(other) : "(system - no counterparty address)"}</span></p>
        <p>Network: CreditChain (${txSymbol(tx)})</p>
        <p>Status: <span style="color:${statusColor};">${statusLabel}</span></p>
        <p>
            Transaction ID:<br>
            <span id="txDetailId" style="word-break:break-all;font-family:monospace;font-size:12px;">${tx.ID}</span>
            <button onclick="copyTxId()" title="Copy transaction ID" style="margin-left:6px;">&#128203;</button>
        </p>
        <p id="txDetailCopyStatus" style="font-size:12px;opacity:0.7;"></p>
    `;
    document.getElementById("txDetailOverlay").style.display = "flex";
}

function closeTxDetail() {
    document.getElementById("txDetailOverlay").style.display = "none";
}

function copyTxId() {
    const id = document.getElementById("txDetailId").innerText;
    if (navigator.clipboard) {
        navigator.clipboard
            .writeText(id)
            .then(() => {
                document.getElementById("txDetailCopyStatus").innerText = "Copied!";
            })
            .catch(() => {});
    }
}

async function loadMempool() {
    try {
        const data = await apiGet("/mempool");

        document.getElementById("activity").innerText = `${(data || []).length} pending transactions`;
    } catch (e) {
        console.error(e);
    }
}

const NOTIFICATION_ICONS = {
    TRADE_STARTED: "\u{1F91D}",
    NEW_MESSAGE: "\u{1F4AC}",
    LOGIN: "\u{1F510}",
    REPT_MINTED: "\u{1F3C6}",
};

async function loadNotifications() {
    if (!wallet) return;

    try {
        const result = await apiGet(`/notifications?address=${wallet.address}`);
        const div = document.getElementById("notifications");
        const list = (result.notifications || []).slice(0, 5);

        if (list.length === 0) {
            div.innerHTML = "No notifications.";
            return;
        }

        div.innerHTML = list
            .map((n) => {
                const icon = NOTIFICATION_ICONS[n.Type] || "\u{1F514}";
                const time = n.CreatedAt ? new Date(n.CreatedAt * 1000).toLocaleTimeString() : "";
                return `<p>${icon} ${n.Message} <span style="opacity:0.6;font-size:11px;">${time}</span></p>`;
            })
            .join("");
    } catch (e) {
        console.error(e);
    }
}

// pingLogin records one "new login activity" notification per browser
// session (sessionStorage guard) - fires once right after the wallet
// loads, not on every 5s refresh tick.
async function pingLogin() {
    if (!wallet) return;
    if (sessionStorage.getItem("cc_login_pinged")) return;

    try {
        await apiPostSigned("/session/login-ping", {});
        sessionStorage.setItem("cc_login_pinged", "1");
    } catch (e) {
        console.error(e);
    }
}

function showReceiveAddress() {
    if (!wallet) return;

    if (navigator.clipboard) {
        navigator.clipboard.writeText(formatAddress(wallet.address)).catch(() => {});
    }

    document.getElementById("qrAddressText").innerText = formatAddress(wallet.address);
    QR.renderToCanvas(document.getElementById("qrCanvas"), formatAddress(wallet.address), 6);
    document.getElementById("qrModalOverlay").style.display = "flex";
}

function closeQrModal() {
    document.getElementById("qrModalOverlay").style.display = "none";
}

async function claimFaucet() {
    if (!wallet) return;

    try {
        const result = await apiPostSigned("/faucet", {});
        document.getElementById("faucetStatus").innerText =
            `Faucet claim pending confirmation (tx ${result.txId}) - ${result.amount} CCT, ${result.ethTestAmount} ETHTEST, and ${result.usdcTestAmount} USDCTEST will land shortly (ETHTEST/USDCTEST go to your imported-token address).`;
        setTimeout(loadWallet, 9000);
    } catch (e) {
        document.getElementById("faucetStatus").innerText = e.message;
    }
}

async function refresh() {
    await loadNetworkStatus();
    await loadWallet();
    await loadExplorer();
    await loadMyTransactions();
    await loadMempool();
    await loadNotifications();
}

refresh();
pingLogin();

setInterval(refresh, 5000);
