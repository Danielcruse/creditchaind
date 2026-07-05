const wallet = requireWallet();

if (wallet) {
    document.getElementById("wallet").innerText = wallet.address;

    const username = localStorage.getItem("username");
    if (username) {
        document.getElementById("username").innerText = username;
    }
}

async function loadNetwork() {
    try {
        const data = await apiGet("/network");

        document.getElementById("height").innerText = data.blockHeight;
        document.getElementById("hash").innerText = data.latestBlockHash;
        document.getElementById("peers").innerText = data.peerCount;
        document.getElementById("validators").innerText = data.activeValidators;
        document.getElementById("tps").innerText = data.tps;
        document.getElementById("gas").innerText = data.gasPrice;
        document.getElementById("supply").innerText = data.totalSupply;
        document.getElementById("circulating").innerText = data.circulatingSupply;
        document.querySelector(".network-live").innerText = `● ${data.status}`;
    } catch (e) {
        console.error(e);
    }
}

// loadWallet updates each field independently (not one big all-or-
// nothing try/catch) - a single failed request (a slow/dropped
// derived-address computation, one endpoint timing out) used to leave
// every field, including ones that succeeded, stuck at the "TODO"
// placeholder forever. Each piece now fails on its own without
// blocking the others.
async function loadWallet() {
    if (!wallet) return;

    try {
        const w = await apiGet(`/wallet?address=${wallet.address}`);
        document.getElementById("nodeId").innerText = w.nodeId || "Not Connected";
        document.getElementById("voting").innerText = w.votingPower;
    } catch (e) {
        console.error(e);
    }

    try {
        const tokenBalance = await apiGet(`/token/balance?address=${wallet.address}`);
        document.getElementById("balance").innerText = `${tokenBalance.balance} CCT`;
    } catch (e) {
        console.error(e);
    }

    try {
        const ethTestAddress = await getEthTestAddress(wallet);
        document.getElementById("ethTestAddress").innerText = ethTestAddress;
        const ethTestBalance = await apiGet(`/ethtest/balance?address=${ethTestAddress}`);
        document.getElementById("ethTestBalance").innerText = `${ethTestBalance.balance} ETHTEST`;
    } catch (e) {
        console.error(e);
    }

    try {
        const usdcTestAddress = await getUsdcTestAddress(wallet);
        document.getElementById("usdcTestAddress").innerText = usdcTestAddress;
        const usdcTestBalance = await apiGet(`/usdctest/balance?address=${usdcTestAddress}`);
        document.getElementById("usdcTestBalance").innerText = `${usdcTestBalance.balance} USDCTEST`;
    } catch (e) {
        console.error(e);
    }
}

async function loadReputation() {
    if (!wallet) return;

    try {
        const data = await apiGet(`/reputation?address=${wallet.address}`);

        document.getElementById("reputation").innerText = data.score;
        document.getElementById("credit").innerText = data.score;
        document.getElementById("level").innerText = data.creditTier;
    } catch (e) {
        console.error(e);
    }
}

async function loadValidatorStatus() {
    if (!wallet) return;

    try {
        const validators = await apiGet("/validators");
        const mine = validators.find((v) => v.address === wallet.address);

        document.getElementById("validator").innerText = mine && mine.active ? "Active" : "Inactive";
    } catch (e) {
        console.error(e);
    }
}

// Dashboard preview only - last 5 blocks/transactions, never the
// whole chain. Anything beyond that belongs on the real explorer
// page (explorer.html), which has proper pagination and search.
async function loadExplorer() {
    try {
        const data = await apiGet("/explorer?blocksLimit=5&txLimit=5");

        const blocks = data.blocks || [];
        document.getElementById("blocks").innerText =
            blocks.length === 0
                ? "No blocks yet."
                : blocks
                      .map((b) => `#${b.Index}  ${b.Hash}  (${(b.Transactions || []).length} tx)`)
                      .join("\n");

        const pendingTxs = (data.pendingTransactions || []).slice(0, 5);
        const confirmedTxs = data.transactions || [];
        const txLines = [
            ...pendingTxs.map((tx) => `[pending] ${tx.From || "-"} -> ${tx.To || "-"}: ${tx.Amount} (${tx.Type})`),
            ...confirmedTxs.map((tx) => `[confirmed] ${tx.From || "-"} -> ${tx.To || "-"}: ${tx.Amount} (${tx.Type})`),
        ];
        document.getElementById("transactions").innerText =
            txLines.length === 0 ? "No transactions." : txLines.join("\n");
    } catch (e) {
        console.error(e);
    }
}

async function loadMempool() {
    try {
        const data = await apiGet("/mempool");

        document.getElementById("activity").innerText = `${(data || []).length} pending transactions`;
    } catch (e) {
        console.error(e);
    }
}

async function loadNotifications() {
    if (!wallet) return;

    try {
        const history = await apiGet(`/credit/history?address=${wallet.address}`);
        const div = document.getElementById("notifications");

        div.innerHTML = "";

        if (!history || history.length === 0) {
            div.innerHTML = "No notifications.";
            return;
        }

        history
            .slice()
            .reverse()
            .slice(0, 5)
            .forEach((event) => {
                div.innerHTML += `<p>${event.Type}: ${event.Amount} CCT</p>`;
            });
    } catch (e) {
        console.error(e);
    }
}

function showReceiveAddress() {
    if (!wallet) return;

    if (navigator.clipboard) {
        navigator.clipboard.writeText(wallet.address).catch(() => {});
    }

    document.getElementById("qrAddressText").innerText = wallet.address;
    QR.renderToCanvas(document.getElementById("qrCanvas"), wallet.address, 6);
    document.getElementById("qrModalOverlay").style.display = "flex";
}

function closeQrModal() {
    document.getElementById("qrModalOverlay").style.display = "none";
}

async function claimFaucet() {
    if (!wallet) return;

    try {
        const result = await apiPostSigned("/faucet", {});
        document.getElementById("faucetStatus").innerText =
            `Faucet claim pending confirmation (tx ${result.txId}) - ${result.amount} CCT, ${result.ethTestAmount} ETHTEST, and ${result.usdcTestAmount} USDCTEST will land shortly (ETHTEST/USDCTEST go to your imported-token address).`;
        setTimeout(loadWallet, 9000);
    } catch (e) {
        document.getElementById("faucetStatus").innerText = e.message;
    }
}

async function refresh() {
    await loadNetwork();
    await loadWallet();
    await loadReputation();
    await loadValidatorStatus();
    await loadExplorer();
    await loadMempool();
    await loadNotifications();
}

refresh();

setInterval(refresh, 5000);
