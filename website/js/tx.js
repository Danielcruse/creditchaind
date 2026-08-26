// tx.html - the "full info" detail page a transaction ID in the
// explorer's Recent Transactions list now links to, instead of the
// list itself trying to show everything inline. Reuses
// /explorer/search (already an exact-match-by-ID lookup returning the
// same TransactionView the list uses) rather than adding a new
// backend endpoint.

function txRow(label, valueHTML) {
    return `
        <div class="tx-row">
            <span class="tx-row-label">${label}</span>
            <span class="tx-row-value">${valueHTML}</span>
        </div>
    `;
}

// safeCopyIcon degrades to no icon (rather than throwing and blanking
// the whole detail page) if the shared copyIconHTML helper is ever
// unavailable - the transaction info itself is the important part of
// this page, a missing copy button is a minor cosmetic loss.
function safeCopyIcon(text, iconId) {
    try {
        return typeof copyIconHTML === "function" ? copyIconHTML(text, iconId) : "";
    } catch (e) {
        return "";
    }
}

function addressWithCopy(address, iconId) {
    if (!address) return `<span style="opacity:0.5;">(none - system-side)</span>`;
    return `${formatAddress(address)} ${safeCopyIcon(formatAddress(address), iconId)}`;
}

// networkLabelForTx mirrors asset.js's own copy of this same small
// derivation (kept in sync manually, same "each page keeps its own
// copy of a tiny helper" precedent already used for weiToEth/
// satsToBtc elsewhere in this codebase) - distinguishes a real
// external-chain bridge leg from an ordinary CreditChain-internal
// transfer.
function networkLabelForTx(tx) {
    const bridgeTypes = ["BRIDGE_DEPOSIT", "BRIDGE_WITHDRAW", "BRIDGE_BTC_DEPOSIT", "BRIDGE_BTC_WITHDRAW"];
    return bridgeTypes.includes(tx.Type) ? "Bridge (external network)" : "CCT network (CreditChain-internal)";
}

// feeRowValue returns a fee display value where a real, stored,
// authoritative number exists in tx.Data - never a guessed/fabricated
// figure. TxBridgeWithdraw always has a real FeeWei snapshotted at
// request time (see api/eth_bridge_withdraw.go's bridgeWithdrawData
// doc comment) - a genuine number, safe to show directly. Ordinary
// CCT-network transfers don't persist their flat fee into tx.Data at
// all (only the one-time HTTP response did, which isn't recoverable
// after the fact) - rather than hardcode a fee constant here that
// could silently drift from the real server-side value over time,
// this is disclosed as "not recorded" instead of guessed.
function feeRowValue(tx) {
    let parsedData = null;
    try {
        parsedData = tx.Data ? JSON.parse(tx.Data) : null;
    } catch (e) {
        parsedData = null;
    }

    if (tx.Type === "BRIDGE_WITHDRAW" && parsedData && parsedData.feeWei) {
        const wei = BigInt(parsedData.feeWei);
        const whole = wei / 1000000000000000000n;
        const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
        return `${whole}.${frac} ETH`;
    }

    if (tx.Type === "EXCHANGE") {
        return `<span style="opacity:0.6;">A flat network fee may have applied at send time - not individually recorded per transaction, see the Send page for current rates.</span>`;
    }

    return null;
}

async function loadTxDetail() {
    const id = new URLSearchParams(window.location.search).get("id");
    const container = document.getElementById("txDetailContent");

    if (!id) {
        container.innerHTML = `<p class="explorer-empty">No transaction ID given.</p>`;
        return;
    }

    try {
        const data = await apiGet(`/explorer/search?q=${encodeURIComponent(id)}`);
        if (!data.found || data.type !== "transactions" || !data.transactions || data.transactions.length === 0) {
            container.innerHTML = `<p class="explorer-empty">No confirmed transaction found with ID "${id}". It may still be pending, or the ID may be wrong.</p>`;
            return;
        }

        const tx = data.transactions[0];
        const time = tx.Timestamp ? new Date(tx.Timestamp * 1000).toLocaleString() : "-";
        const reputation = tx.reputationDelta
            ? txRow(
                  "Reputation",
                  `<span style="color:${tx.reputationDelta > 0 ? "#0f9d58" : "#ff6b6b"};">${tx.reputationDelta > 0 ? "+" : ""}${tx.reputationDelta}</span>` +
                      (tx.reputationTarget ? ` for ${formatAddress(tx.reputationTarget)}` : "")
              )
            : "";
        const feeValue = feeRowValue(tx);
        const feeRow = feeValue ? txRow("Fee", feeValue) : "";
        const externalLinkRow = tx.externalTxLink
            ? txRow(
                  "External Tx",
                  `<a href="${tx.externalTxLink}" target="_blank" rel="noopener" style="color:var(--cc-accent-gold, #c9a227);">${tx.externalTxHash}</a>` +
                      `<br><span style="opacity:0.55;font-size:11px;">CreditChain bridged this - view the real transaction on the external network</span>`
              )
            : "";

        container.innerHTML = `
            <p class="tx-summary">${tx.summary ? escapeHtml(tx.summary) : escapeHtml(tx.Type)}</p>

            ${txRow("Status", `<span class="explorer-pill confirmed">confirmed</span>`)}
            ${txRow("Type", tx.Type)}
            ${txRow("Network", networkLabelForTx(tx))}
            ${txRow("Transaction ID", `<span id="txIdValue">${tx.ID}</span> ${safeCopyIcon(tx.ID, "txIdCopy")}`)}
            ${txRow("From", addressWithCopy(tx.From, "txFromCopy"))}
            ${txRow("To", addressWithCopy(tx.To, "txToCopy"))}
            ${txRow("Amount", String(tx.Amount))}
            ${txRow("Time", time)}
            ${feeRow}
            ${externalLinkRow}
            ${reputation}

            <details class="tx-raw">
                <summary>Raw transaction data</summary>
                <pre>${tx.Data ? JSON.stringify(JSON.parse(tx.Data), null, 2) : "(no additional data)"}</pre>
            </details>
        `;
    } catch (e) {
        console.error(e);
        container.innerHTML = `<p class="explorer-empty">Failed to load this transaction.</p>`;
    }
}

loadTxDetail();
