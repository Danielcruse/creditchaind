const wallet = requireWallet();

async function refresh() {
    if (!wallet) return;

    try {
        const [rep, w, history, rept] = await Promise.all([
            apiGet(`/reputation?address=${wallet.address}`),
            apiGet(`/wallet?address=${wallet.address}`),
            apiGet(`/credit/history?address=${wallet.address}`),
            apiGet(`/rept/balance?address=${wallet.address}`),
        ]);

        document.getElementById("score").innerText = rep.score;
        document.getElementById("tier").innerText = rep.creditTier;
        document.getElementById("votingPower").innerText = w.votingPower;
        document.getElementById("reptBalance").innerText = rept.balance;

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
