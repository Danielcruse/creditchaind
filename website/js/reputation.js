const wallet = requireWallet();

async function refresh() {
    if (!wallet) return;

    try {
        const [rep, w, history, rept, miningPower] = await Promise.all([
            apiGet(`/reputation?address=${wallet.address}`),
            apiGet(`/wallet?address=${wallet.address}`),
            apiGet(`/credit/history?address=${wallet.address}`),
            apiGet(`/rept/balance?address=${wallet.address}`),
            apiGet(`/mining-power?address=${wallet.address}`),
        ]);

        document.getElementById("score").innerText = rep.score;
        document.getElementById("tier").innerText = rep.creditTier;
        document.getElementById("votingPower").innerText = w.votingPower;
        document.getElementById("reptBalance").innerText = rept.balance;

        document.getElementById("miningPowerScore").innerText = miningPower.score;
        document.getElementById("miningPowerFactors").innerHTML = `
            <div class="explorer-row"><span>Trust Score (30%)</span><span>${miningPower.trustScoreFactor}</span></div>
            <div class="explorer-row"><span>Repayment History (20%)</span><span>${miningPower.repaymentFactor}</span></div>
            <div class="explorer-row"><span>Staking Activity (15%)</span><span>${miningPower.stakingFactor}</span></div>
            <div class="explorer-row"><span>Governance Participation (10%)</span><span>${miningPower.governanceFactor}</span></div>
            <div class="explorer-row"><span>Validator Participation (10%)</span><span>${miningPower.validatorFactor}</span></div>
            <div class="explorer-row"><span>Successful Lending (10%)</span><span>${miningPower.successfulLendingFactor}</span></div>
            <div class="explorer-row"><span>Account Age (5%)</span><span>${miningPower.accountAgeFactor}</span></div>
            <div class="explorer-row" style="opacity:0.6;"><span>Network Contributions</span><span>${miningPower.networkContributionsStatus}</span></div>
            <div class="explorer-row"><span>RC Infusion Boost</span><span>+${miningPower.rcInfusionBoost}</span></div>
        `;

        const nextTierEl = document.getElementById("reptNextTier");
        if (rep.nextTier) {
            nextTierEl.innerText = `Next: reach ${rep.nextTier} to mint ${rep.nextTierReward} REPT.`;
        } else {
            nextTierEl.innerText = "You've reached every tier - no further REPT progression rewards.";
        }

        const div = document.getElementById("history");
        div.innerHTML = "";

        if (!history || history.length === 0) {
            div.innerHTML = "No history yet.";
            return;
        }

        history
            .slice()
            .reverse()
            .forEach((event) => {
                const p = document.createElement("p");
                p.innerText = `${event.Type}: ${event.Amount}`;
                div.appendChild(p);
            });
    } catch (e) {
        console.error(e);
    }
}

async function loadRcLeaderboard() {
    const el = document.getElementById("rcLeaderboardList");
    try {
        const rows = await apiGet("/rc/leaderboard");
        if (!rows || rows.length === 0) {
            el.innerHTML = "No RC holders yet.";
            return;
        }
        el.innerHTML = rows
            .map((r, i) => {
                const badge = r.isExecutive ? ' <span style="opacity:0.8;">[Executive]</span>' : "";
                const mine = wallet && r.address === wallet.address ? " (you)" : "";
                return `<div class="explorer-row"><span>#${i + 1} ${r.address}${mine}${badge}</span><span>${r.rcBalance} RC</span></div>`;
            })
            .join("");
    } catch (e) {
        console.error(e);
        el.innerHTML = "Failed to load leaderboard.";
    }
}

async function loadRecentInfusions() {
    const el = document.getElementById("recentInfusionsList");
    try {
        const rows = await apiGet("/rc/infusions/recent");
        if (!rows || rows.length === 0) {
            el.innerHTML = "No infusions yet.";
            return;
        }
        el.innerHTML = rows
            .slice()
            .reverse()
            .map((r) => `<div class="explorer-row"><span>${r.Address}: "${r.TrustReason || "(no reason given)"}"</span><span>${r.RCAmount} RC</span></div>`)
            .join("");
    } catch (e) {
        console.error(e);
        el.innerHTML = "Failed to load recent infusions.";
    }
}

async function refreshInfuseRcBalance() {
    if (!wallet) return;
    try {
        const rept = await apiGet(`/rept/balance?address=${wallet.address}`);
        document.getElementById("infuseRcBalance").innerText = rept.balance;
    } catch (e) {
        console.error(e);
    }
}

async function infuseRc() {
    if (!wallet) return;
    const statusEl = document.getElementById("infuseRcStatus");
    const amount = parseInt(document.getElementById("infuseRcAmount").value, 10);
    const reason = document.getElementById("infuseRcReason").value.trim();

    if (!amount || amount <= 0) {
        statusEl.innerText = "Enter a positive RC amount.";
        return;
    }

    statusEl.innerText = "Submitting...";
    try {
        const res = await apiPostSigned("/rc/infuse", { rcAmount: amount, trustReason: reason });
        statusEl.innerText = `Pending - tx ${res.txId}. Takes effect next block.`;
        document.getElementById("infuseRcAmount").value = "";
        document.getElementById("infuseRcReason").value = "";
        setTimeout(() => {
            refreshInfuseRcBalance();
            loadRecentInfusions();
        }, 9000);
    } catch (e) {
        statusEl.innerText = `Failed: ${e.message || e}`;
    }
}

refresh();
loadRcLeaderboard();
loadRecentInfusions();
refreshInfuseRcBalance();

setInterval(refresh, 10000);
setInterval(loadRcLeaderboard, 15000);
setInterval(loadRecentInfusions, 15000);
