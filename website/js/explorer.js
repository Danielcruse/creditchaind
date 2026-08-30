const EXPLORER_PAGE_SIZE = 10;

let blocksOffset = 0;
let txOffset = 0;
let searchActive = false;

function shortHash(hash) {
    if (!hash) return "-";
    return hash.length > 16 ? `${hash.slice(0, 8)}...${hash.slice(-6)}` : hash;
}

// renderBlockCube renders one block as a connected "cube" tile (see
// .block-chain/.block-cube in explorer.html) instead of a table row -
// each tile visually links to the next via the dashed connector drawn
// by .block-cube::after, echoing how blocks actually chain together.
function renderBlockCube(block) {
    const txCount = (block.Transactions || []).length;
    const time = block.Timestamp ? new Date(block.Timestamp * 1000).toLocaleString() : "-";
    return `
        <div class="block-cube" title="${block.Hash || ""}" onclick="jumpToBlock(${block.Index})">
            <div class="block-cube-index">#${block.Index}</div>
            <div class="block-cube-row">${shortHash(block.Hash)}</div>
            <div class="block-cube-row">${block.Validator ? shortHash(block.Validator) : "genesis"}</div>
            <div class="block-cube-row" style="opacity:0.5;">${time}</div>
            <span class="block-cube-txns">${txCount} tx</span>
        </div>
    `;
}

// jumpToBlock lets clicking a cube act as a shortcut into the same
// search flow the search box uses, instead of requiring the user to
// type the block number themselves.
function jumpToBlock(index) {
    document.getElementById("explorerSearchInput").value = String(index);
    runSearch();
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

// DECIMAL_SYMBOL_DECIMALS is which symbols carry raw smallest-unit
// amounts needing decimal conversion for display - ETH_CCC1/USDC_CCC1
// (see api/vault_token.go's DecimalTokenStore, 18/6 decimals) and
// BTC_CCC1 (btcCcc1Store holds raw satoshis, 8 decimals - same
// convention website/js/vault.js's VAULT_CHAINS.BTC and asset.js's own
// sats-to-BTC conversion already use for every other BTC_CCC1 display
// in this app; explorer previously left this out and showed raw
// satoshi integers instead of BTC amounts). CCT's TxExchange.Amount
// stays a plain whole-CCT integer even after CCT's own 18-decimal
// balance migration (see api/apply.go's applyTransfer - only the FEE
// moved to smallest-unit precision, not the transfer amount itself),
// and REPT/other symbols are already whole-integer-scaled, so those
// don't belong here.
const DECIMAL_SYMBOL_DECIMALS = { ETH_CCC1: 18, USDC_CCC1: 6, BTC_CCC1: 8 };

// smallestToDecimalDisplay mirrors website/js/vault.js's own
// smallestToDecimal exactly - duplicated locally (not a shared
// <script> include) since explorer.html doesn't otherwise depend on
// vault.js at all.
function smallestToDecimalDisplay(smallestStr, decimals) {
    if (smallestStr === undefined || smallestStr === null || smallestStr === "") return "0";
    const base = 10n ** BigInt(decimals);
    const n = BigInt(smallestStr);
    const whole = n / base;
    const frac = (n % base).toString().padStart(decimals, "0").replace(/0+$/, "") || "0";
    return frac === "0" ? `${whole}` : `${whole}.${frac}`;
}

// parseTxData is the same "best-effort JSON.parse, empty object on any
// failure" pattern txTypeLabel already uses independently - kept as its
// own small duplicate rather than a shared refactor, matching this
// file's existing style (every tx-detail function here already parses
// tx.Data locally rather than threading a shared parsed value through).
function parseTxData(tx) {
    if (!tx.Data) return {};
    try {
        return JSON.parse(tx.Data);
    } catch (e) {
        return {};
    }
}

// formatSymbolAmount decimal-converts only the symbols that actually
// need it (DECIMAL_SYMBOL_DECIMALS), leaving every other symbol's raw
// integer exactly as-is (already correct for those) - shared by both
// the transaction-list Amount column and the address profile's held
// balances, which carry the exact same raw-smallest-unit convention.
function formatSymbolAmount(symbol, rawAmount) {
    const decimals = symbol && DECIMAL_SYMBOL_DECIMALS[symbol];
    if (!decimals) return rawAmount;
    return smallestToDecimalDisplay(String(rawAmount), decimals);
}

// VAULT_CHAIN_DECIMALS/VAULT_CHAIN_LABEL are the vault bridge's own
// "chain" field (ETH/USDC/BTC - see api/vault_lock.go's vaultBurnData/
// vaultLockAttestData) mapped to the same decimal precision
// DECIMAL_SYMBOL_DECIMALS already uses for the equivalent CCC1 symbol -
// BTC's amountSmallestUnit is raw satoshis, same 8-decimal conversion
// as BTC_CCC1 above.
const VAULT_CHAIN_DECIMALS = { ETH: 18, USDC: 6, BTC: 8 };
const VAULT_CHAIN_LABEL = { ETH: "ETH", USDC: "USDC", BTC: "BTC" };

// isVaultBridgeType is every transaction type this session's vault
// bridge explorer work covers - all three carry their real amount only
// inside Data.amountSmallestUnit (tx.Amount is always 0 for these, see
// api/vault_lock.go's recordTx calls), so both the label AND the
// amount column need this special-cased, not just the label the way
// STAKE_ETH/EXCHANGE/P2P_OFFER_CREATE already are.
function isVaultBridgeType(type) {
    return type === "VAULT_BURN" || type === "VAULT_LOCK_ATTEST" || type === "VAULT_RELEASE_COMPLETE";
}

// formatTxAmount is txTypeLabel's counterpart for the Amount column.
function formatTxAmount(tx) {
    const data = parseTxData(tx);
    if (isVaultBridgeType(tx.Type)) {
        if (tx.Type === "VAULT_RELEASE_COMPLETE") return ""; // no amount field on this one - see its own doc comment
        const decimals = VAULT_CHAIN_DECIMALS[data.chain];
        return decimals === undefined ? data.amountSmallestUnit : smallestToDecimalDisplay(data.amountSmallestUnit, decimals);
    }
    const symbol = data.symbol || (tx.Type === "EXCHANGE" ? "CCT" : null);
    return formatSymbolAmount(symbol, tx.Amount);
}

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
    if (tx.Type === "VAULT_BURN") {
        // The moment a withdrawal irrevocably leaves CreditChain's own
        // side - the user's CCC1 balance is already burned once this
        // confirms, regardless of whether the real external release
        // (VAULT_RELEASE_COMPLETE, a separate later transaction) ever
        // succeeds. Signed by the withdrawing user themselves in-app,
        // not something CreditChain-the-operator initiates on anyone's
        // behalf.
        const chain = VAULT_CHAIN_LABEL[data.chain] || data.chain || "?";
        const dest = data.destinationExtAddress ? shortHash(data.destinationExtAddress) : "an external address";
        return `Moved Wrapped ${chain} off CreditChain to ${dest}`;
    }
    if (tx.Type === "VAULT_LOCK_ATTEST") {
        // One bridge validator's own independent confirmation that a
        // real external deposit happened - see core/vault_lock_attestation.go.
        // The underlying CCC1 mint only actually fires once enough of
        // these accumulate (bridgeValidatorThreshold), not on every
        // single one - this label describes what THIS attestation is
        // about, not "minting completed."
        const chain = VAULT_CHAIN_LABEL[data.chain] || data.chain || "?";
        const depositor = data.depositorCcAddress ? shortHash(data.depositorCcAddress) : "an unknown depositor";
        return `Confirmed a ${chain} deposit from ${depositor}`;
    }
    if (tx.Type === "VAULT_RELEASE_COMPLETE") {
        // The real external send actually landed - the part
        // VAULT_BURN's own label explicitly says isn't guaranteed. Only
        // ever recorded once a node-local relay observes the real
        // release()/broadcast succeed (api/vault_release_relay.go /
        // api/vault_btc_release_relay.go).
        return `Withdrawal confirmed on the external network${data.externalHash ? ` (${shortHash(data.externalHash)})` : ""}`;
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
    const color = positive ? "#16A34A" : "#ff6b6b";
    const target = tx.reputationTarget ? ` for ${shortHash(tx.reputationTarget)}` : "";
    return `<span style="color:${color};font-weight:600;font-size:11px;border:1px solid ${color};border-radius:8px;padding:1px 7px;" title="Reputation change${target}">${sign}${tx.reputationDelta} rep</span>`;
}

// renderTxRow's "Tx" column shows just the transaction ID, linked to
// tx.html for the full detail (summary, reputation, raw data) - the
// list itself stays a quick scannable index, not a place trying to
// show everything about every row. A pending transaction (still in
// the mempool, not yet in a block) has no page to link to yet, so it
// renders as plain text instead of a link.
//
// The Amount column prefers the backend's own tx.summary (a plain-
// English sentence already built once, server-side, in
// api/explorer_summary.go's buildTransactionSummary - "Sent 5 CCT",
// "Moved Wrapped BTC off CreditChain to ...", never "Vault"/"Bridge
// Contract" per the Explorer design spec) over reconstructing a label
// from tx.Type/tx.Data here, so this list can't drift out of sync with
// - or reintroduce jargon already cleaned out of - that one source of
// truth. txTypeLabel/formatTxAmount stay as a fallback only for a
// TransactionView that somehow arrives without a summary.
function renderTxRow(tx, confirmed) {
    const label = confirmed ? "confirmed" : "pending";
    const time = tx.Timestamp ? new Date(tx.Timestamp * 1000).toLocaleString() : "-";
    const idDisplay = confirmed
        ? `<a href="tx?id=${encodeURIComponent(tx.ID)}" style="color:#16A34A;">${shortHash(tx.ID)}</a>`
        : shortHash(tx.ID);
    const description = tx.summary
        ? escapeHtml(tx.summary)
        : (() => {
              const amount = formatTxAmount(tx);
              const amountPrefix = amount === "" ? "" : `${amount} `;
              return `${amountPrefix}(${txTypeLabel(tx)})`;
          })();
    return `
        <tr title="${tx.ID || ""}">
            <td data-label="Status"><span class="explorer-pill ${label}">${label}</span></td>
            <td data-label="Tx">${idDisplay}</td>
            <td data-label="From &rarr; To" class="explorer-hash">${shortHash(tx.From)} &rarr; ${shortHash(tx.To)}</td>
            <td data-label="Amount">${description} ${reputationBadge(tx)}</td>
            <td data-label="Time">${time}</td>
        </tr>
    `;
}

function renderVoteRow(vote) {
    const time = vote.timestamp ? new Date(vote.timestamp * 1000).toLocaleString() : "-";
    return `
        <div class="explorer-row">
            <span>${vote.support ? "YES" : "NO"}</span>
            <span class="explorer-hash">${escapeHtml(vote.title)}</span>
            <span>${time}</span>
        </div>
    `;
}

function renderLoanRow(loan) {
    const due = loan.dueAt ? new Date(loan.dueAt * 1000).toLocaleString() : "-";
    return `
        <div class="explorer-row">
            <span>${loan.role === "borrower" ? "Borrowed" : "Lent"}</span>
            <span class="explorer-hash">${loan.amount} ${loan.symbol}</span>
            <span class="explorer-pill ${loan.status === "REPAID" ? "confirmed" : "pending"}">${loan.status}</span>
            <span>${due}</span>
        </div>
    `;
}

// WRAPPED_ASSET_LABEL turns a raw held-balance symbol into the plain
// name the Explorer design spec asks for - "Wrapped Bitcoin"/"Wrapped
// Ethereum" over a raw symbol like "BTC_CCC1"/"ETHB". The technical
// symbol still appears, just as secondary metadata (see
// heldBalanceRow below), never as the primary label. Symbols not
// listed here (USDC_CCC1, USDCTEST, REPT, YENE, developer-registered
// tokens, ...) are already plain enough to show as-is.
const WRAPPED_ASSET_LABEL = {
    ETH_CCC1: "Wrapped Ethereum",
    BTC_CCC1: "Wrapped Bitcoin",
    STETH_CCC1: "Wrapped Staked Ethereum",
    ETHB: "Wrapped Ethereum",
    BTCB: "Wrapped Bitcoin",
};

function heldBalanceRow(b) {
    const friendly = WRAPPED_ASSET_LABEL[b.symbol];
    const label = friendly
        ? `${friendly} <span style="opacity:0.5;font-size:11px;">${b.symbol}</span>`
        : b.symbol;
    return `
        <div class="explorer-row">
            <span>${label}</span>
            <span>${formatSymbolAmount(b.symbol, b.balance)}</span>
        </div>`;
}

// renderAddressProfile is the "explorer sheet" for a single address -
// either a CreditChain-native (CCT) address or, when the search query
// itself was a real 0x Ethereum address, the same wallet's profile
// reached via its linked Ethereum-compatible address (see
// profile.lookedUpByEthAddress). Per the design spec this is the same
// wallet identity either way, never a separate product - only the
// badge and which address is shown "primary" change with which one
// was actually searched for. A Linked Addresses row only ever appears
// when profile.ethAddress/profile.address are BOTH populated, i.e. a
// genuinely registered (signed, account-level-verified) link exists -
// never inferred or assumed.
//
// Split into two tabs per the Explorer design spec: "Assets" (what
// this address holds right now - balances, staked positions, open
// loans) and "Activity" (what it's done - transaction history,
// governance votes), with reputation/tier/voting power surfaced above
// both tabs since those apply regardless of which tab is open.
function renderAddressProfile(profile) {
    const transactions = profile.transactions || [];
    const votes = profile.votes || [];
    const heldBalances = profile.heldBalances || [];
    const loans = profile.loans || [];

    // A real-shaped Ethereum address search that found no wallet on
    // this node has ever registered - honest "nothing known" rather
    // than a fabricated profile (see api/explorer.go's
    // HandleExplorerSearch doc comment on this branch).
    if (profile.lookedUpByEthAddress && !profile.address) {
        return `
            <div class="card" style="margin:0;">
                <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap;">
                    <h3 style="word-break:break-all;margin:0;">${profile.ethAddress}</h3>
                    <span class="explorer-addr-badge" title="Ethereum-compatible address">Ethereum Compatible</span>
                </div>
                <div class="explorer-empty" style="margin-top:10px;">
                    No CreditChain wallet has registered this Ethereum address yet.
                </div>
            </div>
        `;
    }

    const txSection =
        transactions.length === 0
            ? `<tr><td colspan="5" class="explorer-empty">No transactions yet.</td></tr>`
            : transactions.map((tx) => renderTxRow(tx, true)).join("");

    const votesSection =
        votes.length === 0
            ? `<div class="explorer-empty">No governance votes yet.</div>`
            : votes.map(renderVoteRow).join("");

    const heldBalancesSection =
        heldBalances.length === 0
            ? `<div class="explorer-empty">No other token balances.</div>`
            : heldBalances.map(heldBalanceRow).join("");

    const loansSection =
        loans.length === 0 ? `<div class="explorer-empty">No loans.</div>` : loans.map(renderLoanRow).join("");

    const shownCount = transactions.length;
    const totalNote =
        profile.transactionsTotal > shownCount
            ? ` (showing most recent ${shownCount} of ${profile.transactionsTotal})`
            : "";

    // primaryAddress/primaryBadge is whichever address the search
    // query itself named; linkedAddress/linkedLabel (if any) is its
    // verified counterpart, shown as a secondary "Linked Addresses"
    // row - never the reverse, and never shown at all when no link is
    // on file.
    const primaryAddress = profile.lookedUpByEthAddress ? profile.ethAddress : formatAddress(profile.address);
    const primaryBadge = profile.lookedUpByEthAddress ? "Ethereum Compatible" : "CreditChain Native";
    const linkedAddress = profile.lookedUpByEthAddress ? profile.address && formatAddress(profile.address) : profile.ethAddress;
    const linkedLabel = profile.lookedUpByEthAddress ? "CreditChain Native" : "Ethereum Compatible";
    const linkedSection = linkedAddress
        ? `
            <div class="explorer-row" style="margin-top:6px;">
                <span>Linked Address <span style="opacity:0.5;font-size:11px;">(${linkedLabel})</span></span>
                <span style="word-break:break-all;">${linkedAddress}</span>
            </div>`
        : "";

    return `
        <div class="card" style="margin:0;">
            <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap;">
                <h3 style="word-break:break-all;margin:0;">${primaryAddress}</h3>
                <span class="explorer-addr-badge" title="${profile.lookedUpByEthAddress ? "Ethereum-compatible address" : "CreditChain-native address"}">${primaryBadge}</span>
            </div>
            ${linkedSection}

            <div class="explorer-row" style="margin-top:6px;">
                <span>CCT Balance</span>
                <span style="font-weight:700;">${cctFromSmallest(profile.cctBalance)} CCT</span>
            </div>
            <div class="explorer-row">
                <span>Reputation</span>
                <span>${profile.reputation} (${profile.creditTier})</span>
            </div>
            <div class="explorer-row">
                <span>Voting Power</span>
                <span>${profile.votingPower}</span>
            </div>

            <div class="explorer-addr-tabs">
                <button type="button" class="explorer-addr-tab-btn active" data-addr-tab="assets" onclick="showAddrTab('assets')">Assets</button>
                <button type="button" class="explorer-addr-tab-btn" data-addr-tab="activity" onclick="showAddrTab('activity')">Activity</button>
            </div>

            <div class="explorer-addr-tab-panel" data-addr-panel="assets">
                <h4 style="margin-bottom:4px;">Other Balances</h4>
                ${heldBalancesSection}

                <h4 style="margin-bottom:4px;">Staked</h4>
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

                <h4 style="margin-bottom:4px;">Loans</h4>
                ${loansSection}
            </div>

            <div class="explorer-addr-tab-panel" data-addr-panel="activity" style="display:none;">
                <h4 style="margin-bottom:4px;">Transaction History${totalNote}</h4>
                <div class="explorer-table-scroll">
                    <table class="explorer-table"><tbody>${txSection}</tbody></table>
                </div>

                <h4 style="margin-bottom:4px;margin-top:14px;">Governance Votes</h4>
                ${votesSection}
            </div>
        </div>
    `;
}

// showAddrTab switches renderAddressProfile's two tabs - plain
// show/hide on the two known panels, not a generic tab framework,
// since this page only ever has the one address profile mounted at a
// time (a fresh search replaces the whole result div, tabs included).
function showAddrTab(name) {
    document.querySelectorAll("[data-addr-panel]").forEach((el) => {
        el.style.display = el.getAttribute("data-addr-panel") === name ? "" : "none";
    });
    document.querySelectorAll("[data-addr-tab]").forEach((el) => {
        el.classList.toggle("active", el.getAttribute("data-addr-tab") === name);
    });
}

function renderList(container, items, emptyMessage, rowFn) {
    if (!items || items.length === 0) {
        container.innerHTML = `<tr><td colspan="5" class="explorer-empty">${emptyMessage}</td></tr>`;
        return;
    }
    container.innerHTML = items.map(rowFn).join("");
}

// renderBlocks is renderList's counterpart for the block-cube chain -
// its container is a plain flex div, not a <table>, so the empty
// state can't reuse renderList's <tr>/<td colspan> markup.
function renderBlocks(container, blocks) {
    if (!blocks || blocks.length === 0) {
        container.innerHTML = `<div class="explorer-empty">No blocks yet.</div>`;
        return;
    }
    container.innerHTML = blocks.map(renderBlockCube).join("");
}

async function loadExplorer() {
    if (searchActive) return;

    try {
        const data = await apiGet(
            `/explorer?blocksOffset=${blocksOffset}&blocksLimit=${EXPLORER_PAGE_SIZE}&txOffset=${txOffset}&txLimit=${EXPLORER_PAGE_SIZE}`
        );

        const blockDiv = document.getElementById("blocks");
        const shownBlocks = data.blocks || [];
        renderBlocks(blockDiv, shownBlocks);

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
            txDiv.innerHTML = `<tr><td colspan="5" class="explorer-empty">No transactions.</td></tr>`;
        } else {
            txDiv.innerHTML = rows.join("");
        }
        document.getElementById("txRange").innerText =
            txOffset === 0 ? "Most recent" : `Starting at transaction ${txOffset}`;
        document.getElementById("txPrev").disabled = txOffset === 0;
        document.getElementById("txNext").disabled = !data.transactionsHasMore;

        // Stats bar - block height/latest-block-time reflect the true
        // chain tip regardless of which page of blocks is currently
        // shown (blocksTotal already is the tip index+1, and the tip
        // itself is always shownBlocks[0] on page one).
        document.getElementById("statBlockHeight").innerText = blocksTotal > 0 ? blocksTotal - 1 : 0;
        document.getElementById("statPendingTx").innerText = (data.pendingTransactions || []).length;
        document.getElementById("statPeerCount").innerText = data.peerCount ?? 0;
        if (blocksOffset === 0 && shownBlocks.length > 0 && shownBlocks[0].Timestamp) {
            document.getElementById("statLatestBlockTime").innerText =
                new Date(shownBlocks[0].Timestamp * 1000).toLocaleTimeString();
        }
    } catch (e) {
        console.error(e);
    }
}

// RESULT_TYPE_LABEL names what was actually found, shown as a small
// banner above the result itself - per the Explorer design spec, a
// search should say plainly what kind of thing matched (block /
// transaction / address) before showing its detail, rather than
// leaving the visitor to infer that from the shape of what renders.
const RESULT_TYPE_LABEL = {
    block: "Block found",
    transactions: "Transaction found",
};

// addressResultTypeLabel is RESULT_TYPE_LABEL's counterpart for a
// found address - distinguishes which of the two address kinds the
// design spec calls out (CreditChain Native vs Ethereum Compatible)
// rather than a single generic "Address found", so the search result
// preview says plainly which one matched before the visitor reads any
// further detail.
function addressResultTypeLabel(profile) {
    return profile && profile.lookedUpByEthAddress ? "Wallet found (Ethereum Compatible)" : "Wallet found (CreditChain Native)";
}

async function runSearch() {
    const input = document.getElementById("explorerSearchInput");
    // normalizeAddress strips an optional "cc" prefix - a no-op for
    // block/tx hash searches, but lets a pasted "cc..." address still
    // resolve, since the backend only ever indexed the raw form.
    const query = normalizeAddress(input.value.trim());
    const resultDiv = document.getElementById("explorerSearchResult");

    if (!query) return;

    try {
        const data = await apiGet(`/explorer/search?q=${encodeURIComponent(query)}`);
        searchActive = true;
        document.getElementById("explorerSearchClearBtn").style.display = "inline-block";

        if (!data.found) {
            resultDiv.innerHTML = `<div class="explorer-empty">No block, transaction, or address matches "${escapeHtml(query)}".</div>`;
            return;
        }

        const bannerText = data.type === "address" ? addressResultTypeLabel(data.address) : RESULT_TYPE_LABEL[data.type] || "Result found";
        const banner = `<div class="explorer-result-type">${bannerText}</div>`;

        if (data.type === "block") {
            resultDiv.innerHTML = banner + `<div class="block-chain">${renderBlockCube(data.block)}</div>`;
        } else if (data.type === "transactions") {
            const rows = data.transactions.map((tx) => renderTxRow(tx, true)).join("");
            resultDiv.innerHTML =
                banner + `<div class="explorer-table-scroll"><table class="explorer-table"><tbody>${rows}</tbody></table></div>`;
        } else if (data.type === "address") {
            resultDiv.innerHTML = banner + renderAddressProfile(data.address);
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

// A page can deep-link straight into a search (e.g. asset.html's
// "View on Explorer" link) via ?q=<address-or-hash> instead of making
// the user paste it in manually.
(function prefillSearchFromQueryString() {
    const q = new URLSearchParams(window.location.search).get("q");
    if (q) {
        document.getElementById("explorerSearchInput").value = q;
        runSearch();
    }
})();

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
