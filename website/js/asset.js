const wallet = requireWallet();

const symbol = new URLSearchParams(window.location.search).get("symbol") || "CCT";

const ASSET_DESCRIPTIONS = {
    CCT: "CreditChain's native network token - the universal hub every swap prices through.",
    REPT: "Reputation-progression token, minted as a one-time reward the first time your reputation reaches a new credit tier.",
    ETHTEST: "Imported test ETH token - lives at its own address derived from this wallet's key, separate from your CCT address.",
    USDCTEST: "Imported test USDC token - lives at its own address derived from this wallet's key, separate from your CCT address.",
};

function descriptionFor(sym) {
    return ASSET_DESCRIPTIONS[sym] || "Custom token registered on OUTERNET.";
}

// assetAddress resolves which address actually holds this symbol's
// balance - CCT/REPT/custom tokens stay on the wallet's own address,
// only ETHTEST/USDCTEST resolve to their derived sub-address (mirrors
// api/subaddress.go's resolveBalanceAddress).
async function assetAddress() {
    return balanceAddressForSymbol(wallet, symbol);
}

function showAssetTab(tab) {
    document.getElementById("assetTabOverviewBtn").classList.toggle("active", tab === "overview");
    document.getElementById("assetTabHistoryBtn").classList.toggle("active", tab === "history");
    document.getElementById("assetTabOverview").classList.toggle("active", tab === "overview");
    document.getElementById("assetTabHistory").classList.toggle("active", tab === "history");
}

async function loadHero() {
    document.getElementById("assetSymbol").innerText = symbol;
    document.getElementById("ovSymbol").innerText = symbol;
    document.getElementById("ovDescription").innerText = descriptionFor(symbol);

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
}

// loadChart is a compact copy of price-chart.js's loadPriceChart -
// CCT itself never gets a chart (it's the hub currency every OTHER
// token prices against, not traded against itself).
async function loadChart() {
    const container = document.getElementById("assetChartArea");

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

    const address = formatAddress(await assetAddress());
    document.getElementById("assetReceiveAddress").innerText = address;
    QR.renderToCanvas(document.getElementById("assetQrCanvas"), address, 6);
    panel.style.display = "block";
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
    location.href = `send.html?symbol=${symbol}`;
});
document.getElementById("assetReceiveBtn").addEventListener("click", toggleReceivePanel);

loadHero();
loadChart();
loadHistory();
