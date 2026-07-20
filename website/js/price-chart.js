// pcSelectedSymbol tracks the user's current pick so a periodic
// symbol-list refresh (new tokens/pools appearing) doesn't yank the
// selector away from whatever they're currently looking at.
let pcSelectedSymbol = null;

async function loadChartSymbols() {
    try {
        const pools = await apiGet("/pools");
        const tradeableSymbols = (pools || []).filter((p) => p.tradeable).map((p) => p.symbol);

        const select = document.getElementById("pcTokenSymbol");
        const previous = pcSelectedSymbol || select.value;

        if (tradeableSymbols.length === 0) {
            select.innerHTML = `<option value="">No tradeable tokens yet</option>`;
            document.getElementById("pcChartContainer").innerHTML =
                `<p class="pool-empty">No token has a live pool yet - add liquidity on the Liquidity Pool page first.</p>`;
            return;
        }

        select.innerHTML = tradeableSymbols.map((s) => `<option value="${s}">${s}</option>`).join("");
        select.value = tradeableSymbols.includes(previous) ? previous : tradeableSymbols[0];
        pcSelectedSymbol = select.value;

        loadPriceChart(pcSelectedSymbol);
    } catch (e) {
        console.error(e);
    }
}

// loadPriceChart draws a hand-built inline SVG line chart from
// /pool/price-history - no external charting library, consistent with
// this codebase's zero-unaudited-dependency convention. Price points
// are CCT-per-token, scaled by 1e6 server-side (see
// core/price_history.go's priceScale), so divide back down here.
// containerId defaults to this page's own chart container so existing
// callers are unaffected - order-book.js passes its own container ID
// to reuse this same renderer for order-book trade prices.
async function loadPriceChart(symbol, containerId) {
    const container = document.getElementById(containerId || "pcChartContainer");
    if (!symbol) {
        container.innerHTML = "";
        return;
    }

    try {
        const history = await apiGet(`/pool/price-history?symbol=${encodeURIComponent(symbol)}`);
        if (!history || history.length < 2) {
            container.innerHTML = `<p class="pool-empty">Not enough trading activity yet to chart ${symbol}.</p>`;
            return;
        }

        const prices = history.map((p) => p.price / 1e6);
        const min = Math.min(...prices);
        const max = Math.max(...prices);
        const range = max - min || max || 1;

        const width = 700;
        const height = 260;
        const pad = 12;

        const points = history
            .map((p, i) => {
                const x = pad + (i / (history.length - 1)) * (width - pad * 2);
                const y = height - pad - ((p.price / 1e6 - min) / range) * (height - pad * 2);
                return `${x.toFixed(1)},${y.toFixed(1)}`;
            })
            .join(" ");

        const first = prices[0];
        const last = prices[prices.length - 1];
        const changeColor = last >= first ? "#059669" : "#dc2626";
        const changePct = first !== 0 ? (((last - first) / first) * 100).toFixed(2) : "0.00";

        container.innerHTML = `
            <svg viewBox="0 0 ${width} ${height}" style="width:100%;height:260px;display:block;">
                <polyline points="${points}" fill="none" stroke="${changeColor}" stroke-width="2.5" />
            </svg>
            <p style="font-size:13px;color:#374151;margin-top:10px;">
                1 ${symbol} = ${last.toFixed(6)} CCT
                <span style="color:${changeColor};font-weight:700;">(${last >= first ? "+" : ""}${changePct}% over ${history.length} recorded points)</span>
            </p>
        `;
    } catch (e) {
        console.error(e);
        container.innerHTML = `<p class="pool-empty">Couldn't load price history.</p>`;
    }
}

// This page-level bootstrapping only applies to price-chart.html
// itself - order-book.js also loads this file (to reuse
// loadPriceChart for its own trade-price chart) but has no
// "pcTokenSymbol" selector of its own, so it drives loadPriceChart
// directly instead.
const pcTokenSymbolEl = document.getElementById("pcTokenSymbol");
if (pcTokenSymbolEl) {
    pcTokenSymbolEl.addEventListener("change", (e) => {
        pcSelectedSymbol = e.target.value;
        loadPriceChart(pcSelectedSymbol);
    });

    loadChartSymbols();
    setInterval(loadChartSymbols, 15000);
}
