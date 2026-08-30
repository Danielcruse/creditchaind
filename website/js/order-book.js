const obWallet = getWallet();

// PRICE_SCALE mirrors core/price_history.go's priceScale - order prices
// are transmitted to the API as whole integers scaled by 1e6 (CCT per
// unit, six decimal digits of precision), same unit price-chart.js's
// history points already use.
const PRICE_SCALE = 1e6;

// OB_SYMBOLS mirrors p2pNativeSymbols' own network branch
// (website/js/p2p.js): testnet trades against the synthetic
// ETHTEST/USDCTEST faucet tokens, mainnet against their real,
// vault-locked 1:1-backed equivalents ETH_CCC1/USDC_CCC1 (see
// api/order_book.go's orderBookSymbols). Labels stay the friendly
// "ETH"/"USDC" on mainnet, matching every other CCC1 display in this
// app (p2pSymbolLabels) - only the underlying value sent to the API
// differs.
const OB_SYMBOLS =
    window.CC_NETWORK === "mainnet"
        ? [
              { value: "ETH_CCC1", label: "ETH" },
              { value: "USDC_CCC1", label: "USDC" },
          ]
        : [
              { value: "ETHTEST", label: "ETHTEST" },
              { value: "USDCTEST", label: "USDCTEST" },
          ];

let obSelectedSymbol = OB_SYMBOLS[0].value;

// obUpdateCcc1UnitNote warns before the trader types anything - Amount
// and Price still operate in the token's raw smallest unit for
// ETH_CCC1/USDC_CCC1 (1 ETH = 1000000000000000000), not human ETH/USDC
// decimals, since Price's own precision (core/price_history.go's
// priceScale, 6 decimal digits) can't represent a sane "CCT per wei"
// price - the same rescaling gap already flagged for hub-rate trading
// (website/js/p2p.js) would need to land here too before this can take
// human-decimal input directly.
function obUpdateCcc1UnitNote() {
    const note = document.getElementById("obCcc1UnitNote");
    const decimals = OB_CCC1_DECIMALS[obSelectedSymbol];
    note.style.display = decimals ? "block" : "none";
    if (decimals) {
        note.innerText = `Amount and price are in ${obSelectedSymbol}'s smallest unit (10^-${decimals}), not whole ${OB_SYMBOLS.find((s) => s.value === obSelectedSymbol).label} - see your balance below for the raw number to work from.`;
    }
}

function obInit() {
    if (!obWallet) {
        document.getElementById("obLoggedOut").style.display = "block";
        document.getElementById("obLoggedIn").style.display = "none";
    }
    const select = document.getElementById("obSymbol");
    select.innerHTML = OB_SYMBOLS.map((s) => `<option value="${s.value}">${s.label}</option>`).join("");
    obSelectedSymbol = select.value;
    obUpdateCcc1UnitNote();
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

// OB_CCC1_CHAIN maps an order-book symbol to the vault "chain" key
// fetchCcc1Balance (vault.js) expects - ETH_CCC1/USDC_CCC1 balances
// live behind the vault (see vault.js's own doc comment), not the
// generic custom-token registry fetchTokenBalance falls back to for
// any symbol it doesn't special-case, which 404s for these.
const OB_CCC1_CHAIN = { ETH_CCC1: "ETH", USDC_CCC1: "USDC" };
const OB_CCC1_DECIMALS = { ETH_CCC1: 18, USDC_CCC1: 6 };

// obBalanceFor fetches the correct balance for whichever kind of
// symbol is selected - raw testnet token store for ETHTEST/USDCTEST,
// the vault-backed CCC1 balance otherwise. Returns the raw smallest-
// unit string either way - Amount/Price below are still expressed in
// that same raw unit (see the note rendered next to those inputs);
// full human-decimal entry needs the same amount/price rescaling
// already flagged as a follow-up for hub rates (website/js/p2p.js),
// not something this fix attempts.
async function obBalanceFor(symbol) {
    const chain = OB_CCC1_CHAIN[symbol];
    if (chain) {
        const { balance } = await fetchCcc1Balance(obWallet, chain);
        return balance || "0";
    }
    const { balance } = await fetchTokenBalance(obWallet, symbol);
    return String(balance || 0);
}

async function obLoadBalance() {
    const el = document.getElementById("obBalance");
    try {
        const [cct, tokenBalanceRaw] = await Promise.all([
            fetchTokenBalance(obWallet, "CCT"),
            obBalanceFor(obSelectedSymbol),
        ]);
        const decimals = OB_CCC1_DECIMALS[obSelectedSymbol];
        const humanHint = decimals ? ` (≈${smallestToDecimal(tokenBalanceRaw, decimals)} ${OB_SYMBOLS.find((s) => s.value === obSelectedSymbol).label})` : "";
        el.innerText = `Your balance: ${cctFromSmallest(cct.balance)} CCT, ${tokenBalanceRaw} ${obSelectedSymbol}${humanHint}`;
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
                        <span style="color:${o.Side === "BUY" ? "#16A34A" : "#dc2626"};font-weight:700;">${o.Side}</span>
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
    obUpdateCcc1UnitNote();
    obLoadAll();
});
document.getElementById("obPlaceBtn").addEventListener("click", obPlaceOrder);

obInit();
setInterval(obLoadAll, 15000);
