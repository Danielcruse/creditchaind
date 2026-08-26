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
    const color = net >= 0 ? "#0f9d58" : "#ff6b6b";
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

// --- Self-custodial HD accounts (ETH/BTC derived from the same
// recovery phrase as the CreditChain wallet - see website/js/hdwallet.js
// and the Multi-Chain HD Wallet plan's Assets UX). This section links
// out to the existing asset.html receive views rather than duplicating
// them, and gives the BTC lookup tool above a one-click way to look up
// the user's own address instead of only accepting a blind paste.
function initHDAccountsCard() {
    const accounts = getHdAccounts();

    if (!accounts || !accounts.eth || !accounts.btc) {
        document.getElementById("hdAccountsMissing").style.display = "block";
        return;
    }

    document.getElementById("hdAccountsPresent").style.display = "block";
    document.getElementById("hdEthLink").innerText = accounts.eth.address;
    document.getElementById("hdBtcLink").innerText = accounts.btc.address;

    document.getElementById("viewMyBtcBtn").addEventListener("click", () => {
        document.getElementById("btcAddressInput").value = accounts.btc.address;
        lookupBtcAddress();
        document.getElementById("btcResult").scrollIntoView({ behavior: "smooth", block: "start" });
    });
}

initHDAccountsCard();
