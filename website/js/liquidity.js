const liqWallet = getWallet();

// nativeTokenSymbols are always poolable, even before any developer
// registers a custom token - see api/token.go's
// cctEthTestPool/cctUsdcTestPool.
const nativeTokenSymbols = ["ETHTEST", "USDCTEST"];

// orderBookOnlySymbols price on the real bid/ask order book now
// (order-book.html), not this AMM - see the plan's "hide, don't
// delete" convention: the pool itself stays fully intact (still used
// internally for unstake redemption), only NEW deposits are blocked
// here. Existing LPs can still remove what they already put in.
const orderBookOnlySymbols = new Set(["ETHTEST", "USDCTEST"]);

function liqInit() {
    if (!liqWallet) {
        document.getElementById("liqLoggedOut").style.display = "block";
        document.getElementById("liqLoggedIn").style.display = "none";
        return;
    }
    document.getElementById("liqActionsCard").style.display = "block";
    loadPoolSymbols();
}

async function loadPoolSymbols() {
    try {
        const [tokens, pools] = await Promise.all([apiGet("/tokens"), apiGet("/pools")]);
        const poolSelect = document.getElementById("liqPoolSymbol");

        const customSymbols = (tokens || []).map((t) => t.symbol);
        const poolableSymbols = [...nativeTokenSymbols, ...customSymbols];

        const currentSelection = poolSelect.value;
        poolSelect.innerHTML = poolableSymbols.map((s) => `<option value="${s}">${s}</option>`).join("");
        if (poolableSymbols.includes(currentSelection)) {
            poolSelect.value = currentSelection;
        }

        if (poolSelect.value) {
            loadPoolInfo(poolSelect.value);
        }
    } catch (e) {
        console.error(e);
    }
}

async function loadPoolInfo(symbol) {
    if (!symbol) return;

    const addDisabled = orderBookOnlySymbols.has(symbol);
    document.getElementById("liqAddDisabledNote").style.display = addDisabled ? "block" : "none";
    document.getElementById("liqAddRow").style.display = addDisabled ? "none" : "flex";

    try {
        const info = await apiGet(
            `/pool?symbol=${encodeURIComponent(symbol)}&address=${liqWallet ? liqWallet.address : ""}`
        );

        const statusLine = info.tradeable
            ? `Tradeable - 1 ${symbol} = ${(info.cctReserve / info.tokenReserve).toFixed(4)} CCT`
            : "Not tradeable yet - add liquidity below to give it a price.";

        let balanceLine = "";
        if (liqWallet) {
            const [cctBalance, tokenBalance] = await Promise.all([
                fetchTokenBalance(liqWallet, "CCT"),
                fetchTokenBalance(liqWallet, symbol),
            ]);
            balanceLine = `<p>Your balance: ${cctBalance.balance} CCT / ${tokenBalance.balance} ${symbol}</p>`;
        }

        document.getElementById("liqPoolInfo").innerHTML = `
            <p>Reserves: ${info.cctReserve} CCT / ${info.tokenReserve} ${symbol}</p>
            <p style="color:${info.tradeable ? "#059669" : "#dc2626"};">${statusLine}</p>
            <p>Your LP shares: ${info.yourShares || 0} of ${info.totalShares} total</p>
            ${balanceLine}
        `;
    } catch (e) {
        console.error(e);
    }
}

async function addLiquidity() {
    const symbol = document.getElementById("liqPoolSymbol").value;
    const cctAmount = parseInt(document.getElementById("liqAddCCT").value, 10);
    const tokenAmount = parseInt(document.getElementById("liqAddToken").value, 10);
    const statusEl = document.getElementById("liqPoolStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (orderBookOnlySymbols.has(symbol)) {
        statusEl.innerText = `${symbol} trades on the Order Book now - see order-book.html.`;
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
    const symbol = document.getElementById("liqPoolSymbol").value;
    const shares = parseInt(document.getElementById("liqRemoveShares").value, 10);
    const statusEl = document.getElementById("liqPoolStatus");

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

document.getElementById("liqAddLiquidityBtn").addEventListener("click", addLiquidity);
document.getElementById("liqRemoveLiquidityBtn").addEventListener("click", removeLiquidity);
document.getElementById("liqPoolSymbol").addEventListener("change", (e) => loadPoolInfo(e.target.value));

liqInit();
setInterval(loadPoolSymbols, 15000);
