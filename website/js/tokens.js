const tkWallet = getWallet();

// Mirrors api/token_registry.go's TokenCreationFeeETHTest - only used
// here for an early, friendlier warning; the server-side check is the
// one that actually matters.
const TOKEN_CREATION_FEE_ETHTEST = 5;

let tkSelectedStandard = "C1";

async function tkLoadStandards() {
    const picker = document.getElementById("tkStandardPicker");
    if (!picker) return;
    try {
        const standards = await apiGet("/token-standards");
        picker.innerHTML = (standards || [])
            .map(
                (s, i) => `
            <label style="display:inline-block;margin-right:16px;cursor:pointer;">
                <input type="radio" name="tkStandard" value="${s.Code}" ${i === 0 ? "checked" : ""}>
                <strong>${s.Code}</strong> - ${s.Name}
            </label>
        `
            )
            .join("");
        if (standards && standards.length > 0) tkSelectedStandard = standards[0].Code;
        picker.querySelectorAll('input[name="tkStandard"]').forEach((el) => {
            el.addEventListener("change", () => {
                tkSelectedStandard = el.value;
                document.getElementById("tkPegReference").style.display = tkSelectedStandard === "C117S" ? "inline-block" : "none";
            });
        });
    } catch (e) {
        console.error(e);
    }
}

async function tkInit() {
    tkLoadStandards();
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
                <span>${t.symbol} <span style="opacity:0.6;font-size:11px;">[${t.standard || "C1"}]</span></span>
                <span class="explorer-hash">${t.name}${t.pegReference ? ` (pegged: ${t.pegReference})` : ""}</span>
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

    const pegReference = document.getElementById("tkPegReference").value.trim();

    try {
        const result = await apiPostSigned("/tokens/register", {
            symbol,
            name,
            initialSupply: supply,
            standard: tkSelectedStandard,
            pegReference: tkSelectedStandard === "C117S" ? pegReference : "",
        });
        statusEl.innerText = `Registered as ${tkSelectedStandard} - pending confirmation (tx ${result.txId}), ${TOKEN_CREATION_FEE_ETHTEST} ETHTEST fee charged. Head to the Pool page to give it a price.`;
        setTimeout(loadTokenList, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function tkLoadYene() {
    if (!tkWallet) return;
    try {
        const bal = await fetchTokenBalance(tkWallet, "YENE");
        document.getElementById("tkYeneBalance").innerText = bal.balance;
    } catch (e) {
        console.error(e);
    }

    try {
        const status = await apiGet(`/yene/claim-status?address=${tkWallet.address}`);
        const btn = document.getElementById("tkYeneClaimBtn");
        if (!status.canClaim) {
            btn.disabled = true;
            const next = new Date(status.nextClaimAt * 1000);
            document.getElementById("tkYeneStatus").innerText = `Next claim available ${next.toLocaleString()}.`;
        } else {
            btn.disabled = false;
            document.getElementById("tkYeneStatus").innerText = "";
        }
    } catch (e) {
        console.error(e);
    }
}

async function claimYene() {
    const statusEl = document.getElementById("tkYeneStatus");
    try {
        const result = await apiPostSigned("/yene/claim", {});
        statusEl.innerText = `Claimed - pending confirmation (tx ${result.txId}).`;
        setTimeout(tkLoadYene, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

document.getElementById("tkRegisterBtn").addEventListener("click", registerToken);
document.getElementById("tkYeneClaimBtn").addEventListener("click", claimYene);

tkInit();
tkLoadYene();

setInterval(loadTokenList, 15000);
setInterval(tkLoadYene, 15000);
