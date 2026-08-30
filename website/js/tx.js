// tx.html - the "full info" detail page a transaction ID in the
// explorer's Recent Transactions list now links to, instead of the
// list itself trying to show everything inline. Reuses
// /explorer/search (already an exact-match-by-ID lookup returning the
// same TransactionView the list uses) rather than adding a new
// backend endpoint.
//
// Laid out in two layers per the Explorer design spec: Layer 1 (always
// visible - status, plain-English summary, from/to/amount, a simple
// flow diagram) a normal user can read in a few seconds, and Layer 2
// (a collapsed "Advanced Blockchain Data" section - raw type, auth/
// signature fields, fee, external-network linkage, raw Data JSON) for
// anyone who intentionally wants the technical detail. Nothing in
// Layer 1 ever says "Vault"/"Bridge Contract"/exposes a raw function
// name - tx.summary is already built that way server-side
// (api/explorer_summary.go), and this file's own flow/category
// labels follow the same rule.

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

// networkLabelForTx names the real internal machinery (bridge/vault) -
// deliberately only ever surfaced inside the Advanced Details section
// below, never in the Layer 1 summary/flow, per the design spec's
// "don't expose internal bridge infrastructure outside Advanced
// Details" rule.
function networkLabelForTx(tx) {
    const bridgeTypes = ["BRIDGE_DEPOSIT", "BRIDGE_WITHDRAW", "BRIDGE_BTC_DEPOSIT", "BRIDGE_BTC_WITHDRAW"];
    const vaultTypes = ["VAULT_BURN", "VAULT_LOCK_ATTEST", "VAULT_RELEASE_COMPLETE", "VAULT_RELEASE_APPROVE"];
    if (bridgeTypes.includes(tx.Type)) return "Bridge (external network, legacy mirror model)";
    if (vaultTypes.includes(tx.Type)) return "Vault bridge (lock-and-mint, external network)";
    return "CCT network (CreditChain-internal)";
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
    const data = parseTxData(tx);

    if (tx.Type === "BRIDGE_WITHDRAW" && data.feeWei) {
        const wei = BigInt(data.feeWei);
        const whole = wei / 1000000000000000000n;
        const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
        return `${whole}.${frac} ETH`;
    }

    if (tx.Type === "EXCHANGE") {
        return `<span style="opacity:0.6;">A flat network fee may have applied at send time - not individually recorded per transaction, see the Send page for current rates.</span>`;
    }

    return null;
}

function parseTxData(tx) {
    if (!tx.Data) return {};
    try {
        return JSON.parse(tx.Data);
    } catch (e) {
        return {};
    }
}

// CATEGORY_LABEL turns the backend's coarse tx.category (see
// api/explorer_summary.go's txCategory) into the plain noun a normal
// user reads - the same handful of buckets the Explorer design spec
// asks for (Transfer/Swap/Cross-Network Transfer/Liquidity/Lending/
// Borrowing/Staking/Unstaking), plus "Repayment" (a real, distinct
// bucket this chain has that the spec's illustrative list didn't
// enumerate) and "Other Activity" for anything not user-facing enough
// to need its own treatment (governance, admin, moderation, etc.).
const CATEGORY_LABEL = {
    transfer: "Transfer",
    swap: "Swap",
    cross_network: "Cross-Network Transfer",
    liquidity: "Liquidity",
    lending: "Lending",
    borrowing: "Borrowing",
    repayment: "Repayment",
    staking: "Staking",
    unstaking: "Unstaking",
    other: "Other Activity",
};

const SMALLEST_UNIT_DECIMALS = { ETH_CCC1: 18, USDC_CCC1: 6, BTC_CCC1: 8, ETH: 18, USDC: 6, BTC: 8 };

// smallestToDecimalDisplay mirrors website/js/vault.js's/explorer.js's
// own copy of the same conversion - duplicated locally rather than a
// shared <script> include, matching this codebase's existing "each
// page keeps its own tiny copy of a small formatter" precedent.
function smallestToDecimalDisplay(smallestStr, decimals) {
    if (smallestStr === undefined || smallestStr === null || smallestStr === "") return "0";
    const base = 10n ** BigInt(decimals);
    const n = BigInt(smallestStr);
    const whole = n / base;
    const frac = (n % base).toString().padStart(decimals, "0").replace(/0+$/, "") || "0";
    return frac === "0" ? `${whole}` : `${whole}.${frac}`;
}

// CHAIN_DISPLAY_NAME turns a short chain code (as stored in vault
// transaction Data - "ETH"/"BTC"/"USDC") into the full network name a
// normal user recognizes - "Wrapped Ethereum"/"Wrapped Bitcoin" over a
// raw symbol like "ETH_BRIDGED", per the Explorer design spec's naming
// preference. Used by both primaryAmount's Wrapped-asset symbols below
// and flowDiagram's network-name nodes.
const CHAIN_DISPLAY_NAME = { ETH: "Ethereum", BTC: "Bitcoin", USDC: "Ethereum (USDC)" };

// primaryAmount is a best-effort "what moved, in what asset" for the
// Layer-1 summary card's Amount row - only attempted for the
// transaction types where the real amount+symbol are unambiguous from
// tx.Amount/tx.Data (the types the Explorer design spec's flow
// diagram cares most about). Returns null rather than guessing for
// anything else - the plain-English tx.summary sentence above it
// already says what happened either way, so a missing Amount row here
// is never the only place that information appears.
function primaryAmount(tx) {
    const data = parseTxData(tx);
    switch (tx.Type) {
        case "EXCHANGE": {
            const symbol = data.symbol || "CCT";
            const decimals = SMALLEST_UNIT_DECIMALS[symbol];
            return { amount: decimals ? smallestToDecimalDisplay(String(tx.Amount), decimals) : String(tx.Amount), symbol };
        }
        case "BRIDGE_WITHDRAW":
            return { amount: smallestToDecimalDisplay(String(tx.Amount), 18), symbol: "ETH" };
        case "BRIDGE_BTC_WITHDRAW":
            return { amount: smallestToDecimalDisplay(String(tx.Amount), 8), symbol: "BTC" };
        case "BRIDGE_DEPOSIT":
            return { amount: String(tx.Amount), symbol: "Wrapped Ethereum" };
        case "BRIDGE_BTC_DEPOSIT":
            return { amount: String(tx.Amount), symbol: "Wrapped Bitcoin" };
        case "VAULT_BURN":
        case "VAULT_LOCK_ATTEST": {
            const chain = data.chain || data.Chain;
            const decimals = SMALLEST_UNIT_DECIMALS[chain];
            const raw = data.amountSmallestUnit;
            if (!chain || raw === undefined) return null;
            const chainName = CHAIN_DISPLAY_NAME[chain] || chain;
            return { amount: decimals ? smallestToDecimalDisplay(raw, decimals) : String(raw), symbol: `Wrapped ${chainName}` };
        }
        case "STAKE_ETH":
        case "UNSTAKE_ETH": {
            const symbol = data.symbol || "ETHTEST";
            if (data.amount === undefined) return null;
            return { amount: String(data.amount), symbol };
        }
        default:
            return null;
    }
}

// flowDiagram draws the "who/what/who" picture the Explorer design
// spec asks for - a plain sender-amount-recipient line for an ordinary
// transaction, or source-network -> CreditChain -> destination-network
// for a cross-network one, without ever naming the internal bridge/
// vault machinery that makes the cross-network leg actually work
// (that detail lives in Advanced Details instead).
function flowDiagram(tx, amount) {
    const amountLabel = amount ? `<div class="tx-flow-amount">${amount.amount} ${escapeHtml(amount.symbol)}</div>` : "";

    if (tx.category === "cross_network") {
        const data = parseTxData(tx);
        const rawChain = data.chain || data.Chain || "";
        const chainName = CHAIN_DISPLAY_NAME[rawChain] || rawChain;
        let left = "CreditChain";
        let right = "CreditChain";
        if (["BRIDGE_WITHDRAW", "BRIDGE_BTC_WITHDRAW", "VAULT_BURN"].includes(tx.Type)) {
            right = chainName || (tx.Type === "BRIDGE_BTC_WITHDRAW" ? "Bitcoin" : "Ethereum");
        } else if (["BRIDGE_DEPOSIT", "BRIDGE_BTC_DEPOSIT", "VAULT_LOCK_ATTEST"].includes(tx.Type)) {
            left = chainName || (tx.Type === "BRIDGE_BTC_DEPOSIT" ? "Bitcoin" : "Ethereum");
        } else if (tx.Type === "VAULT_RELEASE_COMPLETE") {
            left = "CreditChain";
            right = "External network";
        }
        return `
            <div class="tx-flow">
                <div class="tx-flow-node">${escapeHtml(left)}</div>
                <div class="tx-flow-arrow">${amountLabel}&rarr;</div>
                <div class="tx-flow-node">${escapeHtml(right)}</div>
            </div>
        `;
    }

    return `
        <div class="tx-flow">
            <div class="tx-flow-node">${formatAddress(tx.From) || "System"}</div>
            <div class="tx-flow-arrow">${amountLabel}&rarr;</div>
            <div class="tx-flow-node">${formatAddress(tx.To) || "System"}</div>
        </div>
    `;
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
            container.innerHTML = `<p class="explorer-empty">No confirmed transaction found with ID "${escapeHtml(id)}". It may still be waiting to be included in a block, or the ID may be wrong.</p>`;
            return;
        }

        const tx = data.transactions[0];
        const time = tx.Timestamp ? new Date(tx.Timestamp * 1000).toLocaleString() : "-";
        const amount = primaryAmount(tx);
        const categoryLabel = CATEGORY_LABEL[tx.category] || "Activity";

        const reputationRow = tx.reputationDelta
            ? txRow(
                  "Reputation",
                  `<span style="color:${tx.reputationDelta > 0 ? "#16A34A" : "#ff6b6b"};">${tx.reputationDelta > 0 ? "+" : ""}${tx.reputationDelta}</span>` +
                      (tx.reputationTarget ? ` for ${formatAddress(tx.reputationTarget)}` : "")
              )
            : "";

        const feeValue = feeRowValue(tx);
        const feeRow = feeValue ? txRow("Fee", feeValue) : "";
        const externalLinkRow = tx.externalTxLink
            ? txRow(
                  "External Tx",
                  `<a href="${tx.externalTxLink}" target="_blank" rel="noopener" style="color:var(--cc-accent-gold, #16B8A6);">${tx.externalTxHash}</a>` +
                      `<br><span style="opacity:0.55;font-size:11px;">CreditChain bridged this - view the real transaction on the external network</span>`
              )
            : "";

        container.innerHTML = `
            <div class="tx-status-header">
                <span class="tx-status-icon" style="color:#16A34A;">&#10003;</span>
                <span class="tx-status-text" style="color:#16A34A;">Confirmed</span>
                <span class="tx-category-badge">${escapeHtml(categoryLabel)}</span>
            </div>

            <p class="tx-summary">${tx.summary ? escapeHtml(tx.summary) : escapeHtml(tx.Type)}</p>

            ${flowDiagram(tx, amount)}

            <div class="tx-actions">
                <button type="button" class="tx-action-btn" onclick="copyTxIdToClipboard()">Copy Transaction ID</button>
                <button type="button" class="tx-action-btn" onclick="shareTxLink()">Share</button>
            </div>

            <h4 class="tx-section-title">Transfer Details</h4>
            ${txRow("From", addressWithCopy(tx.From, "txFromCopy"))}
            ${txRow("To", addressWithCopy(tx.To, "txToCopy"))}
            ${amount ? txRow("Amount", `${amount.amount} ${escapeHtml(amount.symbol)}`) : ""}
            ${txRow("Time", time)}
            ${reputationRow}

            <details class="tx-advanced">
                <summary>Advanced Blockchain Data</summary>
                ${txRow("Transaction ID", `<span id="txIdValue">${tx.ID}</span> ${safeCopyIcon(tx.ID, "txIdCopy")}`)}
                ${txRow("Raw Type", tx.Type)}
                ${txRow("Category", tx.category || "other")}
                ${txRow("Network", networkLabelForTx(tx))}
                ${txRow("Authenticated Actor", tx.authAddress ? addressWithCopy(tx.authAddress, "txAuthCopy") : "-")}
                ${txRow("Auth Public Key", tx.authPublicKey ? `<span style="word-break:break-all;">${tx.authPublicKey}</span>` : "-")}
                ${txRow("Auth Signature", tx.authSignature ? `<span style="word-break:break-all;">${tx.authSignature}</span>` : "-")}
                ${txRow("Auth Timestamp", tx.authTimestamp ? new Date(tx.authTimestamp * 1000).toLocaleString() : "-")}
                ${feeRow}
                ${externalLinkRow}
                <div class="tx-raw">
                    <div class="tx-row-label" style="margin-bottom:6px;">Raw transaction data</div>
                    <pre>${tx.Data ? JSON.stringify(JSON.parse(tx.Data), null, 2) : "(no additional data)"}</pre>
                </div>
            </details>
        `;

        window._txDetailCurrent = tx;
    } catch (e) {
        console.error(e);
        container.innerHTML = `<p class="explorer-empty">Failed to load this transaction.</p>`;
    }
}

function copyTxIdToClipboard() {
    const tx = window._txDetailCurrent;
    if (!tx || !navigator.clipboard) return;
    navigator.clipboard.writeText(tx.ID).catch(() => {});
}

function shareTxLink() {
    const url = window.location.href;
    if (navigator.share) {
        navigator.share({ title: "CreditChain Transaction", url }).catch(() => {});
        return;
    }
    if (navigator.clipboard) {
        navigator.clipboard.writeText(url).catch(() => {});
    }
}

loadTxDetail();
