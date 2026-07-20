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

refresh();

setInterval(refresh, 10000);
