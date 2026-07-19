const wallet = requireWallet();

const symbol = new URLSearchParams(window.location.search).get("symbol") || "CCT";

const ASSET_DESCRIPTIONS = {
    CCT: "CreditChain's native network token - the universal hub every swap prices through.",
    REPT: "Reputation-progression token, minted as a one-time reward the first time your reputation reaches a new credit tier.",
    ETHTEST: "Imported test ETH token - lives at its own address derived from this wallet's key, separate from your CCT address. This is CreditChain's own internal representation, not real Ethereum.",
    USDCTEST: "Imported test USDC token - lives at its own address derived from this wallet's key, separate from your CCT address.",
    ETH: "Real Ethereum (testnet), self-custodial - derived from the same recovery phrase as your CreditChain account, but an entirely separate blockchain with its own address and key.",
    BTC: "Real Bitcoin (testnet), self-custodial - derived from the same recovery phrase as your CreditChain account, but an entirely separate blockchain with its own address and key. Receive-only for now.",
    ETHB: "Real bridged Sepolia ETH. Deposits auto-credit here. Receive and Send both ask which network: CCT network (instant, inside CreditChain) or ERC network (real Sepolia address - a bridge action moves it between the two automatically, no separate step to click).",
};

// EXTERNAL_CHAIN_SYMBOLS are not CreditChain tokens at all - they live
// on their own independent blockchain (see website/js/hdwallet.js).
// Phase A only derives and displays their address (see the Multi-Chain
// HD Wallet plan's §A.5) - no balance lookup, no send/broadcast, and
// CreditChain's own explorer must never claim to show their history
// (it's CreditChain-internal only, see api/explorer.go).
const EXTERNAL_CHAIN_SYMBOLS = { ETH: "eth", BTC: "btc" };

function descriptionFor(sym) {
    return ASSET_DESCRIPTIONS[sym] || "Custom token registered on OUTERNET.";
}

// weiToEth mirrors defi.js's helper of the same name - both convert a
// decimal-wei string into a human ETH string using BigInt (no
// float-precision loss), kept as separate small copies rather than a
// shared import since each page already loads independently and this
// is only 5 lines.
function weiToEth(weiString) {
    const wei = BigInt(weiString);
    const whole = wei / 1000000000000000000n;
    const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

function externalChainAddress(sym) {
    const key = EXTERNAL_CHAIN_SYMBOLS[sym];
    if (!key) return null;
    try {
        const accounts = JSON.parse(localStorage.getItem("cc_hd_accounts") || "null");
        return accounts && accounts[key] ? accounts[key].address : null;
    } catch (e) {
        return null;
    }
}

// assetAddress resolves which address actually holds this symbol's
// balance - CCT/REPT/custom tokens stay on the wallet's own address,
// ETHTEST/USDCTEST resolve to their derived sub-address (mirrors
// api/subaddress.go's resolveBalanceAddress), and ETH/BTC resolve to
// their own independent-blockchain address (see hdwallet.js) - none of
// these three schemes are interchangeable.
async function assetAddress() {
    if (EXTERNAL_CHAIN_SYMBOLS[symbol]) {
        return externalChainAddress(symbol) || "";
    }
    return balanceAddressForSymbol(wallet, symbol);
}

function showAssetTab(tab) {
    document.getElementById("assetTabOverviewBtn").classList.toggle("active", tab === "overview");
    document.getElementById("assetTabHistoryBtn").classList.toggle("active", tab === "history");
    document.getElementById("assetTabOverview").classList.toggle("active", tab === "overview");
    document.getElementById("assetTabHistory").classList.toggle("active", tab === "history");
}

// displaySymbol is what's shown to the user - ETHB is real bridged
// Sepolia ETH, so it's just labeled "ETH" everywhere on this page
// (the internal symbol/API calls still use "ETHB", only the on-screen
// text changes).
const displaySymbol = symbol === "ETHB" ? "ETH" : symbol;

async function loadHero() {
    document.getElementById("assetSymbol").innerText = displaySymbol;
    document.getElementById("ovSymbol").innerText = displaySymbol;
    document.getElementById("ovDescription").innerText = descriptionFor(symbol);

    if (symbol === "ETH") {
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = address || "No wallet found for this network.";

        if (address) {
            try {
                const info = await apiGet(`/eth-selfcustody/balance?address=${address}`);
                const ethStr = `${weiToEth(info.weiBalance)} ETH`;
                document.getElementById("assetBalance").innerText = ethStr;
                document.getElementById("ovBalance").innerText = ethStr;
            } catch (e) {
                console.error(e);
                document.getElementById("assetBalance").innerText = "Couldn't load balance (Sepolia RPC unavailable)";
                document.getElementById("ovBalance").innerText = "-";
            }
        }

        // Real send is now available for this self-custodial account
        // (Multi-Chain HD Wallet Phase B) - handled via a dedicated
        // send-eth.html page, wired below in the assetSendBtn listener,
        // since the flow differs at nearly every step from the
        // CreditChain-internal send.html path (local ECDSA signing,
        // 0x address format, confirm-before-broadcast step).

        // DeFi Corner has a separate, KMS-custodial Sepolia ETH account
        // (a different trust model - CreditChain holds that key, not
        // the user) - cross-linked so the two aren't confused for one
        // account.
        document.getElementById("ovOtherNetworksList").innerHTML =
            `<a href="defi.html" style="color:#8ef;">This account can send directly. DeFi Corner also has a separate, KMS-custodial Sepolia ETH account.</a>`;
        document.getElementById("ovOtherNetworks").style.display = "block";
        return;
    }

    if (symbol === "ETHB") {
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = formatAddress(address);

        try {
            const info = await apiGet(`/ethb/balance?address=${address}`);
            const ethStr = `${weiToEth(info.balance)} ETH`;
            document.getElementById("assetBalance").innerText = ethStr;
            document.getElementById("ovBalance").innerText = ethStr;
        } catch (e) {
            console.error(e);
            document.getElementById("assetBalance").innerText = "Couldn't load balance";
            document.getElementById("ovBalance").innerText = "-";
        }

        document.getElementById("ovOtherNetworksList").innerHTML =
            `<a href="send-bridged-eth.html" style="color:#8ef;">Sending picks CCT or ERC network on the send page - no separate bridge step.</a>`;
        document.getElementById("ovOtherNetworks").style.display = "block";
        return;
    }

    if (EXTERNAL_CHAIN_SYMBOLS[symbol]) {
        // BTC stays exactly receive-only (Phase C, not yet built).
        document.getElementById("assetBalance").innerText = `Phase A: receive-only, balance display not yet available`;
        document.getElementById("ovBalance").innerText = "-";
        document.getElementById("assetSendBtn").style.display = "none";
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = address || "No wallet found for this network.";

        document.getElementById("ovOtherNetworksList").innerHTML =
            `<a href="defi.html" style="color:#8ef;">Look up this address's real balance/activity in DeFi Corner. &rarr;</a>`;
        document.getElementById("ovOtherNetworks").style.display = "block";
        return;
    }

    try {
        const balance = await fetchTokenBalance(wallet, symbol);
        document.getElementById("assetBalance").innerText = `${balance.balance} ${symbol}`;
        document.getElementById("ovBalance").innerText = `${balance.balance} ${symbol}`;
    } catch (e) {
        console.error(e);
    }

    try {
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = formatAddress(address);
    } catch (e) {
        console.error(e);
    }

    // ETHTEST is CreditChain's own representation of "ETH" - the
    // group's second network (the real, self-custodial Ethereum
    // account) lives at a separate page, cross-linked here so opening
    // this one row surfaces both networks per the Multi-Chain HD
    // Wallet plan's §A.6 unified-asset UX.
    if (symbol === "ETHTEST" && externalChainAddress("ETH")) {
        document.getElementById("ovOtherNetworksList").innerHTML =
            `<a href="asset.html?symbol=ETH" style="color:#8ef;">Ethereum (self-custodial, receive-only) &rarr;</a>`;
        document.getElementById("ovOtherNetworks").style.display = "block";
    }
}

// loadChart is a compact copy of price-chart.js's loadPriceChart -
// CCT itself never gets a chart (it's the hub currency every OTHER
// token prices against, not traded against itself).
async function loadChart() {
    const container = document.getElementById("assetChartArea");

    if (EXTERNAL_CHAIN_SYMBOLS[symbol]) {
        container.innerHTML = `<p style="opacity:0.6;font-size:13px;">${symbol} is an independent blockchain, not a CreditChain-traded token - no price chart here.</p>`;
        return;
    }

    if (symbol === "CCT") {
        container.innerHTML = `<p style="opacity:0.6;font-size:13px;">Native network token - no price chart (every other token prices in CCT).</p>`;
        return;
    }

    try {
        const pools = await apiGet("/pools");
        const isTradeable = (pools || []).some((p) => p.symbol === symbol && p.tradeable);
        if (!isTradeable) {
            container.innerHTML = `<p style="opacity:0.6;font-size:13px;">No live pool yet for ${symbol}.</p>`;
            return;
        }

        const history = await apiGet(`/pool/price-history?symbol=${encodeURIComponent(symbol)}`);
        if (!history || history.length < 2) {
            container.innerHTML = `<p style="opacity:0.6;font-size:13px;">Not enough trading activity yet to chart ${symbol}.</p>`;
            return;
        }

        const prices = history.map((p) => p.price / 1e6);
        const min = Math.min(...prices);
        const max = Math.max(...prices);
        const range = max - min || max || 1;

        const width = 500;
        const height = 140;
        const pad = 8;

        const points = history
            .map((p, i) => {
                const x = pad + (i / (history.length - 1)) * (width - pad * 2);
                const y = height - pad - ((p.price / 1e6 - min) / range) * (height - pad * 2);
                return `${x.toFixed(1)},${y.toFixed(1)}`;
            })
            .join(" ");

        const first = prices[0];
        const last = prices[prices.length - 1];
        const changeColor = last >= first ? "#00ff95" : "#ff6b6b";

        container.innerHTML = `
            <svg viewBox="0 0 ${width} ${height}" style="width:100%;height:100%;display:block;">
                <polyline points="${points}" fill="none" stroke="${changeColor}" stroke-width="2.5" />
            </svg>
        `;
        document.getElementById("assetBalance").innerText += ` · 1 ${symbol} = ${last.toFixed(6)} CCT`;
    } catch (e) {
        console.error(e);
        container.innerHTML = `<p style="opacity:0.6;font-size:13px;">Couldn't load price history.</p>`;
    }
}

async function loadHistory() {
    const div = document.getElementById("assetHistoryList");

    if (EXTERNAL_CHAIN_SYMBOLS[symbol]) {
        // CreditChain's explorer is CreditChain-internal only (see
        // api/explorer.go) - it must never claim to show history for
        // an independent blockchain, so this deliberately does not
        // link out to it or query it for ETH/BTC.
        div.innerHTML = `<p style="opacity:0.6;">History for ${symbol} isn't shown here - use a ${symbol === "ETH" ? "Sepolia" : "Bitcoin testnet"} block explorer with your address above.</p>`;
        document.getElementById("assetExplorerLink").style.display = "none";
        return;
    }

    try {
        const address = await assetAddress();
        document.getElementById("assetExplorerLink").href = `explorer.html?q=${address}`;

        const result = await apiGet(`/explorer/search?q=${address}`);
        const txs = result && result.type === "address" ? (result.address.transactions || []).slice(0, 7) : [];

        if (txs.length === 0) {
            div.innerHTML = `<p style="opacity:0.6;">No transactions yet.</p>`;
            return;
        }

        div.innerHTML = txs
            .map((tx) => {
                const time = tx.Timestamp ? new Date(tx.Timestamp * 1000).toLocaleString() : "-";
                const label = tx.summary || tx.Type;
                return `
                    <div class="asset-history-row">
                        <span>${label}</span>
                        <span style="opacity:0.6;">${time}</span>
                    </div>
                `;
            })
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<p style="opacity:0.6;">Couldn't load history.</p>`;
    }
}

async function toggleReceivePanel() {
    const panel = document.getElementById("assetReceivePanel");
    if (panel.style.display === "block") {
        panel.style.display = "none";
        return;
    }

    // ETHB is a special case: deposits auto-credit from two different
    // places depending on network, so Receive asks which one first
    // instead of guessing - CCT network is this wallet's internal
    // CreditChain sub-address, ERC network is the real custodial
    // Sepolia address. Resolving the wrong one here as if it were the
    // only option would mean funds sent to it could be unrecoverable.
    if (symbol === "ETHB") {
        document.getElementById("assetReceiveNetworkPicker").style.display = "block";
        document.getElementById("assetReceiveQrArea").style.display = "none";
        panel.style.display = "block";
        return;
    }

    const rawAddress = await assetAddress();
    const address = EXTERNAL_CHAIN_SYMBOLS[symbol] ? rawAddress : formatAddress(rawAddress);
    document.getElementById("assetReceiveAddress").innerText = address;
    QR.renderToCanvas(document.getElementById("assetQrCanvas"), address, 6);
    panel.style.display = "block";
}

// loadReceiveForNetwork resolves the actual deposit address once the
// user picks a network for ETHB's Receive flow. CCT network reuses
// assetAddress()'s normal CreditChain sub-address (instant, internal
// sends only). ERC network needs the real custodial Sepolia address -
// real Sepolia ETH sent there is what auto-credits the ETH balance.
// /eth-custodial/create is idempotent (returns the existing address if
// one's already provisioned) and, unlike /eth-custodial/balance,
// doesn't require a live Sepolia RPC connection just to reveal an
// address that's already known.
async function loadReceiveForNetwork(network) {
    document.getElementById("assetReceiveNetworkPicker").style.display = "none";
    document.getElementById("assetReceiveQrArea").style.display = "block";
    const statusEl = document.getElementById("assetCopyStatus");
    const addressEl = document.getElementById("assetReceiveAddress");

    if (network === "CCT") {
        const address = await assetAddress();
        addressEl.innerText = address;
        QR.renderToCanvas(document.getElementById("assetQrCanvas"), address, 6);
        statusEl.innerText = "CCT network address - instant, for CreditChain-internal sends only.";
        return;
    }

    statusEl.innerText = "Loading your deposit address...";
    try {
        const info = await apiPostSigned("/eth-custodial/create", {});
        addressEl.innerText = info.ethAddress;
        QR.renderToCanvas(document.getElementById("assetQrCanvas"), info.ethAddress, 6);
        statusEl.innerText = "Real Sepolia ETH sent here is auto-credited to your ETH balance within a few minutes.";
    } catch (e) {
        console.error(e);
        addressEl.innerText = "";
        statusEl.innerText = "Couldn't load your deposit address: " + e.message;
    }
}

function copyAssetAddress() {
    const address = document.getElementById("assetReceiveAddress").innerText;
    if (navigator.clipboard) {
        navigator.clipboard
            .writeText(address)
            .then(() => {
                document.getElementById("assetCopyStatus").innerText = "Copied!";
            })
            .catch(() => {});
    }
}

document.getElementById("assetSendBtn").addEventListener("click", () => {
    if (symbol === "ETH") {
        location.href = "send-eth.html";
        return;
    }
    if (symbol === "ETHB") {
        location.href = "send-bridged-eth.html";
        return;
    }
    location.href = `send.html?symbol=${symbol}`;
});
document.getElementById("assetReceiveBtn").addEventListener("click", toggleReceivePanel);

loadHero();
loadChart();
loadHistory();
