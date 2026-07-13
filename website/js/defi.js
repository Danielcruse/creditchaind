// DeFi corner: Bitcoin is a completely separate chain from
// CreditChain - this file never touches a CCT/REPT/ETHTEST balance,
// never asks for a private key, and never signs anything. It's a
// read-only viewer against a public block explorer API (Blockstream),
// the same public chain data anyone could look up directly - self-
// custody means the address's owner (not CreditChain) is the only one
// who can ever move what's shown here.
const BTC_API_BASE = "https://blockstream.info/api";

function satsToBtc(sats) {
    return (sats / 1e8).toFixed(8);
}

function btcTxRow(tx, address) {
    // A tx's net effect on THIS address specifically - summing its own
    // vout/vin entries, not the whole transaction's total value.
    let received = 0;
    for (const vout of tx.vout || []) {
        if (vout.scriptpubkey_address === address) received += vout.value;
    }
    let sent = 0;
    for (const vin of tx.vin || []) {
        if (vin.prevout && vin.prevout.scriptpubkey_address === address) sent += vin.prevout.value;
    }
    const net = received - sent;
    const sign = net >= 0 ? "+" : "";
    const color = net >= 0 ? "#00ff95" : "#ff6b6b";
    const confirmed = tx.status && tx.status.confirmed;

    return `
        <div class="explorer-row" title="${tx.txid}">
            <span>[${confirmed ? "confirmed" : "pending"}]</span>
            <span class="explorer-hash">${tx.txid.slice(0, 8)}...${tx.txid.slice(-6)}</span>
            <span style="color:${color};font-weight:600;">${sign}${satsToBtc(net)} BTC</span>
        </div>
    `;
}

async function lookupBtcAddress() {
    const address = document.getElementById("btcAddressInput").value.trim();
    const resultDiv = document.getElementById("btcResult");
    if (!address) return;

    resultDiv.innerHTML = `<p class="explorer-empty">Loading...</p>`;

    try {
        const [infoRes, txsRes] = await Promise.all([
            fetch(`${BTC_API_BASE}/address/${encodeURIComponent(address)}`),
            fetch(`${BTC_API_BASE}/address/${encodeURIComponent(address)}/txs`),
        ]);

        if (!infoRes.ok) {
            resultDiv.innerHTML = `<p class="explorer-empty">Address not found or invalid.</p>`;
            return;
        }

        const info = await infoRes.json();
        const txs = txsRes.ok ? await txsRes.json() : [];

        const chain = info.chain_stats || {};
        const mempool = info.mempool_stats || {};
        const confirmedBalance = (chain.funded_txo_sum || 0) - (chain.spent_txo_sum || 0);
        const pendingBalance = (mempool.funded_txo_sum || 0) - (mempool.spent_txo_sum || 0);

        const txRows = txs.length
            ? txs.slice(0, 10).map((tx) => btcTxRow(tx, address)).join("")
            : `<div class="explorer-empty">No transactions yet.</div>`;

        resultDiv.innerHTML = `
            <div class="card" style="margin:12px 0 0 0;">
                <h3 style="word-break:break-all;margin-top:0;">${info.address}</h3>
                <div class="explorer-row">
                    <span>Confirmed balance</span>
                    <span>${satsToBtc(confirmedBalance)} BTC</span>
                </div>
                ${pendingBalance !== 0 ? `
                <div class="explorer-row">
                    <span>Pending (mempool)</span>
                    <span>${satsToBtc(pendingBalance)} BTC</span>
                </div>` : ""}
                <div class="explorer-row">
                    <span>Total transactions</span>
                    <span>${chain.tx_count || 0}</span>
                </div>
            </div>
            <h3>Recent Activity</h3>
            ${txRows}
        `;
    } catch (e) {
        resultDiv.innerHTML = `<p class="explorer-empty">Lookup failed: ${e.message}</p>`;
        console.error(e);
    }
}

document.getElementById("btcLookupBtn").addEventListener("click", lookupBtcAddress);
document.getElementById("btcAddressInput").addEventListener("keydown", (e) => {
    if (e.key === "Enter") lookupBtcAddress();
});

// --- ETH custodial corner (Sepolia testnet) -------------------------------
// Unlike the BTC viewer above, CreditChain holds this key on the user's
// behalf - see core/eth_custodial.go's doc comment and audit/KNOWN_GAPS.md
// for exactly what that means and doesn't mean here. Every function below
// talks to /eth-custodial/* (api/eth_custodial.go), never anything on this
// page constructs or signs a real transaction itself.

const ethWallet = getWallet();

async function initEthCustodial() {
    if (!ethWallet) return;

    try {
        const resp = await fetch(`${API_BASE}/eth-custodial/balance?address=${ethWallet.address}`);
        if (resp.status === 404) {
            // No custodial account yet - leave the "create" view showing.
            return;
        }
        if (!resp.ok) {
            // ETH custodial corner not enabled on this server (no RPC
            // configured) - nothing more to do; leave the create button
            // visible, it'll surface a clear error if clicked.
            return;
        }
        const data = await resp.json();
        showEthCustodialAccount(data.ethAddress, data.weiBalance);
    } catch (e) {
        console.error(e);
    }
}

function weiToEth(weiString) {
    const wei = BigInt(weiString);
    const whole = wei / 1000000000000000000n;
    const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

function showEthCustodialAccount(ethAddress, weiBalance) {
    document.getElementById("ethCustodialNoAccount").style.display = "none";
    document.getElementById("ethCustodialAccount").style.display = "block";
    document.getElementById("ethCustodialAddress").innerText = ethAddress;
    if (weiBalance !== undefined) {
        document.getElementById("ethCustodialBalance").innerText = `${weiToEth(weiBalance)} ETH (Sepolia)`;
    }
}

async function createEthCustodialAccount() {
    const statusEl = document.getElementById("ethCustodialCreateStatus");
    try {
        const result = await apiPostSigned("/eth-custodial/create", {});
        statusEl.innerText = "";
        showEthCustodialAccount(result.ethAddress);
        loadEthCustodialBalance();
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function loadEthCustodialBalance() {
    if (!ethWallet) return;
    const balanceEl = document.getElementById("ethCustodialBalance");
    balanceEl.innerText = "Loading...";
    try {
        const resp = await fetch(`${API_BASE}/eth-custodial/balance?address=${ethWallet.address}`);
        if (!resp.ok) {
            balanceEl.innerText = await resp.text();
            return;
        }
        const data = await resp.json();
        balanceEl.innerText = `${weiToEth(data.weiBalance)} ETH (Sepolia)`;
    } catch (e) {
        balanceEl.innerText = "Failed to load balance.";
        console.error(e);
    }
}

async function withdrawEthCustodial() {
    const to = document.getElementById("ethWithdrawTo").value.trim();
    const amountEth = document.getElementById("ethWithdrawAmount").value.trim();
    const statusEl = document.getElementById("ethCustodialWithdrawStatus");

    if (!to || !amountEth) {
        statusEl.innerText = "Enter a destination address and amount.";
        return;
    }

    let amountWei;
    try {
        const [whole, frac = ""] = amountEth.split(".");
        const fracPadded = (frac + "0".repeat(18)).slice(0, 18);
        amountWei = (BigInt(whole || "0") * 1000000000000000000n + BigInt(fracPadded || "0")).toString();
    } catch (e) {
        statusEl.innerText = "Invalid amount.";
        return;
    }

    if (!confirm(`Withdraw ${amountEth} Sepolia ETH to ${to}? This broadcasts a real (testnet) transaction and cannot be undone.`)) {
        return;
    }

    statusEl.innerText = "Broadcasting...";
    try {
        const result = await apiPostSigned("/eth-custodial/withdraw", { to, amountWei });
        statusEl.innerText = `Sent - tx ${result.txHash}`;
        setTimeout(loadEthCustodialBalance, 5000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

initEthCustodial();
