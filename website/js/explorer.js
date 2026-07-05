const EXPLORER_PAGE_SIZE = 10;

let blocksOffset = 0;
let txOffset = 0;
let searchActive = false;

function shortHash(hash) {
    if (!hash) return "-";
    return hash.length > 16 ? `${hash.slice(0, 8)}...${hash.slice(-6)}` : hash;
}

function renderBlockRow(block) {
    const txCount = (block.Transactions || []).length;
    const time = block.Timestamp ? new Date(block.Timestamp * 1000).toLocaleString() : "-";
    return `
        <div class="explorer-row" title="${block.Hash || ""}">
            <span>#${block.Index}</span>
            <span class="explorer-hash">${shortHash(block.Hash)}</span>
            <span>${txCount} tx</span>
            <span class="explorer-hash">${block.Validator ? shortHash(block.Validator) : "-"}</span>
            <span>${time}</span>
        </div>
    `;
}

// txTypeLabel distinguishes WHICH token a transaction actually moved -
// tx.Type alone (e.g. "STAKE_ETH") doesn't say if it was CCT, ETHTEST,
// or USDCTEST, since which symbol a stake/unstake/transfer/offer
// concerns lives inside tx.Data's JSON payload, not as its own
// top-level field. Without this, a CCT stake and an ETHTEST stake are
// indistinguishable in history - exactly the "should be distinguished"
// gap this fixes. Defaults to ETHTEST for STAKE_ETH/UNSTAKE_ETH and
// CCT for EXCHANGE when Data is empty, matching the same
// backward-compatible defaults the backend itself uses for
// transactions recorded before Symbol existed.
function txTypeLabel(tx) {
    let data = {};
    if (tx.Data) {
        try {
            data = JSON.parse(tx.Data);
        } catch (e) {
            data = {};
        }
    }

    if (tx.Type === "STAKE_ETH" || tx.Type === "UNSTAKE_ETH") {
        return `${tx.Type} ${data.symbol || "ETHTEST"}`;
    }
    if (tx.Type === "EXCHANGE") {
        return `${tx.Type} ${data.symbol || "CCT"}`;
    }
    if (tx.Type === "P2P_OFFER_CREATE" && data.offerSymbol) {
        return `${tx.Type} ${data.offerSymbol}→${data.wantSymbol || "?"}`;
    }
    return tx.Type;
}

// reputationBadge renders a small "+N rep" / "-N rep" pill for any
// transaction the backend attached a core.ReputationEvent to (see
// api/explorer_summary.go's viewTransaction) - "" for the vast
// majority of transactions, which don't touch anyone's trust score.
function reputationBadge(tx) {
    if (!tx.reputationDelta) return "";
    const positive = tx.reputationDelta > 0;
    const sign = positive ? "+" : "";
    const color = positive ? "#00ff95" : "#ff6b6b";
    const target = tx.reputationTarget ? ` for ${shortHash(tx.reputationTarget)}` : "";
    return `<span style="color:${color};font-weight:600;font-size:11px;border:1px solid ${color};border-radius:8px;padding:1px 7px;" title="Reputation change${target}">${sign}${tx.reputationDelta} rep</span>`;
}

function renderTxRow(tx, confirmed) {
    const label = confirmed ? "confirmed" : "pending";
    const time = tx.Timestamp ? new Date(tx.Timestamp * 1000).toLocaleString() : "-";
    const summary = tx.summary || txTypeLabel(tx);
    return `
        <div class="explorer-row" title="${tx.ID || ""}">
            <span>[${label}]</span>
            <span class="explorer-hash">${shortHash(tx.From)} &rarr; ${shortHash(tx.To)}</span>
            <span>${tx.Amount} (${txTypeLabel(tx)})</span>
            ${reputationBadge(tx)}
            <span>${time}</span>
        </div>
        <div class="explorer-row" style="border-bottom:none;padding-top:0;opacity:0.75;font-size:12px;">
            <span>${summary}</span>
        </div>
    `;
}

function renderVoteRow(vote) {
    const time = vote.timestamp ? new Date(vote.timestamp * 1000).toLocaleString() : "-";
    return `
        <div class="explorer-row">
            <span>${vote.support ? "YES" : "NO"}</span>
            <span class="explorer-hash">${vote.title}</span>
            <span>${time}</span>
        </div>
    `;
}

// renderAddressProfile is the "explorer sheet" for a single address -
// balances across every ledger, reputation/tier, and ordered
// (newest-first) transaction and vote history, all in one place
// instead of the caller having to piece it together from several
// separate lookups.
function renderAddressProfile(profile) {
    const transactions = profile.transactions || [];
    const votes = profile.votes || [];

    const txSection =
        transactions.length === 0
            ? `<div class="explorer-empty">No transactions yet.</div>`
            : transactions.map((tx) => renderTxRow(tx, true)).join("");

    const votesSection =
        votes.length === 0
            ? `<div class="explorer-empty">No governance votes yet.</div>`
            : votes.map(renderVoteRow).join("");

    const shownCount = transactions.length;
    const totalNote =
        profile.transactionsTotal > shownCount
            ? ` (showing most recent ${shownCount} of ${profile.transactionsTotal})`
            : "";

    return `
        <div class="card" style="margin:0;">
            <h3 style="word-break:break-all;">${profile.address}</h3>

            <div class="explorer-row">
                <span>CCT</span>
                <span>${profile.cctBalance}</span>
            </div>
            <div class="explorer-row">
                <span>CCT staked</span>
                <span>${profile.cctStaked}</span>
            </div>
            <div class="explorer-row">
                <span>ETHTEST staked</span>
                <span>${profile.ethTestStaked}</span>
            </div>
            <div class="explorer-row">
                <span>oETH (staked-ETHTEST receipt)</span>
                <span>${profile.oethBalance}</span>
            </div>
            <div class="explorer-row">
                <span>USDCTEST staked</span>
                <span>${profile.usdcTestStaked}</span>
            </div>
            <div class="explorer-row">
                <span>REPT</span>
                <span>${profile.reptBalance}</span>
            </div>
            <p style="opacity:0.6;font-size:12px;">
                Held (not staked) ETHTEST/USDCTEST can't be shown for an arbitrary
                looked-up address - see your own Wallet page instead, which can
                derive your own sub-address balance.
            </p>
            <div class="explorer-row">
                <span>Reputation</span>
                <span>${profile.reputation} (${profile.creditTier})</span>
            </div>
            <div class="explorer-row">
                <span>Voting Power</span>
                <span>${profile.votingPower}</span>
            </div>
        </div>

        <h3>Transaction History${totalNote}</h3>
        ${txSection}

        <h3>Governance Votes</h3>
        ${votesSection}
    `;
}

function renderList(container, items, emptyMessage, rowFn) {
    if (!items || items.length === 0) {
        container.innerHTML = `<div class="explorer-empty">${emptyMessage}</div>`;
        return;
    }
    container.innerHTML = items.map(rowFn).join("");
}

async function loadExplorer() {
    if (searchActive) return;

    try {
        const data = await apiGet(
            `/explorer?blocksOffset=${blocksOffset}&blocksLimit=${EXPLORER_PAGE_SIZE}&txOffset=${txOffset}&txLimit=${EXPLORER_PAGE_SIZE}`
        );

        const blockDiv = document.getElementById("blocks");
        const shownBlocks = data.blocks || [];
        renderList(blockDiv, shownBlocks, "No blocks yet.", renderBlockRow);

        const blocksTotal = data.blocksTotal || 0;
        document.getElementById("blocksRange").innerText =
            shownBlocks.length === 0
                ? ""
                : `Blocks #${shownBlocks[shownBlocks.length - 1].Index}-#${shownBlocks[0].Index} of ${blocksTotal}`;
        document.getElementById("blocksPrev").disabled = blocksOffset === 0;
        document.getElementById("blocksNext").disabled =
            shownBlocks.length === 0 || shownBlocks[shownBlocks.length - 1].Index === 0;

        const txDiv = document.getElementById("transactions");
        const pendingTxs = txOffset === 0 ? (data.pendingTransactions || []) : [];
        const confirmedTxs = data.transactions || [];
        const rows = [
            ...pendingTxs.map((tx) => renderTxRow(tx, false)),
            ...confirmedTxs.map((tx) => renderTxRow(tx, true)),
        ];
        if (rows.length === 0) {
            txDiv.innerHTML = `<div class="explorer-empty">No transactions.</div>`;
        } else {
            txDiv.innerHTML = rows.join("");
        }
        document.getElementById("txRange").innerText =
            txOffset === 0 ? "Most recent" : `Starting at transaction ${txOffset}`;
        document.getElementById("txPrev").disabled = txOffset === 0;
        document.getElementById("txNext").disabled = !data.transactionsHasMore;
    } catch (e) {
        console.error(e);
    }
}

async function runSearch() {
    const input = document.getElementById("explorerSearchInput");
    const query = input.value.trim();
    const resultDiv = document.getElementById("explorerSearchResult");

    if (!query) return;

    try {
        const data = await apiGet(`/explorer/search?q=${encodeURIComponent(query)}`);
        searchActive = true;
        document.getElementById("explorerSearchClearBtn").style.display = "inline-block";

        if (!data.found) {
            resultDiv.innerHTML = `<div class="explorer-empty">No block or transaction matches "${query}".</div>`;
            return;
        }

        if (data.type === "block") {
            resultDiv.innerHTML = renderBlockRow(data.block);
        } else if (data.type === "transactions") {
            resultDiv.innerHTML = data.transactions.map((tx) => renderTxRow(tx, true)).join("");
        } else if (data.type === "address") {
            resultDiv.innerHTML = renderAddressProfile(data.address);
        }
    } catch (e) {
        resultDiv.innerHTML = `<div class="explorer-empty">Search failed.</div>`;
        console.error(e);
    }
}

function clearSearch() {
    searchActive = false;
    document.getElementById("explorerSearchInput").value = "";
    document.getElementById("explorerSearchResult").innerHTML = "";
    document.getElementById("explorerSearchClearBtn").style.display = "none";
    loadExplorer();
}

document.getElementById("explorerSearchBtn").addEventListener("click", runSearch);
document.getElementById("explorerSearchInput").addEventListener("keydown", (e) => {
    if (e.key === "Enter") runSearch();
});
document.getElementById("explorerSearchClearBtn").addEventListener("click", clearSearch);

document.getElementById("blocksPrev").addEventListener("click", () => {
    blocksOffset = Math.max(0, blocksOffset - EXPLORER_PAGE_SIZE);
    loadExplorer();
});
document.getElementById("blocksNext").addEventListener("click", () => {
    blocksOffset += EXPLORER_PAGE_SIZE;
    loadExplorer();
});
document.getElementById("txPrev").addEventListener("click", () => {
    txOffset = Math.max(0, txOffset - EXPLORER_PAGE_SIZE);
    loadExplorer();
});
document.getElementById("txNext").addEventListener("click", () => {
    txOffset += EXPLORER_PAGE_SIZE;
    loadExplorer();
});

loadExplorer();
setInterval(loadExplorer, 10000);
