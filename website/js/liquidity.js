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

        // cctReserve/totalShares/yourShares are 18-decimal smallest-unit
        // decimal strings now (see api/token_registry.go's poolInfo).
        const cctReserveDisplay = cctFromSmallest(info.cctReserve);
        const statusLine = info.tradeable
            ? `Tradeable - 1 ${symbol} = ${(parseFloat(cctReserveDisplay) / info.tokenReserve).toFixed(4)} CCT`
            : "Not tradeable yet - add liquidity below to give it a price.";

        let balanceLine = "";
        if (liqWallet) {
            const [cctBalance, tokenBalance] = await Promise.all([
                fetchTokenBalance(liqWallet, "CCT"),
                fetchTokenBalance(liqWallet, symbol),
            ]);
            balanceLine = `<p>Your balance: ${cctFromSmallest(cctBalance.balance)} CCT / ${tokenBalance.balance} ${symbol}</p>`;
        }

        document.getElementById("liqPoolInfo").innerHTML = `
            <p>Reserves: ${cctReserveDisplay} CCT / ${info.tokenReserve} ${symbol}</p>
            <p style="color:${info.tradeable ? "#16A34A" : "#dc2626"};">${statusLine}</p>
            <p>Your LP shares: ${info.yourShares ? cctFromSmallest(info.yourShares) : 0} of ${cctFromSmallest(info.totalShares)} total</p>
            ${balanceLine}
        `;
    } catch (e) {
        console.error(e);
    }
}

async function addLiquidity() {
    const symbol = document.getElementById("liqPoolSymbol").value;
    const cctAmountStr = document.getElementById("liqAddCCT").value.trim();
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
    const symbol = document.getElementById("liqPoolSymbol").value;
    const sharesStr = document.getElementById("liqRemoveShares").value.trim();
    const statusEl = document.getElementById("liqPoolStatus");

    if (!symbol) {
        statusEl.innerText = "No token selected.";
        return;
    }
    if (!sharesStr) {
        statusEl.innerText = "Enter shares to remove.";
        return;
    }

    try {
        // Shares use the same 18-decimal smallest-unit decimal string
        // encoding as CCT (see core/liquidity_pool.go) - the user types
        // the same human amount shown by loadPoolInfo's "Your LP shares"
        // line, so it round-trips through cctToSmallest the same way.
        const result = await apiPostSigned("/pool/remove-liquidity", { symbol, shares: cctToSmallest(sharesStr) });
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
