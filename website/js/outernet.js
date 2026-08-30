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

// checkBtcNetworkMismatchBanner surfaces the "your cached BTC address
// is for the other network" warning right on the dashboard, the first
// page a user sees after switching networks or logging in - rather
// than only discovering it after already navigating into
// asset.html/send-bridged-btc.html and finding BTC blocked there. Only
// legacy (pre-dual-network-fix) wallets - which cached a single BTC
// address for whichever network was active at derivation time - can
// ever be mismatched; see api.js's getHdAccounts doc comment. Mirrors
// asset.js's own btcAddressMatchesNetwork (kept as its own copy per
// the same page-independence convention dashSmallestToDecimal above
// follows).
function btcAddressMatchesNetwork(address) {
    if (!address) return true;
    const expectedPrefix = window.CC_NETWORK === "mainnet" ? "bc1" : "tb1";
    return address.startsWith(expectedPrefix);
}

function checkBtcNetworkMismatchBanner() {
    if (!wallet) return;
    const accounts = getHdAccounts();
    const btcAddress = accounts && accounts.btc ? accounts.btc.address : null;
    if (!btcAddress || btcAddressMatchesNetwork(btcAddress)) return;

    const networkLabel = window.CC_NETWORK === "mainnet" ? "Mainnet" : "Testnet";
    document.getElementById("btcNetworkMismatchBannerText").innerText =
        ` It was derived for the other network, so it won't work for Bitcoin on ${networkLabel}.`;
    document.getElementById("btcNetworkMismatchBanner").style.display = "block";
}

// loadWallet updates each field independently (not one big all-or-
// nothing try/catch) - a single failed request (a slow/dropped
// derived-address computation, one endpoint timing out) used to leave
// every field, including ones that succeeded, stuck at the "TODO"
// placeholder forever. Each piece now fails on its own without
// blocking the others.
// Node ID/voting power/reputation/credit tier/validator status used to
// be duplicated here too - that detail now lives only on profile.html.
// This dashboard keeps the total balance (wallet-hero, an actionable
// figure). The Assets card itself is populated by assets.js's own
// loadAssets() (same full per-token list as assets.html, not a
// summary) - see the #assetsList container and script tag.
// dashSmallestToDecimal is a local copy of vault.js's smallestToDecimal -
// each page keeps its own tiny copy rather than relying on load order
// (see api.js's getHdAccounts doc comment for the same convention
// applied to a different helper).
function dashSmallestToDecimal(smallestStr, decimals) {
    const n = BigInt(smallestStr || "0");
    const scale = 10n ** BigInt(decimals);
    const whole = n / scale;
    const frac = (n % scale).toString().padStart(decimals, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// loadWallet shows a real cross-asset total - CCT, YENE, and the three
// vault-backed CCC1 balances (ETH_CCC1/USDC_CCC1/BTC_CCC1), converted to
// the user's selected display currency via currency.js's fiatValue.
// Deliberately does NOT include the real self-custodial ETH/USDC/BTCB
// balances sitting at a wallet's receive-only address - those aren't
// spendable/sendable within CreditChain until vaulted (see asset.js's
// pending-vault banner), so counting them here would overstate what a
// user can actually do with their balance and double up against the
// on-page "received, not yet vaulted" figure asset.html already shows.
// Each leg runs independently (Promise.allSettled, not Promise.all) so
// one slow/failed leg (e.g. no live price data yet) can't blank out
// every other asset's contribution - a partial total is still an
// honest total, just of what could actually be priced right now.
async function loadWallet() {
    if (!wallet) return;

    try {
        const tokenBalance = await apiGet(`/token/balance?address=${wallet.address}`);
        const cctDisplay = cctFromSmallest(tokenBalance.balance);

        const legPromises = [
            fiatValue("CCT", cctDisplay),
            fetchTokenBalance(wallet, "YENE").then((b) => fiatValue("YENE", b.balance)),
            fetchCcc1Balance(wallet, "ETH").then((r) => fiatValue("ETH", dashSmallestToDecimal(r.balance, VAULT_CHAIN_INFO.ETH.decimals))),
            fetchCcc1Balance(wallet, "USDC").then((r) => fiatValue("USDC", dashSmallestToDecimal(r.balance, VAULT_CHAIN_INFO.USDC.decimals))),
            fetchCcc1Balance(wallet, "BTC").then((r) => fiatValue("BTC", dashSmallestToDecimal(r.balance, VAULT_CHAIN_INFO.BTC.decimals))),
        ];

        const legs = await Promise.allSettled(legPromises);

        let total = 0;
        let anyPriced = false;
        for (const leg of legs) {
            if (leg.status === "fulfilled" && leg.value !== null) {
                total += leg.value;
                anyPriced = true;
            }
        }

        if (anyPriced) {
            const currency = getDisplayCurrency();
            document.getElementById("balance").innerText =
                `${currencySymbolFor(currency)}${total.toLocaleString(undefined, { maximumFractionDigits: 2, minimumFractionDigits: 2 })}`;
        } else {
            // No live price data available for anything yet - fall back
            // to the raw CCT figure rather than showing a blank/zero
            // total, honest about what's actually known right now.
            document.getElementById("balance").innerText = `${cctDisplay} CCT`;
        }
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
                const statusColor = tx._status === "confirmed" ? "#16A34A" : "#ffb020";
                // tx.summary (only present on confirmed TransactionViews,
                // see explorer_summary.go) already describes system-only
                // transactions (faucet claims, block rewards) correctly -
                // fall back to a generic Sent/Received line only when it's
                // missing (pending mempool entries, which are raw
                // core.Transaction with no summary yet).
                const line = tx.summary
                    ? escapeHtml(tx.summary)
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
    const statusColor = tx._status === "confirmed" ? "#16A34A" : "#ffb020";

    document.getElementById("txDetailBody").innerHTML = `
        <p><strong>${tx.summary ? escapeHtml(tx.summary) : `${direction} ${tx.Amount} ${txSymbol(tx)}`}</strong></p>
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
                return `<p>${icon} ${escapeHtml(n.Message)} <span style="opacity:0.6;font-size:11px;">${time}</span></p>`;
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

// Receive is a two-step flow: pick which token you want to receive,
// then show ONLY that token's CreditChain-network address+QR - never
// a per-network chooser here (that lives on each token's own asset
// page, see asset.js's toggleReceivePanel), so this landing-page
// button can't be mistaken for "send me real BTC/ETH here".
const RECEIVE_TOKEN_LABELS = {
    CCT: "CCT",
    ETHB: "ETH (bridged)",
    BTCB: "BTC (bridged)",
    USDCTEST: "USDC (testnet)",
    YENE: "YEN-e",
};

function openReceivePicker() {
    if (!wallet) return;
    document.getElementById("receivePickerOverlay").style.display = "flex";
}

// On mainnet, "ETH"/"USDC" are the unified, self-custodial pages
// (asset.html?symbol=ETH/USDC) - free, real chain balance, key never
// leaves the browser. On testnet those symbols don't exist as unified
// rows (see assets.js's isMainnet branch), so this keeps routing to
// the ETHB/USDCTEST pages exactly as before - those also resolve to
// the wallet's own self-custodial address now (see api.js's
// getEthbAddress), not a CreditChain-held one.
function receiveEthPicked() {
    closeReceivePicker();
    location.href = window.CC_NETWORK === "mainnet" ? "asset?symbol=ETH" : "asset?symbol=ETHB";
}

function receiveUsdcPicked() {
    if (window.CC_NETWORK === "mainnet") {
        closeReceivePicker();
        location.href = "asset?symbol=USDC";
        return;
    }
    selectReceiveToken("USDCTEST");
}

function closeReceivePicker() {
    document.getElementById("receivePickerOverlay").style.display = "none";
}

function backToReceivePicker() {
    closeQrModal();
    openReceivePicker();
}

// ETHB's address is the wallet's own real, self-custodial Sepolia
// 0x-address (see api.js's getEthbAddress - CreditChain never holds
// this key) - a genuinely different format from every other symbol
// here, which resolve to a CreditChain-network sub-address
// ("cc"-prefixed, 40 hex). Formatting an 0x-address with a "cc" prefix
// would misrepresent it as a CreditChain address, so this only
// applies formatAddress's "cc" convention to the symbols it's
// actually meant for.
async function selectReceiveToken(symbol) {
    if (!wallet) return;
    closeReceivePicker();

    const rawAddress = await balanceAddressForSymbol(wallet, symbol);
    const displayAddress = symbol === "ETHB" ? rawAddress : formatAddress(rawAddress);

    if (navigator.clipboard) {
        navigator.clipboard.writeText(displayAddress).catch(() => {});
    }

    document.getElementById("qrModalTitle").innerText = `Your ${RECEIVE_TOKEN_LABELS[symbol] || symbol} Address`;
    document.getElementById("qrAddressText").innerText = displayAddress;
    QR.renderToCanvas(document.getElementById("qrCanvas"), displayAddress, 6);
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
    await loadMyTransactions();
    await loadMempool();
    await loadNotifications();
}

// loadTradingHubGate hides the real "Trading Hub" sidebar button
// behind an "Apply Here" link unless this wallet is already a
// VERIFIED Trading Hub - everyone else applies through companies.html
// first (see api/companies.go's HandleCompanyStatus). Checked once at
// load, not on every 5s refresh poll, since verification status
// doesn't change on its own.
async function loadTradingHubGate() {
    if (!wallet) return;
    const btn = document.getElementById("tradingHubNavBtn");
    if (!btn) return;
    try {
        const status = await apiGet(`/companies/status?address=${wallet.address}`);
        if (!status.verified) {
            btn.innerText = "Trading Hub - Apply Here";
            btn.title = "Apply to become a Verified Trading Hub";
        }
    } catch (e) {
        console.error(e);
    }
}

const txViewOnExplorerBtn = document.getElementById("txViewOnExplorerBtn");
if (txViewOnExplorerBtn) {
    txViewOnExplorerBtn.onclick = () =>
        location.href = "explorer?q=" + encodeURIComponent(wallet.address);
}

refresh();
loadTradingHubGate();
pingLogin();
checkBtcNetworkMismatchBanner();

setInterval(refresh, 5000);
