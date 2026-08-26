const swapWallet = getWallet();

// nativeTokenSymbols are always poolable, even before any developer
// registers a custom token - see api/token.go's
// cctEthTestPool/cctUsdcTestPool.
const nativeTokenSymbols = ["ETHTEST", "USDCTEST"];

// orderBookOnlySymbols price on the real bid/ask order book now
// (order-book.html) instead of this AMM - see liquidity.js's matching
// constant/doc comment. The pool itself isn't touched, only the
// swap UI stops offering these two as a swap symbol.
const orderBookOnlySymbols = new Set(["ETHTEST", "USDCTEST"]);

let swapCurrentPoolTradeable = false;

function swapInit() {
    if (!swapWallet) {
        document.getElementById("swapLoggedOut").style.display = "block";
        document.getElementById("swapLoggedIn").style.display = "none";
        return;
    }
    loadSwapSymbols();
}

async function loadSwapSymbols() {
    try {
        const [tokens, pools] = await Promise.all([apiGet("/tokens"), apiGet("/pools")]);
        const poolSelect = document.getElementById("swapPoolSymbol");
        const swapFromSelect = document.getElementById("swapFrom");
        const swapToSelect = document.getElementById("swapTo");

        const customSymbols = (tokens || []).map((t) => t.symbol);
        // ETHTEST/USDCTEST are deliberately excluded here - they price
        // on the order book now (order-book.html), not this AMM's swap
        // UI. Existing pool reserves for them are untouched (still used
        // internally for unstake redemption), only the swap form stops
        // offering them.
        const poolableSymbols = [...nativeTokenSymbols.filter((s) => !orderBookOnlySymbols.has(s)), ...customSymbols];

        // Only a symbol that ALREADY has liquidity can actually be
        // swapped - registering a token or importing ETHTEST/USDCTEST
        // doesn't make it tradeable by itself (see api/token_registry.go's
        // isTradeable). CCT itself needs no pool - it's the hub every
        // pool is paired against.
        const tradeableSymbols = new Set((pools || []).filter((p) => p.tradeable).map((p) => p.symbol));
        const swappableSymbols = ["CCT", ...poolableSymbols.filter((s) => tradeableSymbols.has(s))];

        const currentPoolSelection = poolSelect.value;
        poolSelect.innerHTML = poolableSymbols.map((s) => `<option value="${s}">${s}</option>`).join("");
        if (poolableSymbols.includes(currentPoolSelection)) {
            poolSelect.value = currentPoolSelection;
        }
        if (poolSelect.value) {
            checkPoolTradeable(poolSelect.value);
        }

        const currentFrom = swapFromSelect.value;
        const currentTo = swapToSelect.value;
        swapFromSelect.innerHTML = swappableSymbols.map((s) => `<option value="${s}">${s}</option>`).join("");
        swapToSelect.innerHTML = swappableSymbols.map((s) => `<option value="${s}">${s}</option>`).join("");
        swapFromSelect.value = swappableSymbols.includes(currentFrom) ? currentFrom : "CCT";
        swapToSelect.value = swappableSymbols.includes(currentTo)
            ? currentTo
            : swappableSymbols.find((s) => s !== "CCT") || "CCT";

        if (swapFromSelect.value) {
            loadSwapFromBalance(swapFromSelect.value);
        }
    } catch (e) {
        console.error(e);
    }
}

async function checkPoolTradeable(symbol) {
    try {
        const info = await apiGet(`/pool?symbol=${encodeURIComponent(symbol)}`);
        swapCurrentPoolTradeable = info.tradeable;
    } catch (e) {
        console.error(e);
    }
}

async function loadSwapFromBalance(symbol) {
    if (!swapWallet || !symbol) {
        document.getElementById("swapFromBalance").innerText = "";
        return;
    }
    try {
        const balance = await fetchTokenBalance(swapWallet, symbol);
        const displayBalance = symbol === "CCT" ? cctFromSmallest(balance.balance) : balance.balance;
        document.getElementById("swapFromBalance").innerText = `Your balance: ${displayBalance} ${symbol}`;
    } catch (e) {
        console.error(e);
    }
}

async function swapInPool() {
    const symbol = document.getElementById("swapPoolSymbol").value;
    const direction = document.getElementById("swapDirection").value;
    const amountInStr = document.getElementById("swapAmount").value.trim();
    const statusEl = document.getElementById("swapStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (!amountInStr) {
        statusEl.innerText = "Enter an amount to swap.";
        return;
    }
    if (!swapCurrentPoolTradeable) {
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
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function swapViaCCT() {
    const fromSymbol = document.getElementById("swapFrom").value;
    const toSymbol = document.getElementById("swapTo").value;
    const amountInStr = document.getElementById("swapViaAmount").value.trim();
    const statusEl = document.getElementById("swapViaStatus");

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

document.getElementById("swapBtn").addEventListener("click", swapInPool);
document.getElementById("swapViaBtn").addEventListener("click", swapViaCCT);
document.getElementById("swapPoolSymbol").addEventListener("change", (e) => checkPoolTradeable(e.target.value));
document.getElementById("swapFrom").addEventListener("change", (e) => loadSwapFromBalance(e.target.value));

swapInit();
setInterval(loadSwapSymbols, 15000);
