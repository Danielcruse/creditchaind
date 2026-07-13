const obWallet = getWallet();

// PRICE_SCALE mirrors core/price_history.go's priceScale - order prices
// are transmitted to the API as whole integers scaled by 1e6 (CCT per
// unit, six decimal digits of precision), same unit price-chart.js's
// history points already use.
const PRICE_SCALE = 1e6;

let obSelectedSymbol = "ETHTEST";

function obInit() {
    if (!obWallet) {
        document.getElementById("obLoggedOut").style.display = "block";
        document.getElementById("obLoggedIn").style.display = "none";
    }
    obSelectedSymbol = document.getElementById("obSymbol").value;
    obLoadAll();
}

function obLoadAll() {
    loadPriceChart(obSelectedSymbol, "obChartContainer");
    obLoadDepth();
    obLoadTrades();
    if (obWallet) {
        obLoadBalance();
        obLoadMyOrders();
    }
}

async function obLoadBalance() {
    const el = document.getElementById("obBalance");
    try {
        const [cct, token] = await Promise.all([
            fetchTokenBalance(obWallet, "CCT"),
            fetchTokenBalance(obWallet, obSelectedSymbol),
        ]);
        el.innerText = `Your balance: ${cct.balance} CCT, ${token.balance} ${obSelectedSymbol}`;
    } catch (e) {
        console.error(e);
        el.innerText = "";
    }
}

function obRenderSide(containerId, orders, side) {
    const container = document.getElementById(containerId);
    if (!orders || orders.length === 0) {
        container.innerHTML = `<p class="pool-empty">No open ${side === "BUY" ? "bids" : "asks"}.</p>`;
        return;
    }
    const rows = orders
        .filter((o) => o.Side === side)
        .map(
            (o) => `
                <div style="display:flex;justify-content:space-between;font-size:13px;padding:4px 0;border-bottom:1px solid #f3f4f6;">
                    <span>${(o.Price / PRICE_SCALE).toFixed(6)} CCT</span>
                    <span class="pool-muted">${o.Remaining} ${o.Symbol}</span>
                </div>`
        )
        .join("");
    container.innerHTML = rows || `<p class="pool-empty">No open ${side === "BUY" ? "bids" : "asks"}.</p>`;
}

async function obLoadDepth() {
    try {
        const orders = await apiGet(`/orders?symbol=${encodeURIComponent(obSelectedSymbol)}`);
        // Bids sort highest-first (best bid on top), asks sort
        // lowest-first (best ask on top) - purely a display
        // convenience, the matching engine itself doesn't care how
        // this list is ordered once it's read back in.
        const bids = (orders || []).filter((o) => o.Side === "BUY").sort((a, b) => b.Price - a.Price);
        const asks = (orders || []).filter((o) => o.Side === "SELL").sort((a, b) => a.Price - b.Price);
        obRenderSide("obBids", bids, "BUY");
        obRenderSide("obAsks", asks, "SELL");
    } catch (e) {
        console.error(e);
    }
}

async function obLoadTrades() {
    const container = document.getElementById("obTrades");
    try {
        const trades = await apiGet(`/trades?symbol=${encodeURIComponent(obSelectedSymbol)}`);
        if (!trades || trades.length === 0) {
            container.innerHTML = `<p class="pool-empty">No trades yet for ${obSelectedSymbol}.</p>`;
            return;
        }
        const rows = trades
            .slice()
            .reverse()
            .slice(0, 25)
            .map(
                (t) => `
                    <div style="display:flex;justify-content:space-between;font-size:13px;padding:4px 0;border-bottom:1px solid #f3f4f6;">
                        <span>${(t.Price / PRICE_SCALE).toFixed(6)} CCT</span>
                        <span class="pool-muted">${t.Amount} ${t.Symbol}</span>
                        <span class="pool-muted">${new Date(t.Timestamp * 1000).toLocaleString()}</span>
                    </div>`
            )
            .join("");
        container.innerHTML = rows;
    } catch (e) {
        console.error(e);
        container.innerHTML = `<p class="pool-empty">Couldn't load trade history.</p>`;
    }
}

async function obLoadMyOrders() {
    const container = document.getElementById("obMyOrders");
    try {
        const orders = await apiGet(
            `/orders/mine?address=${obWallet.address}&symbol=${encodeURIComponent(obSelectedSymbol)}`
        );
        const open = (orders || []).filter((o) => o.Status === "OPEN");
        if (open.length === 0) {
            container.innerHTML = `<p class="pool-empty">No open orders.</p>`;
            return;
        }
        container.innerHTML = open
            .map(
                (o) => `
                    <div style="display:flex;justify-content:space-between;align-items:center;font-size:13px;padding:6px 0;border-bottom:1px solid #f3f4f6;">
                        <span style="color:${o.Side === "BUY" ? "#059669" : "#dc2626"};font-weight:700;">${o.Side}</span>
                        <span>${(o.Price / PRICE_SCALE).toFixed(6)} CCT</span>
                        <span class="pool-muted">${o.Remaining}/${o.Amount} ${o.Symbol}</span>
                        <button class="pool-btn pool-btn-secondary" onclick="obCancelOrder('${o.ID}')">Cancel</button>
                    </div>`
            )
            .join("");
    } catch (e) {
        console.error(e);
        container.innerHTML = `<p class="pool-empty">Couldn't load your orders.</p>`;
    }
}

async function obPlaceOrder() {
    const side = document.getElementById("obSide").value;
    const priceFloat = parseFloat(document.getElementById("obPrice").value);
    const amount = parseInt(document.getElementById("obAmount").value, 10);
    const statusEl = document.getElementById("obPlaceStatus");

    if (!priceFloat || priceFloat <= 0) {
        statusEl.innerText = "Enter a limit price.";
        return;
    }
    if (!amount || amount <= 0) {
        statusEl.innerText = "Enter an amount.";
        return;
    }

    const price = Math.round(priceFloat * PRICE_SCALE);

    try {
        const result = await apiPostSigned("/orders/place", {
            symbol: obSelectedSymbol,
            side,
            price,
            amount,
        });
        statusEl.innerText = `Order pending confirmation (tx ${result.txId}).`;
        setTimeout(obLoadAll, 3000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function obCancelOrder(orderId) {
    const statusEl = document.getElementById("obCancelStatus");
    try {
        const result = await apiPostSigned("/orders/cancel", { orderId });
        statusEl.innerText = `Cancel pending confirmation (tx ${result.txId}).`;
        setTimeout(obLoadAll, 3000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

document.getElementById("obSymbol").addEventListener("change", (e) => {
    obSelectedSymbol = e.target.value;
    obLoadAll();
});
document.getElementById("obPlaceBtn").addEventListener("click", obPlaceOrder);

obInit();
setInterval(obLoadAll, 15000);
