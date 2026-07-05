const wallet = requireWallet();
let pendingStakeTx = null;

const SYMBOL_LABELS = { CCT: "CCT", ETHTEST: "ETHTEST", USDCTEST: "USDCTEST" };

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
                const bal = await fetchTokenBalance(wallet, pos.symbol);
                balances[pos.symbol] = bal.balance;
            } catch (e) {
                balances[pos.symbol] = null;
            }
        }

        renderPositions(data.positions, balances, data.oethBalance);
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

function renderPositions(positions, balances, oethBalance) {
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
            ? `${balances[pos.symbol]} ${label} in wallet, `
            : "";
        p.innerText = `${label}: ${bal}${pos.staked} staked (${pos.rewards} CCT reward rate)`;
        el.appendChild(p);
    }
    if (oethBalance !== undefined) {
        const oeth = document.createElement("p");
        oeth.style.opacity = "0.75";
        oeth.innerText = `oETH (staked-ETHTEST receipt): ${oethBalance}`;
        el.appendChild(oeth);
    }
}

async function stakeToken() {
    if (!wallet) return;

    const symbol = document.getElementById("stakeSymbol").value;
    const amount = parseFloat(document.getElementById("stakeAmount").value);

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/stake", {
            Amount: amount,
            Symbol: symbol,
        });

        pendingStakeTx = result.txId;

        document.getElementById("pendingStatus").innerText =
            `Pending (tx ${result.txId}) - takes effect once the next block confirms it, ~${result.estimatedReward} CCT estimated reward.`;
    } catch (e) {
        alert("Stake failed: " + e.message);
    }
}

async function unstakeToken() {
    if (!wallet) return;

    const symbol = document.getElementById("unstakeSymbol").value;
    const amount = parseFloat(document.getElementById("unstakeAmount").value);

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/unstake", {
            Amount: amount,
            Symbol: symbol,
        });

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

function claimRewards() {
    alert("Staking rewards are minted automatically as CCT each time you stake.");
}

refresh();

setInterval(refresh, 10000);
