const wallet = requireWallet();

// The admin mint faucet card is testnet-only (see its own HTML doc
// comment) - hidden by default in markup, only shown here once we
// know which network is actually selected.
if (window.CC_NETWORK === "testnet") {
    const adminMintCard = document.getElementById("adminMintTestnetCard");
    if (adminMintCard) adminMintCard.style.display = "";
}

// Buyer-supplied free text (PaymentMethod) is rendered into innerHTML
// below - must be escaped, same discipline as any other untrusted
// user input reaching the DOM.
function escapeHtml(s) {
    const div = document.createElement("div");
    div.innerText = s;
    return div.innerHTML;
}

// adminToken lives in a page-scoped variable only - never
// localStorage/sessionStorage/cookies, same spirit as how this app
// already handles wallet private keys client-side. Lost on reload by
// design. Only used as a fallback on networks where no admin wallet
// (ADMIN_WALLET_ADDRESS) has been configured yet - see loadAdminWalletStatus.
let adminToken = null;

async function apiAdminGet(path) {
    const res = await fetch(API_BASE + path, {
        headers: { "X-Admin-Token": adminToken || "" },
    });
    if (!res.ok) {
        throw new Error((await res.text()) || `GET ${path} failed (${res.status})`);
    }
    return res.json();
}

async function apiAdminPost(path, body) {
    const res = await fetch(API_BASE + path, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
            "X-Admin-Token": adminToken || "",
        },
        body: JSON.stringify(body || {}),
    });
    if (!res.ok) {
        throw new Error((await res.text()) || `POST ${path} failed (${res.status})`);
    }
    return res.json();
}

function setAdminToken() {
    const value = document.getElementById("adminTokenInput").value.trim();
    if (!value) {
        document.getElementById("adminTokenStatus").innerText = "Enter a token first.";
        return;
    }
    adminToken = value;
    document.getElementById("adminTokenStatus").innerText = "Token set for this tab.";
    loadPurchases();
}

// loadAdminWalletStatus compares the wallet logged into this browser
// against the network's configured admin wallet (GET /admin/whoami is
// public - the address is inherently visible on-chain anyway once any
// admin transaction lands), so an operator gets a clear "you're logged
// in as the right/wrong wallet" signal before trying an action and
// hitting a confusing 403. Once multisig is configured, it supersedes
// the single-wallet check entirely - see requireAdminAuthed's own
// three-tier precedence (api/admin.go).
async function loadAdminWalletStatus() {
    document.getElementById("myWalletAddressDisplay").innerText = wallet ? wallet.address : "(not logged in)";
    try {
        const info = await apiGet("/admin/whoami");
        const configured = info.adminWalletAddress || "";
        document.getElementById("configuredAdminAddressDisplay").innerText = configured || "(not configured - legacy admin token mode)";

        const statusEl = document.getElementById("adminWalletMatchStatus");
        const multisigCard = document.getElementById("multisigCard");

        if (info.multisigEnabled) {
            const signers = info.multisigSigners || [];
            document.getElementById("multisigThresholdDisplay").innerText = info.multisigThreshold;
            document.getElementById("multisigSignerCountDisplay").innerText = signers.length;
            document.getElementById("multisigSignersDisplay").innerText = signers.join(", ");
            multisigCard.style.display = "";

            const isSigner = wallet && signers.includes(wallet.address);
            statusEl.innerText = isSigner
                ? "✓ This wallet is one of the configured multisig signers - it can propose and approve admin actions below."
                : "⚠ This wallet is NOT one of the configured multisig signers - admin actions will be rejected.";
        } else if (!configured) {
            multisigCard.style.display = "none";
            statusEl.innerText = "No admin wallet configured on this network yet - use the Admin Token below instead.";
        } else if (wallet && wallet.address === configured) {
            multisigCard.style.display = "none";
            statusEl.innerText = "✓ This wallet is the configured admin wallet - every action below will sign correctly.";
        } else {
            multisigCard.style.display = "none";
            statusEl.innerText = "⚠ This wallet is NOT the configured admin wallet - actions below will be rejected. Log in as the admin wallet instead.";
        }

        if (info.multisigEnabled) {
            loadMultisigProposals();
        }
    } catch (e) {
        console.error(e);
    }
}

// loadMultisigProposals is public (GET /admin/multisig/proposals) -
// every configured signer needs to see what's pending regardless of
// whether they're logged in yet, same reasoning as /admin/whoami.
async function loadMultisigProposals() {
    const div = document.getElementById("multisigProposalsList");
    try {
        const proposals = await apiGet("/admin/multisig/proposals");

        if (!proposals || proposals.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No pending proposals.</div>`;
            return;
        }

        div.innerHTML = proposals
            .map((p) => {
                const approved = wallet && p.Approvals && p.Approvals[wallet.address];
                const approvalCount = p.Approvals ? Object.keys(p.Approvals).length : 0;
                return `
            <div class="explorer-row" style="display:block;">
                <strong>${escapeHtml(p.ActionType)}</strong>
                <span style="opacity:0.7;font-size:12px;"> — proposed by ${p.Proposer ? p.Proposer.slice(0, 8) + "..." : "?"}</span>
                <p style="margin:4px 0;font-family:monospace;font-size:11px;word-break:break-all;">${escapeHtml(p.Payload || "")}</p>
                <span>${approvalCount} approval(s) so far</span>
                <button onclick="approveMultisigProposal('${p.ID}')" ${approved ? "disabled" : ""}>
                    ${approved ? "Already approved by you" : "Approve"}
                </button>
            </div>
        `;
            })
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<div class="explorer-empty">Could not load: ${e.message}</div>`;
    }
}

async function approveMultisigProposal(proposalId) {
    if (!confirm("Approve this proposal? If this is the final required approval, the underlying action executes once the next block confirms it.")) {
        return;
    }
    try {
        await apiPostSigned("/admin/multisig/approve", { proposalId });
        setTimeout(loadMultisigProposals, 9000);
    } catch (e) {
        alert("Could not approve: " + e.message);
    }
}

// loadTreasuryInfo is public - no admin token needed, matches
// HandleTreasuryInfo's own auth (none).
async function loadTreasuryInfo() {
    try {
        const info = await apiGet("/treasury/info");
        document.getElementById("treasuryAddressDisplay").innerText = info.foundationAddress || "(not set)";
        document.getElementById("salePriceDisplay").innerText = info.salePriceCentsPerCCT;
        document.getElementById("remainingAllocationDisplay").innerText = cctFromSmallest(info.remainingAllocation);
        document.getElementById("pausedDisplay").innerText = info.paused ? "PAUSED" : "ACTIVE";
    } catch (e) {
        console.error(e);
    }
}

// saveAWSCredentials submits the ETH custodial KMS signer's AWS
// credentials straight from this form to the server - the value
// never passes through me/the assistant, only through this page's
// own signed request. Fields are cleared immediately after a
// successful submit so the secret doesn't linger in the DOM/browser
// autofill any longer than necessary.
async function saveAWSCredentials() {
    const accessKeyId = document.getElementById("awsAccessKeyIdInput").value.trim();
    const secretAccessKey = document.getElementById("awsSecretAccessKeyInput").value.trim();
    const region = document.getElementById("awsRegionInput").value.trim();
    const statusEl = document.getElementById("awsCredentialsStatus");

    if (!accessKeyId || !secretAccessKey || !region) {
        statusEl.innerText = "All three fields are required.";
        return;
    }

    statusEl.innerText = "Saving...";
    try {
        const result = await apiPostSigned("/admin/aws-credentials/set", {
            accessKeyId,
            secretAccessKey,
            region,
        });
        document.getElementById("awsAccessKeyIdInput").value = "";
        document.getElementById("awsSecretAccessKeyInput").value = "";
        document.getElementById("awsRegionInput").value = "";
        statusEl.innerText = result.note || "Saved on the server - restart the service to activate.";
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

async function saveTreasuryAddress() {
    const foundationAddress = document.getElementById("treasuryAddressInput").value.trim();
    const statusEl = document.getElementById("treasuryAddressStatus");
    if (!foundationAddress) {
        statusEl.innerText = "Enter an address first.";
        return;
    }
    try {
        const result = await apiPostSigned("/admin/treasury/address", { foundationAddress });
        statusEl.innerText = `Saved (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(loadTreasuryInfo, 9000);
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

async function saveSalePrice() {
    const salePriceCentsPerCCT = parseInt(document.getElementById("salePriceInput").value, 10);
    const statusEl = document.getElementById("salePriceStatus");
    if (isNaN(salePriceCentsPerCCT) || salePriceCentsPerCCT < 0) {
        statusEl.innerText = "Enter a valid price.";
        return;
    }
    try {
        const result = await apiPostSigned("/admin/treasury/sale-price", { salePriceCentsPerCCT });
        statusEl.innerText = `Saved (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(loadTreasuryInfo, 9000);
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

async function togglePaused() {
    const statusEl = document.getElementById("pausedStatus");
    try {
        const info = await apiGet("/treasury/info");
        const result = await apiPostSigned("/admin/treasury/paused", { paused: !info.paused });
        statusEl.innerText = `Saved (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(loadTreasuryInfo, 9000);
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

const PURCHASE_METHOD_LABELS = {
    BANK_TRANSFER: "Bank transfer",
    CRYPTO_TRANSFER: "Crypto transfer",
    ONLINE_WALLET: "Online wallet",
    CARD: "Bank card (email follow-up)",
    APPLE_PAY: "Apple Pay (email follow-up)",
    GOOGLE_PAY: "Google Pay (email follow-up)",
};

const PURCHASE_STATUS_LABELS = {
    AWAITING_PAYMENT: "Awaiting instructions",
    INSTRUCTIONS_SENT: "Instructions sent - waiting on buyer",
    BUYER_MARKED_PAID: "Buyer says they've paid - verify and confirm",
};

async function loadPurchases() {
    const div = document.getElementById("purchaseList");
    try {
        const purchases = await apiPostSigned("/admin/treasury/purchases", {});
        const pending = (purchases || []).filter((p) => p.Status !== "CONFIRMED");

        if (pending.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No pending purchase requests.</div>`;
            return;
        }

        div.innerHTML = pending
            .map((p) => {
                // A bank-transfer request naming a Currency already had
                // its preset account shown to the buyer immediately (see
                // buy-cct.js's showBankAccountDetails) - "Send
                // Instructions" below is still there as a fallback/extra-
                // notes channel, not something this flow strictly needs.
                const instructionsBox =
                    p.Status === "AWAITING_PAYMENT"
                        ? `
                <div class="explorer-search" style="margin:6px 0;">
                    <input id="purchaseInstructions_${p.ID}" type="text" placeholder="Bank account / crypto address / wallet handle to give the buyer">
                    <button onclick="sendPurchaseInstructions('${p.ID}')">Send Instructions</button>
                </div>`
                        : `
                <div style="margin:6px 0;font-size:13px;">
                    <strong>Instructions sent:</strong> ${escapeHtml(p.AdminInstructions || "")}
                </div>`;
                const contactLine = (p.BuyerName || p.BuyerContact || p.Currency)
                    ? `
                <div style="margin:6px 0;font-size:13px;">
                    ${p.BuyerName ? `<strong>Name:</strong> ${escapeHtml(p.BuyerName)} &mdash; ` : ""}
                    ${p.BuyerContact ? `<strong>Contact:</strong> ${escapeHtml(p.BuyerContact)} &mdash; ` : ""}
                    ${p.Currency ? `<strong>Currency:</strong> ${escapeHtml(p.Currency)}` : ""}
                </div>`
                    : "";

                return `
            <div class="explorer-row" style="display:block;">
                <div>
                    <span>${p.BuyerAddress.slice(0, 8)}...${p.BuyerAddress.slice(-6)}</span>
                    &mdash;
                    <span>${p.CCTAmount} CCT</span>
                    &mdash;
                    <span>${p.PriceAtRequest} cents/CCT</span>
                    &mdash;
                    <span>requested ${new Date(p.CreatedAt * 1000).toLocaleString()}</span>
                </div>
                <div style="margin:6px 0;font-size:13px;">
                    <strong>Method:</strong> ${PURCHASE_METHOD_LABELS[p.Method] || p.Method || "(none given)"}
                    &mdash;
                    <strong>Email:</strong> ${escapeHtml(p.BuyerEmail || "(none given)")}
                    &mdash;
                    <strong>Status:</strong> ${PURCHASE_STATUS_LABELS[p.Status] || p.Status}
                </div>
                ${contactLine}
                ${instructionsBox}
                <button onclick="confirmPurchase('${p.ID}')">Confirm - I've received this payment</button>
            </div>
        `;
            })
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<div class="explorer-empty">Could not load: ${e.message}</div>`;
    }
}

async function sendPurchaseInstructions(purchaseId) {
    const input = document.getElementById(`purchaseInstructions_${purchaseId}`);
    const instructions = input.value.trim();
    if (!instructions) {
        alert("Enter the payment instructions to send first.");
        return;
    }
    try {
        await apiPostSigned("/admin/treasury/purchase/send-instructions", { purchaseId, instructions });
        setTimeout(loadPurchases, 9000);
    } catch (e) {
        alert("Could not send instructions: " + e.message);
    }
}

async function confirmPurchase(purchaseId) {
    if (!confirm("Confirm this purchase? This transfers CCT from the treasury to the buyer once the next block confirms it.")) {
        return;
    }
    try {
        await apiPostSigned("/admin/treasury/purchase/confirm", { purchaseId });
        setTimeout(() => {
            loadPurchases();
            loadTreasuryInfo();
        }, 9000);
    } catch (e) {
        alert("Could not confirm: " + e.message);
    }
}

// loadBankAccountsAdmin is a public, unauthenticated read (same
// endpoint buy-cct.html's currency picker uses - see
// api/treasury.go's HandleListBankAccounts doc comment for why there's
// no confidentiality value here), so it loads even before an admin
// wallet/token is set.
async function loadBankAccountsAdmin() {
    const div = document.getElementById("bankAccountsList");
    try {
        const accounts = await apiGet("/treasury/bank-accounts");
        if (!accounts || accounts.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No currencies configured yet.</div>`;
            return;
        }
        div.innerHTML = accounts
            .map(
                (a) => `
            <div class="explorer-row" style="display:block;">
                <strong>${escapeHtml(a.currency)}</strong>
                &mdash; ${escapeHtml(a.accountName)} &mdash; ${escapeHtml(a.accountNumber)} &mdash; ${escapeHtml(a.bankName)}
                ${a.routingCode ? ` &mdash; ${escapeHtml(a.routingCode)}` : ""}
                ${a.notes ? `<div style="opacity:0.7;font-size:12px;margin-top:2px;">${escapeHtml(a.notes)}</div>` : ""}
            </div>
        `
            )
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<div class="explorer-empty">Could not load: ${e.message}</div>`;
    }
}

async function submitBankAccount() {
    const statusEl = document.getElementById("bankAccountStatus");
    const currency = document.getElementById("bankAccountCurrencyInput").value.trim().toUpperCase();
    const accountName = document.getElementById("bankAccountNameInput").value.trim();
    const accountNumber = document.getElementById("bankAccountNumberInput").value.trim();
    const bankName = document.getElementById("bankAccountBankNameInput").value.trim();
    const routingCode = document.getElementById("bankAccountRoutingInput").value.trim();
    const notes = document.getElementById("bankAccountNotesInput").value.trim();

    if (!currency || !accountName || !accountNumber || !bankName) {
        statusEl.innerText = "Currency, account name, account number, and bank name are all required.";
        return;
    }
    if (!confirm(`Save the ${currency} bank account? This replaces any existing ${currency} details entirely - real buyers may already be looking at the current ones.`)) {
        return;
    }

    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/admin/treasury/bank-account", { currency, accountName, accountNumber, bankName, routingCode, notes });
        statusEl.innerText = `Submitted (tx ${result.txId}). If multisig is enabled, other signers need to approve it above before it takes effect.`;
        setTimeout(loadBankAccountsAdmin, 9000);
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

// loadAnnouncements is public - no admin token needed, matches
// HandleListAnnouncements' own auth (none). Posting still requires
// admin auth (postAnnouncement below).
async function loadAnnouncements() {
    const div = document.getElementById("announcementList");
    try {
        const announcements = await apiGet("/announcements/recent");

        if (!announcements || announcements.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No announcements posted yet.</div>`;
            return;
        }

        div.innerHTML = announcements
            .map(
                (a) => `
            <div class="explorer-row" style="display:block;">
                <strong>${escapeHtml(a.Title)}</strong>
                <p style="margin:4px 0;">${escapeHtmlMultiline(a.Text)}</p>
                <span style="opacity:0.6;">${new Date(a.CreatedAt * 1000).toLocaleString()}</span>
            </div>
        `
            )
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<div class="explorer-empty">Could not load: ${e.message}</div>`;
    }
}

async function postAnnouncement() {
    const title = document.getElementById("announcementTitleInput").value.trim();
    const text = document.getElementById("announcementTextInput").value.trim();
    const statusEl = document.getElementById("announcementPostStatus");

    if (!title || !text) {
        statusEl.innerText = "Enter both a title and text.";
        return;
    }

    try {
        const result = await apiPostSigned("/announcements/create", { title, text });
        statusEl.innerText = `Posted (tx ${result.txId}) - takes effect once the next block confirms it.`;
        document.getElementById("announcementTitleInput").value = "";
        document.getElementById("announcementTextInput").value = "";
        setTimeout(loadAnnouncements, 9000);
    } catch (e) {
        statusEl.innerText = "Could not post: " + e.message;
    }
}

async function allocateGenesisPools() {
    const statusEl = document.getElementById("genesisPoolAllocateStatus");
    const treasuryAddr = document.getElementById("treasuryAddressDisplay").innerText;

    if (!confirm(
        `This mints 1,000,000,000 CCT total in one irreversible action - 250,000,000 CCT lands directly at the ` +
        `current Foundation Treasury address (${treasuryAddr}), the rest fills the other 4 genesis pools. ` +
        `This can only ever run once. Continue?`
    )) {
        return;
    }

    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/admin/genesis-pools/allocate", {});
        statusEl.innerText = `Submitted (tx ${result.txId}). If multisig is enabled, other signers need to approve it above before it takes effect.`;
        setTimeout(() => {
            loadTreasuryInfo();
            loadAdminWalletStatus();
        }, 9000);
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

async function withdrawGenesisPool() {
    const pool = document.getElementById("genesisPoolSelect").value;
    const to = document.getElementById("genesisPoolToInput").value.trim();
    const amount = parseInt(document.getElementById("genesisPoolAmountInput").value, 10);
    const statusEl = document.getElementById("genesisPoolWithdrawStatus");

    if (!to) {
        statusEl.innerText = "Enter a destination address.";
        return;
    }
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    if (!confirm(`Withdraw ${amount} CCT from ${pool} to ${to}? Takes effect once the next block confirms it.`)) {
        return;
    }

    try {
        const result = await apiPostSigned("/admin/genesis-pools/withdraw", { pool, to, amount });
        statusEl.innerText = `Submitted (tx ${result.txId}) - takes effect once the next block confirms it.`;
        document.getElementById("genesisPoolToInput").value = "";
        document.getElementById("genesisPoolAmountInput").value = "";
    } catch (e) {
        statusEl.innerText = "Could not withdraw: " + e.message;
    }
}

async function adminMintTestnetCct() {
    const statusEl = document.getElementById("adminMintStatus");

    // Belt-and-suspenders alongside the card's own display:none default -
    // refuses even if the network switched after the page loaded (e.g. a
    // second tab flipped it), since HandleTokenMint itself has no
    // mainnet/testnet gate of its own (see api/token_mint.go).
    if (window.CC_NETWORK !== "testnet") {
        statusEl.innerText = "Switch to Testnet before minting - this must never be used on Mainnet.";
        return;
    }

    const to = document.getElementById("adminMintToInput").value.trim();
    const amount = parseInt(document.getElementById("adminMintAmountInput").value, 10);

    if (!to) {
        statusEl.innerText = "Enter a destination address.";
        return;
    }
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    if (!confirm(`Mint ${amount} testnet CCT to ${to}?`)) {
        return;
    }

    try {
        const result = await apiPostSigned("/token/mint", { address: to, amount: amount });
        statusEl.innerText = `Submitted (tx ${result.txId}) - takes effect once the next block confirms it.`;
        document.getElementById("adminMintToInput").value = "";
        document.getElementById("adminMintAmountInput").value = "";
    } catch (e) {
        statusEl.innerText = "Could not mint: " + e.message;
    }
}

async function submitAdminTransfer() {
    const from = document.getElementById("adminTransferFromInput").value.trim();
    const to = document.getElementById("adminTransferToInput").value.trim();
    const symbol = document.getElementById("adminTransferSymbolInput").value.trim() || "CCT";
    const amountRaw = document.getElementById("adminTransferAmountInput").value.trim();
    const statusEl = document.getElementById("adminTransferStatus");

    if (!from || !to) {
        statusEl.innerText = "Enter both a from and to address.";
        return;
    }
    // amount stays a plain (non-BigInt) integer here, same convention
    // every other admin amount field on this page already uses - fine
    // up to 2^53, which every real correction this tool exists for
    // (a single misrouted transfer, never a whole balance at CCT-supply
    // scale) comfortably stays under.
    const amount = parseInt(amountRaw, 10);
    if (!amountRaw || isNaN(amount) || amount <= 0 || String(amount) !== amountRaw) {
        statusEl.innerText = "Enter a valid whole-number amount, in the symbol's smallest unit.";
        return;
    }

    if (!confirm(
        `Move ${amount} ${symbol} (smallest unit) from ${from} to ${to}? This does not need ${from}'s own ` +
        `signature - double check both addresses are correct before continuing.`
    )) {
        return;
    }

    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/admin/token/transfer", { from, to, amount, symbol });
        statusEl.innerText = `Submitted (tx ${result.txId}). If multisig is enabled, other signers need to approve it above before it takes effect.`;
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

// submitNormalizeCcc1Balance - see admin_normalize_ccc1_balance.go's own
// doc comment for the full incident this exists to fix. legacyAddress is
// sent completely untouched (no client-side normalization/trim-casing) -
// the whole point is reaching the exact wrong-case key as it's actually
// stored server-side.
async function submitNormalizeCcc1Balance() {
    const legacyAddress = document.getElementById("normalizeCcc1LegacyAddressInput").value.trim();
    const symbol = document.getElementById("normalizeCcc1SymbolInput").value.trim();
    const statusEl = document.getElementById("normalizeCcc1Status");

    if (!legacyAddress) {
        statusEl.innerText = "Enter the legacy address exactly as it's stored.";
        return;
    }
    if (symbol !== "ETH_CCC1" && symbol !== "USDC_CCC1") {
        statusEl.innerText = "Symbol must be ETH_CCC1 or USDC_CCC1.";
        return;
    }

    if (!confirm(`Move ${symbol}'s balance at the exact key "${legacyAddress}" to its normalized (lowercase) form? Double check this is really the wrong-case address before continuing.`)) {
        return;
    }

    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/admin/token/normalize-ccc1-balance", { legacyAddress, symbol });
        statusEl.innerText = `Submitted (tx ${result.txId}). If multisig is enabled, other signers need to approve it above before it takes effect.`;
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

// submitRefundStuckVaultBurn - see admin_refund_stuck_vault_burn.go's
// own doc comment for the full incident this exists to fix.
async function submitRefundStuckVaultBurn() {
    const burnTxId = document.getElementById("refundStuckVaultBurnTxIdInput").value.trim();
    const statusEl = document.getElementById("refundStuckVaultBurnStatus");

    if (!burnTxId) {
        statusEl.innerText = "Enter the burn transaction ID first.";
        return;
    }
    if (!confirm(`Refund the full original burned amount for burn transaction "${burnTxId}" back to its own burner? This only works on a still-PENDING approval - double check it hasn't already released.`)) {
        return;
    }

    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/admin/vault/refund-stuck-burn", { burnTxId });
        statusEl.innerText = `Submitted (tx ${result.txId}). If multisig is enabled, other signers need to approve it above before it takes effect.`;
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

// sweepSigningKeyEth triggers HandleSweepVaultReleaseSigningKeyEth - a
// real, synchronous on-chain broadcast (not a queued CreditChain
// transaction), so the response carries a real txHash immediately
// rather than a pending txId.
async function sweepSigningKeyEth() {
    const statusEl = document.getElementById("sweepSigningKeyStatus");
    if (!confirm("Sweep any ETH sitting at the vault signing key's address to the relayer address? This is a real, irreversible on-chain transaction.")) {
        return;
    }
    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/admin/vault/sweep-signing-key-eth", {});
        statusEl.innerText = `Swept ${result.amountWei} wei from ${result.from} to ${result.to} - tx ${result.txHash}.`;
    } catch (e) {
        statusEl.innerText = "Could not sweep: " + e.message;
    }
}

async function runTokenomicsDecimalMigration() {
    const statusEl = document.getElementById("tokenomicsMigrationStatus");
    if (!confirm("Run the one-time CCT decimal migration now? This converts every CCT balance on the whole network and cannot be undone. Only run this once, right after every node is on the new binary.")) {
        return;
    }
    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/admin/tokenomics/migrate-decimals", {});
        statusEl.innerText = `Submitted (tx ${result.txId}) - takes effect once the next block confirms it. Balances will show correctly a few seconds after that.`;
    } catch (e) {
        statusEl.innerText = "Could not run migration: " + e.message;
    }
}

// The two USD-rate keys that back the account-holdings cap - kept as a
// named set so the auto-fill/staleness logic below can single them out
// from the other (non-price) governance keys in the same dropdown.
const GOV_PARAM_PRICE_KEYS = { EthUsdCentsPerEth: "ethUsd", BtcUsdCentsPerBtc: "btcUsd" };

// lastKnownGovParams/lastKnownLivePrice are cached from their own
// loaders below so onGovParamKeyChange (fired on every dropdown change,
// independent of the 10s poll cycle) can react instantly without
// re-fetching both endpoints itself.
let lastKnownGovParams = {};
let lastKnownLivePrice = null;

// loadGovernanceParams is public (GET /admin/params) - shows every
// currently-set value for the keys this page's dropdown offers, so a
// signer can see what they'd be changing before submitting. A key with
// no entry yet has never been set (falls back to its hardcoded default
// server-side, per GovernanceParamsStore.GetOrDefault) - shown as
// "not set (default applies)" rather than a misleading 0.
async function loadGovernanceParams() {
    const el = document.getElementById("govParamCurrentValues");
    try {
        const params = await apiGet("/admin/params");
        lastKnownGovParams = params;
        const keys = ["EthUsdCentsPerEth", "BtcUsdCentsPerBtc", "CctUsdCents", "AccountHoldingsCapUsdCents", "UnbondingPeriodSeconds", "DormancyPeriodSeconds", "ModeratorRegisterFeeCCT", "ModeratorStakeRequirementCCT", "EthCcc1TransferFeeUsdCents", "UsdcCcc1TransferFeeUsdCents", "BtcCcc1TransferFeeUsdCents"];
        el.innerText = keys
            .map((k) => `${k}=${Object.prototype.hasOwnProperty.call(params, k) ? params[k] : "not set (default applies)"}`)
            .join(", ");
        renderGovParamStaleness();
    } catch (e) {
        el.innerText = "Could not load: " + e.message;
    }
}

// loadLivePriceForGovParams is public (GET /price/external) - the same
// live CoinGecko-backed feed the site already polls every 60s
// server-side. Used ONLY to prefill the submit form and show staleness
// - never read directly inside consensus-critical apply code (see
// api/account_holdings_cap.go's own doc comment on why: each node
// polls this independently, so two nodes could momentarily disagree -
// the governance param a signer explicitly submits and gets approved
// is what every node actually applies identically).
async function loadLivePriceForGovParams() {
    try {
        lastKnownLivePrice = await apiGet("/price/external");
        renderGovParamStaleness();
        onGovParamKeyChange();
    } catch (e) {
        console.error(e);
    }
}

// onGovParamKeyChange auto-fills the value field with the current live
// price (converted to cents) whenever a price-backed key is selected -
// a signer no longer needs to look up a price and do cents math by
// hand, just review the prefilled number and click Submit. Left blank
// (not auto-filled) for the non-price keys, where there's no
// "current market value" to suggest.
function onGovParamKeyChange() {
    const key = document.getElementById("govParamKeySelect").value;
    const priceField = GOV_PARAM_PRICE_KEYS[key];
    const input = document.getElementById("govParamValueInput");
    if (priceField && lastKnownLivePrice && lastKnownLivePrice[priceField] > 0) {
        input.value = Math.round(lastKnownLivePrice[priceField] * 100);
    }
    renderGovParamStaleness();
}

// renderGovParamStaleness shows how far the CURRENTLY-SET on-chain rate
// has drifted from the live market price right now, in percent - the
// concrete "should someone refresh this?" signal a signer needs
// without doing the math themselves. Only meaningful for the two price
// keys and only once both the on-chain value and a live price are
// known.
function renderGovParamStaleness() {
    const staleEl = document.getElementById("govParamStaleness");
    if (!staleEl) return;
    const key = document.getElementById("govParamKeySelect").value;
    const priceField = GOV_PARAM_PRICE_KEYS[key];
    if (!priceField) {
        staleEl.innerText = "";
        return;
    }
    const onChainCents = lastKnownGovParams[key];
    if (!onChainCents || !lastKnownLivePrice || !(lastKnownLivePrice[priceField] > 0)) {
        staleEl.innerText = "";
        return;
    }
    const liveCents = lastKnownLivePrice[priceField] * 100;
    const driftPct = ((liveCents - onChainCents) / onChainCents) * 100;
    const driftAbs = Math.abs(driftPct).toFixed(1);
    if (Math.abs(driftPct) < 1) {
        staleEl.innerText = `On-chain rate is within 1% of the live price (${driftPct >= 0 ? "+" : ""}${driftPct.toFixed(1)}%) - no refresh needed.`;
        staleEl.style.color = "";
    } else {
        staleEl.innerText = `⚠ On-chain rate is ${driftAbs}% ${driftPct >= 0 ? "below" : "above"} the live price - consider refreshing (value field is prefilled with the current live price).`;
        staleEl.style.color = "#ffb020";
    }
}

async function setGovernanceParam() {
    const key = document.getElementById("govParamKeySelect").value;
    const valueStr = document.getElementById("govParamValueInput").value.trim();
    const statusEl = document.getElementById("govParamStatus");
    const value = parseInt(valueStr, 10);
    if (valueStr === "" || isNaN(value) || value < 0) {
        statusEl.innerText = "Enter a valid non-negative integer value.";
        return;
    }
    if (!confirm(`Set ${key} = ${value}? If multisig is enabled this creates a proposal that still needs approvals; otherwise it takes effect once the next block confirms it.`)) {
        return;
    }
    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/admin/params/set", { key, value });
        statusEl.innerText = `Submitted (tx ${result.txId}). If multisig is enabled, other signers need to approve it above before it takes effect.`;
        setTimeout(() => {
            loadGovernanceParams();
            loadAdminWalletStatus();
        }, 9000);
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

// loadSmtpStatus shows whether email is live and, if so, via which
// source - env vars always win over the admin-page config when both
// are set (see effectiveSmtpConfig, api/email.go), so a stale/wrong
// admin-page entry doesn't look like it's silently doing nothing.
// Never pre-fills the password field - HandleAdminGetSmtpStatus never
// sends it back, matching every other secret-accepting form here.
async function loadSmtpStatus() {
    const statusEl = document.getElementById("smtpStatusDisplay");
    try {
        const info = await apiPostSigned("/admin/smtp/status", {});
        if (info.envConfigured) {
            statusEl.innerText = `Live via environment variables (SMTP_HOST=${info.host || "?"}) - this takes priority over the form below.`;
        } else if (info.adminConfigured) {
            statusEl.innerText = `Live via this form: sending as ${info.username} through ${info.host}:${info.port}.`;
            document.getElementById("smtpHostInput").value = info.host || "";
            document.getElementById("smtpPortInput").value = info.port || "";
            document.getElementById("smtpUsernameInput").value = info.username || "";
            document.getElementById("smtpFromInput").value = info.from || "";
            document.getElementById("smtpNotifyRecipientInput").value = info.notifyRecipient || "";
        } else {
            statusEl.innerText = "Not configured yet - emails (payment order confirmations, etc.) will not send.";
        }
    } catch (e) {
        statusEl.innerText = "Could not load status: " + e.message;
    }
}

async function saveSmtpConfig() {
    const statusEl = document.getElementById("smtpConfigStatus");
    const host = document.getElementById("smtpHostInput").value.trim();
    const port = document.getElementById("smtpPortInput").value.trim();
    const username = document.getElementById("smtpUsernameInput").value.trim();
    const password = document.getElementById("smtpPasswordInput").value;
    const from = document.getElementById("smtpFromInput").value.trim();
    const notifyRecipient = document.getElementById("smtpNotifyRecipientInput").value.trim();

    if (!host || !username || !password || !from) {
        statusEl.innerText = "Host, username, password, and from address are all required.";
        return;
    }

    statusEl.innerText = "Saving...";
    try {
        await apiPostSigned("/admin/smtp/set", { host, port, username, password, from, notifyRecipient });
        document.getElementById("smtpPasswordInput").value = "";
        statusEl.innerText = "Saved.";
        loadSmtpStatus();
    } catch (e) {
        statusEl.innerText = "Could not save: " + e.message;
    }
}

// loadValidatorJoinRequests/markValidatorJoinRequestHandled - see
// website/validator.html's "Request to Join" form (submits) and
// api/validator_join_request.go (backend). Same apiPostSigned-with-
// empty-payload pattern loadPurchases above already uses for a
// signed-wallet-gated listing read.
async function loadValidatorJoinRequests() {
    const div = document.getElementById("validatorJoinRequestsList");
    try {
        const requests = await apiPostSigned("/validator/join-requests", {});
        const pending = (requests || []).filter((r) => r.status !== "HANDLED");

        if (pending.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No pending join requests.</div>`;
            return;
        }

        div.innerHTML = pending
            .map(
                (r) => `
            <div class="explorer-row" style="display:block;">
                <div>
                    <span style="font-family:monospace;">${r.address.slice(0, 8)}...${r.address.slice(-6)}</span>
                    &mdash;
                    <span>requested ${new Date(r.createdAt * 1000).toLocaleString()}</span>
                </div>
                <div style="margin:6px 0;font-size:13px;">
                    <strong>Node URL:</strong> <span style="font-family:monospace;">${escapeHtml(r.nodeUrl)}</span>
                </div>
                ${r.note ? `<div style="margin:6px 0;font-size:13px;"><strong>Note:</strong> ${escapeHtml(r.note)}</div>` : ""}
                <button onclick="markValidatorJoinRequestHandled('${r.id}')">Mark Handled</button>
            </div>
        `
            )
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<div class="explorer-empty">Could not load: ${e.message}</div>`;
    }
}

async function markValidatorJoinRequestHandled(id) {
    try {
        await apiPostSigned("/validator/join-request/mark-handled", { id });
        loadValidatorJoinRequests();
    } catch (e) {
        alert("Could not update: " + e.message);
    }
}

loadAdminWalletStatus();
loadTreasuryInfo();
loadPurchases();
loadAnnouncements();
loadGovernanceParams();
loadLivePriceForGovParams();
loadSmtpStatus();
loadValidatorJoinRequests();
loadBankAccountsAdmin();

setInterval(() => {
    loadTreasuryInfo();
    loadPurchases();
    loadAnnouncements();
    loadAdminWalletStatus();
    loadGovernanceParams();
    loadLivePriceForGovParams();
    loadValidatorJoinRequests();
}, 10000);
