const wallet = requireWallet();
let pendingStakeTx = null;

const SYMBOL_LABELS = { CCT: "CCT", ETHB: "ETH", ETH_CCC1: "ETH", ETHTEST: "ETHTEST", USDCTEST: "USDCTEST" };

// On mainnet, ETHB (the testnet-only mirror-model ETH ledger) is
// replaced with ETH_CCC1 (real vault-locked ETH - see api/stake.go's
// doc comment for why ETHB isn't the mainnet-appropriate stakeable ETH
// asset). Removed entirely from both dropdowns here, not just relabeled -
// same "mainnet should use only mainnet features" treatment send.js
// already applies to its own symbol list. Testnet is completely
// unaffected - this only runs in mainnet mode.
(function adjustStakeSymbolsForNetwork() {
    if (window.CC_NETWORK !== "mainnet") return;
    for (const selectId of ["stakeSymbol", "unstakeSymbol"]) {
        const select = document.getElementById(selectId);
        if (!select) continue;
        const ethbOpt = [...select.options].find((o) => o.value === "ETHB");
        if (!ethbOpt) continue;
        const ccc1Opt = document.createElement("option");
        ccc1Opt.value = "ETH_CCC1";
        ccc1Opt.innerText = "ETH";
        select.insertBefore(ccc1Opt, ethbOpt);
        ethbOpt.remove();
    }
})();

// ethCcc1Balance mirrors fetchTokenBalance's return shape ({balance})
// for ETH_CCC1 specifically - /vault/balance returns the raw wei-integer
// amount as a decimal string (see api/vault_info.go's HandleVaultBalance
// and api/token.go's ccc1DecimalWholeUnitAdapter, which mints/stores
// plain wei integers, never a human "1.0"-style value). Converted to a
// human decimal for display by stakeDisplayAmount below, not here -
// this stays a pure balance fetch.
async function ethCcc1Balance(w) {
    return fetchCcc1Balance(w, "ETH");
}

// ETH_CCC1 is the one stakeable symbol whose balance/staked amounts are
// real wei (18 decimals) rather than a small whole-integer count like
// CCT/ETHB - displaying it raw (as the wallet-balance and .staked
// fields naturally are, see fetchCcc1Balance/HandleStaking) showed
// something like "5000000000000000 ETH" instead of "0.005 ETH". Uses
// vault.js's smallestToDecimal (already loaded - see staking.html).
function stakeDisplayAmount(symbol, smallestStr) {
    return symbol === "ETH_CCC1" ? smallestToDecimal(String(smallestStr), 18) : smallestStr;
}

// postStakeAmount sends /stake or /unstake's Amount field. CCT/ETHB
// stay whole small integers (well inside JS's safe-integer range) and
// go through the normal apiPostSigned(object) path unchanged. ETH_CCC1
// is real wei (18 decimals) - converting the typed decimal ETH amount
// to its wei integer via BigInt (vault.js's decimalToSmallest) and
// hand-building the JSON payload (apiPostSignedRaw, same pattern
// api.js's own doc comment describes for ETHB bridge withdrawals) so
// the exact digit string reaches the server, never round-tripping
// through a JS Number that would silently lose precision above 2^53
// (~0.009 ETH - i.e. almost any real stake amount).
async function postStakeAmount(path, symbol, amountStr, amount) {
    if (symbol !== "ETH_CCC1") {
        return apiPostSigned(path, { Amount: amount, Symbol: symbol });
    }
    const amountWei = decimalToSmallest(amountStr, 18);
    if (amountWei === "0") {
        throw new Error("amount is too small to represent in wei");
    }
    return apiPostSignedRaw(path, `{"Amount":${amountWei},"Symbol":"ETH_CCC1"}`);
}

async function refresh() {
    if (!wallet) return;

    try {
        const data = await apiGet(`/staking?address=${wallet.address}`);

        // The staking response only ever knew about staked amounts -
        // it has no way to see a wallet's actual holdable CCT/ETHTEST/
        // USDCTEST balance (that's a different endpoint entirely, and
        // for ETHTEST/USDCTEST needs this wallet's own public key to
        // derive the right sub-address, which only the client has).
        // Fetching these alongside is what lets you actually SEE an
        // unstaked amount land back in your real balance, instead of
        // only ever seeing "0 staked" and wondering where it went.
        const balances = {};
        for (const pos of data.positions || []) {
            try {
                // ETH_CCC1 doesn't resolve through the generic
                // /token/balance path fetchTokenBalance uses - see
                // ethCcc1Balance's doc comment.
                const bal = pos.symbol === "ETH_CCC1"
                    ? await ethCcc1Balance(wallet)
                    : await fetchTokenBalance(wallet, pos.symbol);
                // CCT's holdable balance is an 18-decimal smallest-unit
                // string now; staked/rewards positions stay whole-uint64
                // (staking is still whole-CCT at this boundary - see
                // core/staking_sync.go).
                balances[pos.symbol] = pos.symbol === "CCT" ? cctFromSmallest(bal.balance) : bal.balance;
            } catch (e) {
                balances[pos.symbol] = null;
            }
        }

        const legacyBalances = {};
        for (const pos of data.legacyPositions || []) {
            try {
                const bal = await fetchTokenBalance(wallet, pos.symbol);
                legacyBalances[pos.symbol] = bal.balance;
            } catch (e) {
                legacyBalances[pos.symbol] = null;
            }
        }

        renderPositions(data.positions, balances, data.oethBalance, data.delegatedOutCCT, data.availableStakedCCT);
        renderLegacyPositions(data.legacyPositions, legacyBalances);
        renderUnbonding(data.unbonding);

        // Once a poll shows the pending stake reflected, the block
        // that confirmed it has landed - clear the pending banner.
        if (pendingStakeTx) {
            document.getElementById("pendingStatus").innerText =
                "Confirmed - included in a block.";
            pendingStakeTx = null;
        }
    } catch (e) {
        console.error(e);
    }
}

function renderPositions(positions, balances, oethBalance, delegatedOutCCT, availableStakedCCT) {
    const el = document.getElementById("positions");
    if (!positions || positions.length === 0) {
        el.innerText = "No positions.";
        return;
    }
    el.innerHTML = "";
    for (const pos of positions) {
        const p = document.createElement("p");
        const label = SYMBOL_LABELS[pos.symbol] || pos.symbol;
        const bal = balances && balances[pos.symbol] !== null && balances[pos.symbol] !== undefined
            ? `${stakeDisplayAmount(pos.symbol, balances[pos.symbol])} ${label} in wallet, `
            : "";
        p.innerText = `${label}: ${bal}${stakeDisplayAmount(pos.symbol, pos.staked)} staked`;
        el.appendChild(p);

        // Delegating to a validator draws from a separate pool than
        // staking itself (see core.DelegationStore's own doc comment) -
        // shown only for CCT (the only delegatable symbol today) and
        // only once something's actually delegated, so an ordinary
        // staker who's never delegated sees no change here at all.
        if (pos.symbol === "CCT" && delegatedOutCCT) {
            const d = document.createElement("p");
            d.style.cssText = "margin-left:16px;opacity:0.75;font-size:13px;";
            d.innerText =
                `↳ ${delegatedOutCCT.toLocaleString()} CCT delegated to a validator, ` +
                `${availableStakedCCT.toLocaleString()} CCT still available`;
            el.appendChild(d);
        }
    }
    if (oethBalance !== undefined) {
        const oeth = document.createElement("p");
        oeth.style.opacity = "0.75";
        oeth.innerText = `oETH (staked-ETH receipt): ${oethBalance}`;
        el.appendChild(oeth);
    }
}

// renderLegacyPositions shows any still-outstanding ETHTEST/USDCTEST
// stake the one-time migration sweep hasn't (yet) redeemed to YEN-e -
// see api/legacy_stake_migration.go. Most wallets will never see this
// (either they never staked those symbols, or the sweep already
// closed the position out) - the section only renders when there's
// something real to show, rather than always showing an empty "0
// legacy stake" line.
function renderLegacyPositions(positions, balances) {
    const el = document.getElementById("legacyPositions");
    if (!el) return;

    if (!positions || positions.length === 0) {
        el.style.display = "none";
        return;
    }

    el.style.display = "";
    el.innerHTML = "<p style=\"opacity:0.75;font-size:13px;\">Legacy stake from before the ETH/USDC migration - " +
        "this will be automatically converted to YEN-e (or is currently locked as an open loan's collateral " +
        "and will convert once that loan resolves). You can also unstake it manually below if you'd rather " +
        "get the real token back instead of waiting.</p>";
    for (const pos of positions) {
        const p = document.createElement("p");
        const label = SYMBOL_LABELS[pos.symbol] || pos.symbol;
        const bal = balances && balances[pos.symbol] !== null && balances[pos.symbol] !== undefined
            ? `${balances[pos.symbol]} ${label} in wallet, `
            : "";
        p.innerText = `${label}: ${bal}${pos.staked} staked (legacy)`;
        el.appendChild(p);
    }
}

async function stakeToken() {
    if (!wallet) return;

    const symbol = document.getElementById("stakeSymbol").value;
    const amountStr = document.getElementById("stakeAmount").value.trim();
    const amount = parseFloat(amountStr);

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await postStakeAmount("/stake", symbol, amountStr, amount);

        pendingStakeTx = result.txId;

        document.getElementById("pendingStatus").innerText =
            `Pending (tx ${result.txId}) - takes effect once the next block confirms it.`;
    } catch (e) {
        alert("Stake failed: " + e.message);
    }
}

async function unstakeToken() {
    if (!wallet) return;

    const symbol = document.getElementById("unstakeSymbol").value;
    const amountStr = document.getElementById("unstakeAmount").value.trim();
    const amount = parseFloat(amountStr);

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await postStakeAmount("/unstake", symbol, amountStr, amount);

        document.getElementById("unstakeStatus").innerText =
            `Unstake requested (tx ${result.txId}) - once confirmed, this ${symbol} stays bonded ` +
            `for a ${result.unbondingPeriodSecs}s cooldown before it's released back to your ${symbol} balance. ` +
            `See the Unbonding section below once the block lands.`;
    } catch (e) {
        alert("Unstake failed: " + e.message);
    }
}

function renderUnbonding(entries) {
    const el = document.getElementById("unbondingList");

    if (!entries || entries.length === 0) {
        el.innerText = "No pending unbonds.";
        return;
    }

    el.innerHTML = "";

    for (const entry of entries) {
        const p = document.createElement("p");
        const availableAt = new Date(entry.MaturesAt * 1000).toLocaleString();
        const symbol = entry.Symbol || "ETHTEST";
        p.innerText = `${entry.Amount} ${symbol} unbonding - available at ${availableAt}`;
        el.appendChild(p);
    }
}

refresh();

setInterval(refresh, 10000);
