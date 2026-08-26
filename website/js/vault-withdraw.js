// vault-withdraw.js - two ways to spend a vault-backed CCC1 balance:
// "Send to CreditChain user" moves the balance instantly, internally,
// to another CreditChain user's own CCC1 balance (no burn, no real-
// chain movement, reuses the same generic /token/transfer path ETHB's
// CCT-network send already uses - see api/p2p_offers.go's
// resolveTokenStore). "Withdraw to real address" burns the balance and
// requests release of the real locked asset to a real external address,
// then polls /vault/release-status until enough bridge validators have
// co-signed (see core/vault_release_approval.go's doc comment - the
// actual real-chain release broadcast is a separate node-local relay,
// this page only ever submits the burn request and watches for the
// outcome). Mirrors send-bridged-eth.html's Direct/CCT-network mode
// toggle - same two-ways-to-spend-one-balance shape, just with the
// instant path being the CreditChain-internal one here instead.

const vaultWithdrawWallet = requireWallet();

let chain = "ETH";
let sendMode = "SEND";

function setChain(newChain) {
    chain = newChain;
    document.getElementById("chainSelect").value = chain;
    updateDestPlaceholder();
    amountUnit = "TOKEN"; // switching asset - start over in token units rather than carry a stale fiat conversion
    refreshAmountUnitToggle();
    document.getElementById("amount").value = "";
    document.getElementById("amountPreview").innerText = "";
    document.getElementById("formStatus").innerText = "";
    refreshBalance();
}

function setSendMode(newMode) {
    sendMode = newMode;
    document.getElementById("sendModeSendBtn").classList.toggle("active", sendMode === "SEND");
    document.getElementById("sendModeWithdrawBtn").classList.toggle("active", sendMode === "WITHDRAW");
    document.getElementById("modeExplainer").innerText = sendMode === "SEND"
        ? "Moves your vault-backed balance instantly to another CreditChain user's own balance - nothing is burned, nothing touches the real chain, no validator wait."
        : "Burning a vault-backed balance requests release of the real locked asset to a real external address you control. " +
          "Multiple bridge validators must each independently co-sign the release before it's actually broadcast on the real chain - this can take a few minutes.";
    updateDestPlaceholder();
    document.getElementById("formStatus").innerText = "";
}

function updateDestPlaceholder() {
    const el = document.getElementById("destAddress");
    if (sendMode === "SEND") {
        el.placeholder = "Recipient CreditChain address";
        return;
    }
    el.placeholder = chain === "BTC"
        ? `Destination Bitcoin address (${window.CC_NETWORK === "mainnet" ? "bc1" : "tb1"}...)`
        : "Destination Ethereum address (0x...)";
}

async function refreshBalance() {
    if (!vaultWithdrawWallet) return;
    const info = VAULT_CHAIN_INFO[chain];
    const fiatEl = document.getElementById("ccc1BalanceFiat");
    fiatEl.innerText = "";
    try {
        const { balance } = await fetchCcc1Balance(vaultWithdrawWallet, chain);
        const decimalBalance = smallestToDecimal(balance, info.decimals);
        document.getElementById("ccc1Balance").innerText = `${decimalBalance} ${info.ccc1Symbol}`;
        // fiatValue expects the plain chain symbol ("ETH"/"USDC"/"BTC"),
        // which is exactly what `chain` already is here - see
        // amountRateFor's own doc comment below for the same mapping.
        fiatLine(chain, decimalBalance).then((line) => { fiatEl.innerText = line; });
    } catch (e) {
        document.getElementById("ccc1Balance").innerText = "Couldn't load balance";
    }
}

// --- fiat/token amount entry ---------------------------------------------
//
// Withdrawing by typing a fiat amount ("I want to pull out $50") instead
// of the token's own decimal amount - reuses currency.js's already-live
// price feed (the same one assets.js/asset.js show a fiat-equivalent line
// from), no new backend endpoint. chain ("ETH"/"USDC"/"BTC") already
// matches fiatValue's own symbol convention exactly, so no CCC1-specific
// mapping is needed. Fiat rates are inherently approximate (a live quote,
// not an on-chain price), so a converted token amount is rounded to a
// fixed, generous precision (8 decimals - covers BTC's own decimals
// exactly and is far more precision than a fiat-derived amount needs for
// ETH/USDC) rather than carried at full floating-point precision.
//
// amountUnit is which currency the input field's raw value is currently
// IN - a small pill button (the swap-arrow toggle next to the input,
// see vault-withdraw.html) switches it, converting whatever's already
// typed using the same live rate updateAmountPreview shows, so toggling
// mid-entry doesn't just blank the field.
let amountUnit = "TOKEN";

function amountRateFor(rates) {
    if (chain === "ETH") return rates.eth;
    if (chain === "BTC") return rates.btc;
    return rates.usdc;
}

function refreshAmountUnitToggle() {
    const info = VAULT_CHAIN_INFO[chain];
    const label = amountUnit === "TOKEN" ? info.ccc1Symbol : getDisplayCurrency();
    document.getElementById("amountUnitToggleLabel").innerText = label;
    document.getElementById("amount").placeholder = `Amount to withdraw (${label})`;
}

// toggleAmountUnit swaps TOKEN<->FIAT, converting whatever's currently
// typed via the live rate so the underlying real amount is preserved
// across the swap (not just cleared) - best-effort: if the rate isn't
// available or nothing's typed yet, it still swaps the unit/label, just
// with nothing to convert.
async function toggleAmountUnit() {
    const nextUnit = amountUnit === "TOKEN" ? "FIAT" : "TOKEN";
    const amountEl = document.getElementById("amount");
    const raw = amountEl.value.trim();
    const num = parseFloat(raw);

    if (raw && !isNaN(num) && num > 0) {
        try {
            const rates = await fetchRates(getDisplayCurrency());
            const rate = amountRateFor(rates);
            if (rate) {
                const info = VAULT_CHAIN_INFO[chain];
                amountEl.value = nextUnit === "FIAT"
                    ? (num * rate).toFixed(2)
                    : (num / rate).toFixed(Math.min(info.decimals, 8));
            }
        } catch (e) {
            // Rate fetch failed - fall through and swap the unit anyway,
            // just without converting the already-typed value.
        }
    }

    amountUnit = nextUnit;
    refreshAmountUnitToggle();
    updateAmountPreview();
}

let amountPreviewDebounce = null;
function updateAmountPreview() {
    clearTimeout(amountPreviewDebounce);
    const previewEl = document.getElementById("amountPreview");
    const raw = document.getElementById("amount").value.trim();
    const num = parseFloat(raw);
    if (!raw || isNaN(num) || num <= 0) {
        previewEl.innerText = "";
        return;
    }
    amountPreviewDebounce = setTimeout(async () => {
        try {
            const currency = getDisplayCurrency();
            const rates = await fetchRates(currency);
            const rate = amountRateFor(rates);
            if (!rate) {
                previewEl.innerText = "Live price unavailable for this asset right now.";
                return;
            }
            const info = VAULT_CHAIN_INFO[chain];
            if (amountUnit === "TOKEN") {
                previewEl.innerText = `≈ ${currencySymbolFor(currency)}${(num * rate).toLocaleString(undefined, { maximumFractionDigits: 2 })}`;
            } else {
                previewEl.innerText = `≈ ${(num / rate).toFixed(Math.min(info.decimals, 8))} ${info.ccc1Symbol}`;
            }
        } catch (e) {
            previewEl.innerText = "";
        }
    }, 300);
}

function isValidCctAddress(text) {
    return /^(cc)?[0-9a-f]{40}$/i.test(text);
}

function isValidRealChainDestination(text) {
    if (chain === "BTC") {
        const prefix = window.CC_NETWORK === "mainnet" ? "bc1" : "tb1";
        return new RegExp(`^${prefix}[0-9a-z]{11,87}$`, "i").test(text);
    }
    return /^0x[0-9a-fA-F]{40}$/.test(text);
}

async function review() {
    const statusEl = document.getElementById("formStatus");
    const dest = document.getElementById("destAddress").value.trim();
    const enteredStr = document.getElementById("amount").value.trim();
    const enteredNum = parseFloat(enteredStr);

    if (isNaN(enteredNum) || enteredNum <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }

    const info = VAULT_CHAIN_INFO[chain];

    // amountStr is always the TOKEN decimal amount from here on - a
    // fiat entry gets converted once, up front, using the same live
    // rate updateAmountPreview already showed the user.
    let amountStr = enteredStr;
    if (amountUnit === "FIAT") {
        statusEl.innerText = "Converting...";
        let rate;
        try {
            const rates = await fetchRates(getDisplayCurrency());
            rate = amountRateFor(rates);
        } catch (e) {
            statusEl.innerText = "Could not fetch a live price - try again, or switch the toggle back to " + info.ccc1Symbol + ".";
            return;
        }
        if (!rate) {
            statusEl.innerText = "Live price unavailable for this asset right now - switch the toggle back to " + info.ccc1Symbol + " instead.";
            return;
        }
        amountStr = (enteredNum / rate).toFixed(Math.min(info.decimals, 8));
        statusEl.innerText = "";
    }
    const amount = parseFloat(amountStr);

    let normalizedDest = dest;
    if (sendMode === "SEND") {
        if (!isValidCctAddress(dest)) {
            statusEl.innerText = "Enter a valid recipient CreditChain address.";
            return;
        }
        normalizedDest = normalizeAddress(dest);
    } else {
        if (!isValidRealChainDestination(dest)) {
            statusEl.innerText = chain === "BTC" ? "Enter a valid destination Bitcoin address." : "Enter a valid destination Ethereum address (0x...).";
            return;
        }
    }

    const amountSmallest = decimalToSmallest(amountStr, info.decimals);

    document.getElementById("reviewTitle").innerText = sendMode === "SEND" ? "Confirm Send" : "Confirm Withdrawal";
    document.getElementById("reviewChain").innerText = chain;
    document.getElementById("reviewDest").innerText = dest;
    document.getElementById("reviewAmount").innerText = amountUnit === "FIAT"
        ? `${amountStr} ${info.ccc1Symbol} (converted from ${currencySymbolFor(getDisplayCurrency())}${enteredStr})`
        : `${amountStr} ${info.ccc1Symbol}`;
    document.getElementById("reviewWarning").innerText = sendMode === "SEND"
        ? "This moves your balance instantly to the recipient's CreditChain address - there is no undo once it confirms. " +
          `A small ${info.ccc1Symbol} network fee (approximating this token's real on-chain gas cost) also applies on top of the amount, taken from this same balance.`
        : "This burns your vault-backed balance immediately. Release of the real asset happens once enough bridge validators " +
          "co-sign - if something goes wrong before that point, contact support with your burn transaction ID.";
    document.getElementById("confirmBtn").innerText = sendMode === "SEND" ? "Confirm and Send" : "Confirm and Burn";

    document.getElementById("reviewCard").dataset.dest = normalizedDest;
    document.getElementById("reviewCard").dataset.amountSmallest = amountSmallest;
    document.getElementById("formCard").style.display = "none";
    document.getElementById("reviewCard").style.display = "block";
}

function cancelReview() {
    document.getElementById("reviewCard").style.display = "none";
    document.getElementById("formCard").style.display = "block";
    document.getElementById("reviewStatus").innerText = "";
}

async function confirmAction() {
    if (sendMode === "SEND") {
        await confirmSend();
    } else {
        await confirmWithdraw();
    }
}

// confirmSend submits the instant, internal CCC1-to-CCC1 transfer via
// the same generic /token/transfer path ETHB's CCT-network send already
// uses. Charges a small same-token fee approximating this token's real
// on-chain gas cost (see api/ccc1_transfer_fee.go), split 50/30/20
// Foundation/burn/validator-pool - HandleTokenTransfer's response
// carries the exact amount actually charged in result.fee.
async function confirmSend() {
    const statusEl = document.getElementById("reviewStatus");
    const confirmBtn = document.getElementById("confirmBtn");
    const dest = document.getElementById("reviewCard").dataset.dest;
    const amountSmallest = document.getElementById("reviewCard").dataset.amountSmallest;
    const info = VAULT_CHAIN_INFO[chain];

    confirmBtn.disabled = true;
    statusEl.innerText = "Submitting...";

    try {
        // amountSmallest is wei-scale for ETH_CCC1 and can exceed JS's
        // 2^53 safe-integer range - built as a raw JSON literal (not
        // JSON.stringify on a Number) so the exact digit string survives
        // to the Go uint64 field untouched, same convention
        // apiPostSignedRaw's doc comment (api.js) establishes.
        const payloadJSON = `{"to":${JSON.stringify(dest)},"amount":${amountSmallest},"symbol":${JSON.stringify(info.ccc1Symbol)}}`;
        const result = await apiPostSignedRaw("/token/transfer", payloadJSON);

        if (result.success === false) {
            statusEl.innerText = result.reason || "Insufficient balance.";
            confirmBtn.disabled = false;
            return;
        }

        document.getElementById("reviewCard").style.display = "none";
        document.getElementById("progressCard").style.display = "block";
        document.getElementById("progressTitle").innerText = "Sent";
        document.getElementById("progressSigRow").style.display = "none";
        const feeNote = result.fee ? ` - ${smallestToDecimal(String(result.fee), info.decimals)} ${info.ccc1Symbol} network fee applied` : "";
        document.getElementById("progressNote").innerText =
            `Sent (tx ${result.txId})${feeNote} - takes effect once the next block confirms it.`;

        refreshBalance();
    } catch (e) {
        statusEl.innerText = "Send failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

async function confirmWithdraw() {
    const statusEl = document.getElementById("reviewStatus");
    const confirmBtn = document.getElementById("confirmBtn");
    const dest = document.getElementById("reviewCard").dataset.dest;
    const amountSmallest = document.getElementById("reviewCard").dataset.amountSmallest;

    confirmBtn.disabled = true;
    statusEl.innerText = "Submitting burn...";

    try {
        const result = await burnForRelease(chain, dest, amountSmallest);
        const burnTxId = result.txId;

        document.getElementById("reviewCard").style.display = "none";
        document.getElementById("progressCard").style.display = "block";
        document.getElementById("progressTitle").innerText = "Waiting for release";
        document.getElementById("progressSigRow").style.display = "block";
        document.getElementById("progressNote").innerText = `Burned - waiting for validator co-signatures (burn tx ${burnTxId}).`;

        const info = await fetchVaultInfo(chain);
        document.getElementById("sigThreshold").innerText = info.requiredThreshold;

        pollUntil(
            () => fetchReleaseStatus(burnTxId),
            (status) => status.status === "RELEASED",
            (status) => {
                document.getElementById("sigCount").innerText = status.signatures || 0;
                document.getElementById("releaseStatusPill").innerText = status.status || "PENDING";
                if (status.status === "RELEASED") {
                    document.getElementById("progressNote").innerText = "Released - the real asset has been sent to your destination address.";
                }
            },
            { intervalMs: 10000, maxAttempts: 240 }
        );

        refreshBalance();
    } catch (e) {
        statusEl.innerText = "Withdrawal failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

// A caller can deep-link straight into a specific chain's form (see
// asset.js's Vault-locked section, which links here as
// vault-withdraw.html?chain=BTC so "Send BTC_CCC1" from inside the BTC
// asset page doesn't dump the user on the ETH tab first) - falls back to
// ETH, today's unparameterized default, for any other/missing value.
// Always starts on SEND mode - the instant, no-validator-wait path is
// the common case; withdrawal to a real address is the deliberate,
// slower opt-in.
const initialChain = new URLSearchParams(window.location.search).get("chain");
setSendMode("SEND");
setChain(["ETH", "USDC", "BTC"].includes(initialChain) ? initialChain : "ETH");
