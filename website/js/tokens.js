const tkWallet = getWallet();

// Mirrors api/token_registry.go's TokenCreationFeeETHTest - only used
// here for an early, friendlier warning; the server-side check is the
// one that actually matters.
const TOKEN_CREATION_FEE_ETHTEST = 5;

async function tkInit() {
    if (!tkWallet) {
        document.getElementById("tokensLoggedOut").style.display = "block";
        document.getElementById("tokensLoggedIn").style.display = "none";
        loadTokenList();
        return;
    }

    try {
        const staking = await apiGet(`/staking?address=${tkWallet.address}`);
        if (!staking.stakedETH || staking.stakedETH === 0) {
            document.getElementById("tokensNotStaked").style.display = "block";
            document.getElementById("tokensLoggedIn").style.display = "none";
            loadTokenList();
            return;
        }
    } catch (e) {
        console.error(e);
    }

    try {
        const ethTestBalance = await fetchTokenBalance(tkWallet, "ETHTEST");
        if (ethTestBalance.balance < TOKEN_CREATION_FEE_ETHTEST) {
            document.getElementById("tokensNotEnoughFee").style.display = "block";
            document.getElementById("tokensLoggedIn").style.display = "none";
        }
    } catch (e) {
        console.error(e);
    }

    loadTokenList();
}

async function loadTokenList() {
    try {
        const tokens = await apiGet("/tokens");
        const listDiv = document.getElementById("tkTokenList");

        if (!tokens || tokens.length === 0) {
            listDiv.innerHTML = `<div class="explorer-empty">No tokens registered yet.</div>`;
            return;
        }

        listDiv.innerHTML = tokens
            .map(
                (t) => `
            <div class="explorer-row">
                <span>${t.symbol}</span>
                <span class="explorer-hash">${t.name}</span>
                <span>supply: ${t.totalSupply}</span>
                <span>pool: ${t.cctReserve} CCT / ${t.tokenReserve} ${t.symbol}</span>
                <span style="color:${t.tradeable ? "#8f8" : "#f88"};">
                    ${t.tradeable ? "Tradeable" : "Not tradeable yet - needs liquidity"}
                </span>
            </div>
        `
            )
            .join("");
    } catch (e) {
        console.error(e);
    }
}

async function registerToken() {
    const symbol = document.getElementById("tkSymbol").value.trim().toUpperCase();
    const name = document.getElementById("tkName").value.trim();
    const supply = parseInt(document.getElementById("tkSupply").value, 10);
    const statusEl = document.getElementById("tkRegisterStatus");

    if (!symbol || !name || !supply) {
        statusEl.innerText = "Fill in symbol, name, and initial supply.";
        return;
    }

    try {
        const result = await apiPostSigned("/tokens/register", {
            symbol,
            name,
            initialSupply: supply,
        });
        statusEl.innerText = `Registered - pending confirmation (tx ${result.txId}), ${TOKEN_CREATION_FEE_ETHTEST} ETHTEST fee charged. Head to the Pool page to give it a price.`;
        setTimeout(loadTokenList, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

document.getElementById("tkRegisterBtn").addEventListener("click", registerToken);

tkInit();

setInterval(loadTokenList, 15000);
