const wallet = requireWallet();

async function loadIdentity() {
    if (!wallet) return;

    document.getElementById("address").innerText = wallet.address;

    try {
        const [w, reputation] = await Promise.all([
            apiGet(`/wallet?address=${wallet.address}`),
            apiGet(`/reputation?address=${wallet.address}`),
        ]);

        document.getElementById("rep").innerText = `${reputation.score} (${reputation.creditTier})`;
        document.getElementById("credit").innerText = reputation.score;
        document.getElementById("vote").innerText = w.votingPower;
    } catch (e) {
        console.error(e);
    }
}

loadIdentity();
