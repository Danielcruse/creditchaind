// buy-cct.js - keypad amount entry -> payment method selection ->
// method-specific contact form -> (bank transfer only) an admin-preset
// account shown immediately, no per-request admin action needed - see
// core.TreasuryStore.SetBankAccount's doc comment. Every non-bank
// method (CARD/ONLINE_WALLET/APPLE_PAY/GOOGLE_PAY) collects ONLY
// contact info, never card details - see core.PurchaseMethod's doc
// comment for why: a raw card-number form would be a PCI-DSS
// violation, so those methods all end the same way, an admin following
// up by email with a real, compliant payment link.

function escapeHtmlBuy(s) {
    const div = document.createElement("div");
    div.innerText = s == null ? "" : String(s);
    return div.innerHTML;
}

// --- sale info (price/allocation/paused) -----------------------------

let salePriceCentsPerCCT = 0;

async function loadSaleInfo() {
    try {
        const info = await apiGet("/treasury/info");
        salePriceCentsPerCCT = info.salePriceCentsPerCCT || 0;
        document.getElementById("salePrice").innerText = salePriceCentsPerCCT
            ? `$${(salePriceCentsPerCCT / 100).toFixed(2)} / CCT`
            : "Not set yet";
        document.getElementById("saleRemaining").innerText = `${info.remainingAllocation} CCT`;
        document.getElementById("salePausedNotice").style.display = info.paused ? "block" : "none";
        updateAmountPreview();
    } catch (e) {
        console.error(e);
    }
}

// --- step 1: keypad amount entry --------------------------------------

let amountBuffer = "";
let amountUnit = "CCT"; // or "USD"
let pendingCctAmount = 0;

function keypadPress(key) {
    if (key === "back") {
        amountBuffer = amountBuffer.slice(0, -1);
    } else if (key === ".") {
        if (!amountBuffer.includes(".")) amountBuffer += ".";
    } else {
        amountBuffer = amountBuffer === "0" ? key : amountBuffer + key;
    }
    updateAmountDisplay();
}

function updateAmountDisplay() {
    document.getElementById("amountDisplay").innerText = amountBuffer || "0";
    updateAmountPreview();
}

function updateAmountPreview() {
    const previewEl = document.getElementById("amountPreview");
    const num = parseFloat(amountBuffer);
    if (!amountBuffer || isNaN(num) || num <= 0 || !salePriceCentsPerCCT) {
        previewEl.innerText = "";
        return;
    }
    if (amountUnit === "CCT") {
        const usd = (num * salePriceCentsPerCCT) / 100;
        previewEl.innerText = `≈ $${usd.toLocaleString(undefined, { maximumFractionDigits: 2 })}`;
    } else {
        const cct = num / (salePriceCentsPerCCT / 100);
        previewEl.innerText = `≈ ${cct.toLocaleString(undefined, { maximumFractionDigits: 2 })} CCT`;
    }
}

// toggleAmountUnit converts whatever's typed so far using the sale's
// own price (not a live market feed - CCT has no external price, its
// only defined price IS core.Treasury.SalePriceCentsPerCCT), same
// value-preserving-swap UX as vault-withdraw.html's fiat/token toggle.
function toggleAmountUnit() {
    const num = parseFloat(amountBuffer);
    if (!isNaN(num) && num > 0 && salePriceCentsPerCCT) {
        amountBuffer = amountUnit === "CCT"
            ? ((num * salePriceCentsPerCCT) / 100).toFixed(2)
            : String(Math.round(num / (salePriceCentsPerCCT / 100)));
    }
    amountUnit = amountUnit === "CCT" ? "USD" : "CCT";
    document.getElementById("amountUnitToggleLabel").innerText = amountUnit;
    updateAmountDisplay();
}

// currentCctAmount is always a whole CCT count (core's CCTAmount field
// is a plain uint64, unchanged by this redesign) - a USD entry rounds
// to the nearest whole CCT, a CCT entry floors away any stray decimal
// typed despite CCT amounts being whole units.
function currentCctAmount() {
    const num = parseFloat(amountBuffer);
    if (isNaN(num) || num <= 0) return 0;
    if (amountUnit === "CCT") return Math.floor(num);
    if (!salePriceCentsPerCCT) return 0;
    return Math.round(num / (salePriceCentsPerCCT / 100));
}

function continueFromAmount() {
    const statusEl = document.getElementById("amountStatus");
    const cctAmount = currentCctAmount();
    if (cctAmount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    pendingCctAmount = cctAmount;
    statusEl.innerText = "";
    document.getElementById("stepAmount").style.display = "none";
    document.getElementById("stepMethod").style.display = "block";
}

function backToAmount() {
    document.getElementById("stepMethod").style.display = "none";
    document.getElementById("stepAmount").style.display = "block";
}

// --- step 2: payment method selection -----------------------------------

let selectedMethod = null;

const OTHER_METHOD_EXPLAINER = {
    CARD: "We'll email you a secure payment link to complete your card payment - no card details are ever collected on this site.",
    ONLINE_WALLET: "We'll email you instructions for paying via your online wallet.",
    APPLE_PAY: "We'll email you a secure Apple Pay payment link.",
    GOOGLE_PAY: "We'll email you a secure Google Pay payment link.",
};

function selectMethod(method) {
    selectedMethod = method;
    document.getElementById("stepMethod").style.display = "none";
    if (method === "BANK_TRANSFER") {
        document.getElementById("stepBankForm").style.display = "block";
        loadBankCurrencies();
    } else {
        document.getElementById("otherFormExplainer").innerText = OTHER_METHOD_EXPLAINER[method] || "";
        document.getElementById("stepOtherForm").style.display = "block";
    }
}

function backToMethod() {
    document.getElementById("stepBankForm").style.display = "none";
    document.getElementById("stepOtherForm").style.display = "none";
    document.getElementById("stepMethod").style.display = "block";
}

// --- step 3a: bank transfer -----------------------------------------------

let bankAccountsByCurrency = {};
let pendingPurchaseId = null;

async function loadBankCurrencies() {
    const select = document.getElementById("bankCurrency");
    select.innerHTML = `<option value="">Loading currencies...</option>`;
    try {
        const accounts = await apiGet("/treasury/bank-accounts");
        bankAccountsByCurrency = {};
        (accounts || []).forEach((a) => { bankAccountsByCurrency[a.currency] = a; });
        const currencies = Object.keys(bankAccountsByCurrency);
        select.innerHTML = currencies.length === 0
            ? `<option value="">No currencies available yet - try another payment method</option>`
            : currencies.map((c) => `<option value="${escapeHtmlBuy(c)}">${escapeHtmlBuy(c)}</option>`).join("");
    } catch (e) {
        select.innerHTML = `<option value="">Couldn't load currencies</option>`;
    }
}

async function submitBankTransferForm() {
    const statusEl = document.getElementById("bankFormStatus");
    const name = document.getElementById("bankName").value.trim();
    const email = document.getElementById("bankEmail").value.trim();
    const contact = document.getElementById("bankContact").value.trim();
    const currency = document.getElementById("bankCurrency").value;

    if (!name) { statusEl.innerText = "Enter your full name."; return; }
    if (!email || !email.includes("@")) { statusEl.innerText = "Enter a valid email."; return; }
    if (!currency) { statusEl.innerText = "Select a currency."; return; }

    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/treasury/purchase/create", {
            cctAmount: pendingCctAmount, method: "BANK_TRANSFER",
            buyerEmail: email, buyerName: name, buyerContact: contact, currency,
        });
        pendingPurchaseId = result.txId; // the purchase's own ID always equals its creating tx's ID
        statusEl.innerText = "";
        showBankAccountDetails(currency, name);
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

function showBankAccountDetails(currency, name) {
    const account = bankAccountsByCurrency[currency];
    const container = document.getElementById("bankAccountDetails");
    if (!account) {
        container.innerHTML = `<p>Couldn't load account details for ${escapeHtmlBuy(currency)}.</p>`;
    } else {
        const rows = [
            ["Currency", account.currency],
            ["Account Name", account.accountName],
            ["Account Number", account.accountNumber],
            ["Bank Name", account.bankName],
        ];
        if (account.routingCode) rows.push(["Routing / SWIFT", account.routingCode]);
        container.innerHTML = rows
            .map(([label, value]) => `<div class="bank-account-row"><span>${label}</span><span>${escapeHtmlBuy(value)}</span></div>`)
            .join("") + (account.notes ? `<p style="font-size:12px;opacity:0.7;margin-top:8px;">${escapeHtmlBuy(account.notes)}</p>` : "");
    }
    document.getElementById("bankAccountWarning").innerText =
        `Pay from an account in the name "${name}" - a mismatched sender name may delay confirmation.`;
    document.getElementById("stepBankForm").style.display = "none";
    document.getElementById("stepBankAccount").style.display = "block";
}

// submitMarkPaidWithRetry retries for a bit before giving up - the
// purchase request just submitted is still PENDING at this point (not
// confirmed into a block yet), so an immediate mark-paid call would
// find no matching purchase record server-side. A few seconds' worth
// of retries covers normal block-confirmation latency without the
// buyer needing to know any of this is happening.
async function submitMarkPaidWithRetry() {
    const maxAttempts = 8;
    for (let i = 0; i < maxAttempts; i++) {
        try {
            return await apiPostSigned("/treasury/purchase/mark-paid", { purchaseId: pendingPurchaseId });
        } catch (e) {
            if (i === maxAttempts - 1) throw e;
            await new Promise((resolve) => setTimeout(resolve, 2000));
        }
    }
}

async function confirmIHavePaid() {
    const statusEl = document.getElementById("bankAccountStatus");
    if (!confirm("Confirm you've sent the payment? An admin will verify it and release your CCT.")) return;

    statusEl.innerText = "Confirming...";
    try {
        await submitMarkPaidWithRetry();
        document.getElementById("stepBankAccount").style.display = "none";
        document.getElementById("stepWaitBank").style.display = "block";
    } catch (e) {
        statusEl.innerText = "Could not confirm yet - try again in a moment (" + e.message + ").";
    }
}

// --- step 3b: card / online wallet / apple pay / google pay -------------

async function submitOtherForm() {
    const statusEl = document.getElementById("otherFormStatus");
    const email = document.getElementById("otherEmail").value.trim();
    const contact = document.getElementById("otherContact").value.trim();

    if (!email || !email.includes("@")) {
        statusEl.innerText = "Enter a valid email.";
        return;
    }

    statusEl.innerText = "Submitting...";
    try {
        await apiPostSigned("/treasury/purchase/create", {
            cctAmount: pendingCctAmount, method: selectedMethod,
            buyerEmail: email, buyerContact: contact,
        });
        document.getElementById("stepOtherForm").style.display = "none";
        document.getElementById("stepWaitOther").style.display = "block";
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

loadSaleInfo();
