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
        document.getElementById("plSwapFromBalance").innerText = `Your balance: ${balance.balance} ${symbol}`;
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

        const statusLine = info.tradeable
            ? `Tradeable - 1 ${symbol} = ${(info.cctReserve / info.tokenReserve).toFixed(4)} CCT`
            : "Not tradeable yet - add liquidity below to give it a price.";

        let balanceLine = "";
        if (plWallet) {
            const [cctBalance, tokenBalance] = await Promise.all([
                fetchTokenBalance(plWallet, "CCT"),
                fetchTokenBalance(plWallet, symbol),
            ]);
            balanceLine = `<p>Your balance: ${cctBalance.balance} CCT / ${tokenBalance.balance} ${symbol}</p>`;
        }

        document.getElementById("plPoolInfo").innerHTML = `
            <p>Reserves: ${info.cctReserve} CCT / ${info.tokenReserve} ${symbol}</p>
            <p style="color:${info.tradeable ? "#8f8" : "#f88"};">${statusLine}</p>
            <p>Your LP shares: ${info.yourShares || 0} of ${info.totalShares} total</p>
            ${balanceLine}
        `;
    } catch (e) {
        console.error(e);
    }
}

async function addLiquidity() {
    const symbol = document.getElementById("plPoolSymbol").value;
    const cctAmount = parseInt(document.getElementById("plAddCCT").value, 10);
    const tokenAmount = parseInt(document.getElementById("plAddToken").value, 10);
    const statusEl = document.getElementById("plPoolStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (!cctAmount || !tokenAmount) {
        statusEl.innerText = "Enter both a CCT amount and a token amount.";
        return;
    }

    try {
        const result = await apiPostSigned("/pool/add-liquidity", {
            symbol,
            cctAmount,
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
    const shares = parseInt(document.getElementById("plRemoveShares").value, 10);
    const statusEl = document.getElementById("plPoolStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (!shares) {
        statusEl.innerText = "Enter shares to remove.";
        return;
    }

    try {
        const result = await apiPostSigned("/pool/remove-liquidity", { symbol, shares });
        statusEl.innerText = `Removal pending confirmation (tx ${result.txId}).`;
        setTimeout(() => loadPoolInfo(symbol), 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function swapInPool() {
    const symbol = document.getElementById("plPoolSymbol").value;
    const direction = document.getElementById("plSwapDirection").value;
    const amountIn = parseInt(document.getElementById("plSwapAmount").value, 10);
    const statusEl = document.getElementById("plPoolStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (!amountIn) {
        statusEl.innerText = "Enter an amount to swap.";
        return;
    }
    if (!plCurrentPoolTradeable) {
        statusEl.innerText = `${symbol} isn't tradeable yet - add liquidity first.`;
        return;
    }

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
    const amountIn = parseInt(document.getElementById("plSwapViaAmount").value, 10);
    const statusEl = document.getElementById("plSwapViaStatus");

    if (fromSymbol === toSymbol) {
        statusEl.innerText = "Pick two different tokens.";
        return;
    }
    if (!amountIn) {
        statusEl.innerText = "Enter an amount to swap.";
        return;
    }

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
