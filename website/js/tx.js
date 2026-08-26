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
                  `<span style="color:${tx.reputationDelta > 0 ? "#00ff95" : "#ff6b6b"};">${tx.reputationDelta > 0 ? "+" : ""}${tx.reputationDelta}</span>` +
                      (tx.reputationTarget ? ` for ${formatAddress(tx.reputationTarget)}` : "")
              )
            : "";

        container.innerHTML = `
            <p class="tx-summary">${tx.summary || tx.Type}</p>

            ${txRow("Status", `<span class="explorer-pill confirmed">confirmed</span>`)}
            ${txRow("Type", tx.Type)}
            ${txRow("Transaction ID", `<span id="txIdValue">${tx.ID}</span> ${safeCopyIcon(tx.ID, "txIdCopy")}`)}
            ${txRow("From", addressWithCopy(tx.From, "txFromCopy"))}
            ${txRow("To", addressWithCopy(tx.To, "txToCopy"))}
            ${txRow("Amount", String(tx.Amount))}
            ${txRow("Time", time)}
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
