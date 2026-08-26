const plWallet = getWallet();

// nativeTokenSymbols are always swappable/poolable, even before any
// developer registers a custom token - see api/token.go's
// cctEthTestPool/cctUsdcTestPool.
const nativeTokenSymbols = ["ETHTEST", "USDCTEST"];

function plInit() {
    if (!plWallet) {
        document.getElementById("poolLoggedOut").style.display = "block";
        document.getElementById("poolLoggedIn").style.display = "none";
        return;
    }
    document.getElementById("poolActionsCard").style.display = "block";
    document.getElementById("swapCard").style.display = "block";
    document.getElementById("priceChartCard").style.display = "block";
    loadSymbols();
}

async function loadSymbols() {
    try {
        const [tokens, pools] = await Promise.all([apiGet("/tokens"), apiGet("/pools")]);
        const poolSelect = document.getElementById("plPoolSymbol");
        const swapFromSelect = document.getElementById("plSwapFrom");
        const swapToSelect = document.getElementById("plSwapTo");

        const customSymbols = (tokens || []).map((t) => t.symbol);
        const poolableSymbols = [...nativeTokenSymbols, ...customSymbols];

        // Only a symbol that ALREADY has liquidity can actually be
        // swapped - registering a token or importing ETHTEST/USDCTEST
        // doesn't make it tradeable by itself (see api/token_registry.go's
        // isTradeable). Listing it in the swap dropdowns before that
        // would let someone submit a swap that's guaranteed to silently
        // do nothing once confirmed. CCT itself needs no pool - it's the
        // hub every pool is paired against.
        const tradeableSymbols = new Set((pools || []).filter((p) => p.tradeable).map((p) => p.symbol));
        const swappableSymbols = ["CCT", ...poolableSymbols.filter((s) => tradeableSymbols.has(s))];

        const currentPoolSelection = poolSelect.value;
        poolSelect.innerHTML = poolableSymbols.map((s) => `<option value="${s}">${s}</option>`).join("");
        if (poolableSymbols.includes(currentPoolSelection)) {
            poolSelect.value = currentPoolSelection;
        }

        const currentFrom = swapFromSelect.value;
        const currentTo = swapToSelect.value;
        swapFromSelect.innerHTML = swappableSymbols.map((s) => `<option value="${s}">${s}</option>`).join("");
        swapToSelect.innerHTML = swappableSymbols.map((s) => `<option value="${s}">${s}</option>`).join("");
        swapFromSelect.value = swappableSymbols.includes(currentFrom) ? currentFrom : "CCT";
        swapToSelect.value = swappableSymbols.includes(currentTo)
            ? currentTo
            : swappableSymbols.find((s) => s !== "CCT") || "CCT";

        if (poolSelect.value) {
            loadPoolInfo(poolSelect.value);
        }
        if (swapFromSelect.value) {
            loadSwapFromBalance(swapFromSelect.value);
        }
    } catch (e) {
        console.error(e);
    }
}

async function loadSwapFromBalance(symbol) {
    if (!plWallet || !symbol) {
        document.getElementById("plSwapFromBalance").innerText = "";
        return;
    }
    try {
        const balance = await fetchTokenBalance(plWallet, symbol);
        const displayBalance = symbol === "CCT" ? cctFromSmallest(balance.balance) : balance.balance;
        document.getElementById("plSwapFromBalance").innerText = `Your balance: ${displayBalance} ${symbol}`;
    } catch (e) {
        console.error(e);
    }
}

let plCurrentPoolTradeable = false;

async function loadPoolInfo(symbol) {
    if (!symbol) return;

    try {
        const info = await apiGet(
            `/pool?symbol=${encodeURIComponent(symbol)}&address=${plWallet ? plWallet.address : ""}`
        );
        plCurrentPoolTradeable = info.tradeable;

        // cctReserve/totalShares/yourShares are 18-decimal smallest-unit
        // decimal strings now (see api/token_registry.go's poolInfo) -
        // convert to a human CCT amount before doing any arithmetic or
        // display, same convention as a CCT balance.
        const cctReserveDisplay = cctFromSmallest(info.cctReserve);
        const statusLine = info.tradeable
            ? `Tradeable - 1 ${symbol} = ${(parseFloat(cctReserveDisplay) / info.tokenReserve).toFixed(4)} CCT`
            : "Not tradeable yet - add liquidity below to give it a price.";

        let balanceLine = "";
        if (plWallet) {
            const [cctBalance, tokenBalance] = await Promise.all([
                fetchTokenBalance(plWallet, "CCT"),
                fetchTokenBalance(plWallet, symbol),
            ]);
            balanceLine = `<p>Your balance: ${cctFromSmallest(cctBalance.balance)} CCT / ${tokenBalance.balance} ${symbol}</p>`;
        }

        document.getElementById("plPoolInfo").innerHTML = `
            <p>Reserves: ${cctReserveDisplay} CCT / ${info.tokenReserve} ${symbol}</p>
            <p style="color:${info.tradeable ? "#8f8" : "#f88"};">${statusLine}</p>
            <p>Your LP shares: ${info.yourShares ? cctFromSmallest(info.yourShares) : 0} of ${cctFromSmallest(info.totalShares)} total</p>
            ${balanceLine}
        `;

        loadPriceChart(symbol);
    } catch (e) {
        console.error(e);
    }
}

// loadPriceChart draws a hand-built inline SVG line chart from
// /pool/price-history - no external charting library, consistent with
// this codebase's zero-unaudited-dependency convention. Price points
// are CCT-per-token, scaled by 1e6 server-side (see
// core/price_history.go's priceScale), so divide back down here.
async function loadPriceChart(symbol) {
    const container = document.getElementById("pcChartContainer");
    document.getElementById("pcSymbolLabel").innerText = symbol ? `(${symbol})` : "";
    if (!symbol) {
        container.innerHTML = "";
        return;
    }

    try {
        const history = await apiGet(`/pool/price-history?symbol=${encodeURIComponent(symbol)}`);
        if (!history || history.length < 2) {
            container.innerHTML = `<p class="explorer-empty">Not enough trading activity yet to chart ${symbol}.</p>`;
            return;
        }

        const prices = history.map((p) => p.price / 1e6);
        const min = Math.min(...prices);
        const max = Math.max(...prices);
        const range = max - min || max || 1;

        const width = 700;
        const height = 220;
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
        const changeColor = last >= first ? "#0f9d58" : "#ff5f5f";
        const changePct = first !== 0 ? (((last - first) / first) * 100).toFixed(2) : "0.00";

        container.innerHTML = `
            <svg viewBox="0 0 ${width} ${height}" style="width:100%;height:220px;display:block;">
                <polyline points="${points}" fill="none" stroke="${changeColor}" stroke-width="2" />
            </svg>
            <p style="opacity:0.8;font-size:13px;">
                1 ${symbol} = ${last.toFixed(6)} CCT
                <span style="color:${changeColor};">(${last >= first ? "+" : ""}${changePct}% over ${history.length} recorded points)</span>
            </p>
        `;
    } catch (e) {
        console.error(e);
        container.innerHTML = `<p class="explorer-empty">Couldn't load price history.</p>`;
    }
}

async function addLiquidity() {
    const symbol = document.getElementById("plPoolSymbol").value;
    const cctAmountStr = document.getElementById("plAddCCT").value.trim();
    const tokenAmount = parseInt(document.getElementById("plAddToken").value, 10);
    const statusEl = document.getElementById("plPoolStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (!cctAmountStr || !tokenAmount) {
        statusEl.innerText = "Enter both a CCT amount and a token amount.";
        return;
    }

    try {
        const result = await apiPostSigned("/pool/add-liquidity", {
            symbol,
            // cctAmount is now an 18-decimal smallest-unit string (see
            // api/token_registry.go's poolAddLiquidityData) - tokenAmount
            // (the OTHER leg, always non-CCT) stays a plain whole uint64.
            cctAmount: cctToSmallest(cctAmountStr),
            tokenAmount,
        });
        statusEl.innerText = `Liquidity pending confirmation (tx ${result.txId}).`;
        setTimeout(() => loadPoolInfo(symbol), 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function removeLiquidity() {
    const symbol = document.getElementById("plPoolSymbol").value;
    const sharesStr = document.getElementById("plRemoveShares").value.trim();
    const statusEl = document.getElementById("plPoolStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (!sharesStr) {
        statusEl.innerText = "Enter shares to remove.";
        return;
    }

    try {
        // Shares use the exact same 18-decimal smallest-unit decimal
        // string encoding as CCT itself (see core/liquidity_pool.go) -
        // the user types the same human amount shown by loadPoolInfo's
        // "Your LP shares" line, so it round-trips through cctToSmallest
        // the same way a CCT amount would.
        const result = await apiPostSigned("/pool/remove-liquidity", { symbol, shares: cctToSmallest(sharesStr) });
        statusEl.innerText = `Removal pending confirmation (tx ${result.txId}).`;
        setTimeout(() => loadPoolInfo(symbol), 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function swapInPool() {
    const symbol = document.getElementById("plPoolSymbol").value;
    const direction = document.getElementById("plSwapDirection").value;
    const amountInStr = document.getElementById("plSwapAmount").value.trim();
    const statusEl = document.getElementById("plPoolStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (!amountInStr) {
        statusEl.innerText = "Enter an amount to swap.";
        return;
    }
    if (!plCurrentPoolTradeable) {
        statusEl.innerText = `${symbol} isn't tradeable yet - add liquidity first.`;
        return;
    }

    // AmountIn is a decimal string regardless of direction - CCT
    // smallest units for "cct_to_token", plain whole units of symbol
    // for "token_to_cct" (see api/token_registry.go's poolSwapData doc
    // comment) - only the CCT leg needs the 18-decimal conversion.
    const amountIn = direction === "cct_to_token" ? cctToSmallest(amountInStr) : String(parseInt(amountInStr, 10));

    try {
        const result = await apiPostSigned("/pool/swap", { symbol, direction, amountIn });
        statusEl.innerText = `Swap pending confirmation (tx ${result.txId}).`;
        setTimeout(() => loadPoolInfo(symbol), 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function swapViaCCT() {
    const fromSymbol = document.getElementById("plSwapFrom").value;
    const toSymbol = document.getElementById("plSwapTo").value;
    const amountInStr = document.getElementById("plSwapViaAmount").value.trim();
    const statusEl = document.getElementById("plSwapViaStatus");

    if (fromSymbol === toSymbol) {
        statusEl.innerText = "Pick two different tokens.";
        return;
    }
    if (!amountInStr) {
        statusEl.innerText = "Enter an amount to swap.";
        return;
    }

    // Same one-wire-shape-for-both convention as poolSwapData.AmountIn -
    // CCT smallest units when fromSymbol is CCT, plain whole units of
    // fromSymbol otherwise (see api/token_registry.go's swapViaCCTData).
    const amountIn = fromSymbol === "CCT" ? cctToSmallest(amountInStr) : String(parseInt(amountInStr, 10));

    try {
        const result = await apiPostSigned("/swap", { fromSymbol, toSymbol, amountIn });
        statusEl.innerText = `Swap pending confirmation (tx ${result.txId}).`;
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

document.getElementById("plAddLiquidityBtn").addEventListener("click", addLiquidity);
document.getElementById("plRemoveLiquidityBtn").addEventListener("click", removeLiquidity);
document.getElementById("plSwapBtn").addEventListener("click", swapInPool);
document.getElementById("plSwapViaBtn").addEventListener("click", swapViaCCT);
document.getElementById("plPoolSymbol").addEventListener("change", (e) => loadPoolInfo(e.target.value));
document.getElementById("plSwapFrom").addEventListener("change", (e) => loadSwapFromBalance(e.target.value));

plInit();
setInterval(loadSymbols, 15000);
