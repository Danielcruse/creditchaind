const wallet = requireWallet();

function showReceiveAddress() {
    if (!wallet) return;

    if (navigator.clipboard) {
        navigator.clipboard.writeText(formatAddress(wallet.address)).catch(() => {});
    }

    alert(
        "Your address (copied to clipboard if supported):\n\n" +
        formatAddress(wallet.address) +
        "\n\nAnyone can send you test CCT by entering this address on the Send page."
    );
}

async function loadWallet() {
    if (!wallet) return;

    document.getElementById("walletAddress").innerText = formatAddress(wallet.address);

    try {
        const [w, tokenBalance, usdcTestBalance] = await Promise.all([
            apiGet(`/wallet?address=${wallet.address}`),
            apiGet(`/token/balance?address=${wallet.address}`),
            apiGet(`/usdctest/balance?address=${wallet.address}`),
        ]);

        document.getElementById("reputation").innerText = w.reputation;
        document.getElementById("votingPower").innerText = w.votingPower;
        document.getElementById("creditScore").innerText = w.reputation;
        document.getElementById("ethBalance").innerText = w.stakedETH;
        document.getElementById("oethBalance").innerText = w.oethBalance;
        document.getElementById("cctBalance").innerText = tokenBalance.balance;
        document.getElementById("usdcTestBalance").innerText = usdcTestBalance.balance;
        document.getElementById("rewardBalance").innerText = w.rewards;
    } catch (e) {
        console.error(e);
    }

    try {
        const history = await apiGet(`/credit/history?address=${wallet.address}`);
        const txDiv = document.getElementById("transactions");
        txDiv.innerHTML = "";

        if (!history || history.length === 0) {
            txDiv.innerHTML = "No Transactions";
            return;
        }

        history
            .slice()
            .reverse()
            .forEach((event) => {
                txDiv.innerHTML += `<p>${event.Type}: ${event.Amount} CCT</p>`;
            });
    } catch (e) {
        console.error(e);
    }
}

loadWallet();

setInterval(loadWallet, 10000);
