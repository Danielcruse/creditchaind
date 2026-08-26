// vault-deposit-btc.js - BTC "Lock in Vault" flow: builds and signs a
// real transaction paying the vault's shared P2WSH address plus an
// OP_RETURN depositor memo, entirely client-side (self-custodial key,
// never leaves the browser), broadcasts via /btc-bridge/broadcast, then
// polls /vault/lock-status until enough bridge validators have
// independently confirmed the lock at the required depth and BTC_CCC1
// is minted. See website/js/vault.js for the shared memo/witness-
// building helpers this reuses.

const btcVaultWallet = requireWallet();

function hdBtcAccount() {
    const accounts = getHdAccounts();
    return (accounts && accounts.btc) || null;
}

function satsToBtc(sats) {
    return smallestToDecimal(String(sats || 0), 8);
}

function btcToSats(btcStr) {
    return Number(decimalToSmallest(btcStr, 8));
}

let realUtxos = [];
let latestRealBalanceSats = 0;
let latestVaultInfo = null;

async function refresh() {
    if (!btcVaultWallet) return;

    try {
        latestVaultInfo = await fetchVaultInfo("BTC");
        document.getElementById("vaultAddress").innerText = latestVaultInfo.configured
            ? latestVaultInfo.depositAddress
            : "Not enough registered bridge validators to form a vault address yet.";
        document.getElementById("requiredThreshold").innerText = latestVaultInfo.requiredThreshold || "-";
        document.getElementById("requiredConfirmations").innerText = latestVaultInfo.requiredConfirmations || "-";
    } catch (e) {
        document.getElementById("vaultAddress").innerText = "Couldn't load vault info.";
    }

    const btcAccount = hdBtcAccount();
    if (!btcAccount) {
        document.getElementById("fromAddress").innerText = "No self-custodial Bitcoin account found - migrate your wallet first.";
        document.getElementById("realBalance").innerText = "N/A";
        return;
    }
    document.getElementById("fromAddress").innerText = btcAccount.address;

    try {
        realUtxos = await apiGet(`/btc-bridge/utxos?address=${btcAccount.address}`);
        latestRealBalanceSats = realUtxos
            .filter((u) => u.status && u.status.confirmed)
            .reduce((sum, u) => sum + Number(u.value || 0), 0);
        document.getElementById("realBalance").innerText = `${satsToBtc(latestRealBalanceSats)} BTC`;
    } catch (e) {
        document.getElementById("realBalance").innerText = "Couldn't load balance";
    }
}

// estimateVaultDepositFee mirrors send-bridged-btc.js's
// estimateBtcNetworkFee exactly, except sized for THREE outputs (vault
// P2WSH + OP_RETURN memo + change) instead of two - the memo output
// costs a little extra vsize a plain send doesn't have.
async function estimateVaultDepositFee(amountSats) {
    const rateResp = await apiGet("/btc-bridge/fee-estimate");
    const satsPerVByte = rateResp.satsPerVByte || 2;

    const confirmed = realUtxos
        .filter((u) => u.status && u.status.confirmed)
        .sort((a, b) => b.value - a.value);

    function vsizeForInputCount(n) {
        const baseVsize = 10.5;
        const perInputVsize = 68;
        const perP2wshOutputVsize = 43; // slightly larger than a 22-byte P2WPKH output
        const perOpReturnVsize = 63;    // 8(value) + 1(scriptlen) + 2(op_return+pushlen) + 52(memo)
        const perChangeOutputVsize = 31;
        return baseVsize + n * perInputVsize + perP2wshOutputVsize + perOpReturnVsize + perChangeOutputVsize;
    }

    function countInputsFor(target) {
        let total = 0;
        let count = 0;
        for (const u of confirmed) {
            if (total >= target) break;
            total += u.value;
            count++;
        }
        return count;
    }

    let count = countInputsFor(amountSats);
    let feeSats = Math.ceil(vsizeForInputCount(count) * satsPerVByte);
    count = countInputsFor(amountSats + feeSats);
    feeSats = Math.ceil(vsizeForInputCount(Math.max(count, 1)) * satsPerVByte);

    return feeSats;
}

async function review() {
    const statusEl = document.getElementById("formStatus");
    const amountStr = document.getElementById("amount").value.trim();
    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    if (!latestVaultInfo || !latestVaultInfo.configured) {
        statusEl.innerText = "The vault isn't configured on this node yet.";
        return;
    }

    const amountSats = btcToSats(amountStr);

    statusEl.innerText = "Estimating live network fee...";
    let feeSats;
    try {
        feeSats = await estimateVaultDepositFee(amountSats);
    } catch (e) {
        statusEl.innerText = "Could not fetch the live fee estimate: " + e.message;
        return;
    }
    statusEl.innerText = "";

    document.getElementById("reviewAmount").innerText = `${satsToBtc(amountSats)} BTC`;
    document.getElementById("reviewFee").innerText = `${satsToBtc(feeSats)} BTC (estimated, live network rate)`;

    const totalSats = amountSats + feeSats;
    const confirmBtn = document.getElementById("confirmBtn");
    confirmBtn.disabled = totalSats > latestRealBalanceSats;
    document.getElementById("reviewStatus").innerText = confirmBtn.disabled
        ? "Amount plus the estimated fee exceeds your real BTC balance."
        : "";

    document.getElementById("reviewCard").dataset.amountSats = String(amountSats);
    document.getElementById("reviewCard").dataset.feeSats = String(feeSats);
    document.getElementById("formCard").style.display = "none";
    document.getElementById("reviewCard").style.display = "block";
}

function cancelReview() {
    document.getElementById("reviewCard").style.display = "none";
    document.getElementById("formCard").style.display = "block";
    document.getElementById("reviewStatus").innerText = "";
}

async function confirmDeposit() {
    const statusEl = document.getElementById("reviewStatus");
    const confirmBtn = document.getElementById("confirmBtn");
    const amountSats = parseInt(document.getElementById("reviewCard").dataset.amountSats, 10);
    const feeSats = parseInt(document.getElementById("reviewCard").dataset.feeSats, 10);

    const btcAccount = hdBtcAccount();
    if (!btcAccount) {
        statusEl.innerText = "No self-custodial Bitcoin account available.";
        return;
    }

    confirmBtn.disabled = true;
    statusEl.innerText = "Signing lock transaction...";

    try {
        const confirmed = realUtxos.filter((u) => u.status && u.status.confirmed);
        const utxosForSigning = confirmed.map((u) => ({ txid: u.txid, vout: u.vout, value: u.value }));

        const { rawTxHex, txid } = await buildAndSignBtcVaultDepositTx(btcAccount.privateKey, {
            utxos: utxosForSigning,
            vaultAddress: latestVaultInfo.depositAddress,
            amountSats,
            feeSats,
            changeAddress: btcAccount.address,
            ccAddressHex: btcVaultWallet.address,
            publicKeyHex: btcVaultWallet.publicKey,
        });

        statusEl.innerText = "Broadcasting...";
        const result = await apiPostSigned("/btc-bridge/broadcast", { rawTxHex, to: latestVaultInfo.depositAddress, amountSats, feeSats });

        if (result.status === "broadcast_failed") {
            statusEl.innerText = `Broadcast failed: ${result.error}`;
            confirmBtn.disabled = false;
            return;
        }

        document.getElementById("reviewCard").style.display = "none";
        document.getElementById("progressCard").style.display = "block";
        document.getElementById("progressNote").innerText = `Locked - waiting for confirmation depth and validator attestations (tx ${result.txid || txid}).`;
        document.getElementById("attestThreshold").innerText = latestVaultInfo.requiredThreshold;

        pollUntil(
            () => fetchLockStatus("BTC", result.txid || txid, 0, { ccAddress: btcVaultWallet.address, publicKey: btcVaultWallet.publicKey, amountSmallestUnit: String(amountSats) }),
            (status) => status.status === "MINTED",
            (status) => {
                document.getElementById("attestCount").innerText = status.attestations || 0;
                document.getElementById("attestStatusPill").innerText = status.status || "PENDING";
                if (status.status === "MINTED") {
                    document.getElementById("progressNote").innerText = "BTC_CCC1 minted - your balance is updated.";
                }
            },
            { intervalMs: 15000, maxAttempts: 240 }
        );
    } catch (e) {
        statusEl.innerText = "Lock failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

// A caller can deep-link straight to Review with an amount pre-filled
// (see asset.js's "Lock now" pending-deposit banner on the BTC asset
// page, which links here as vault-deposit-btc.html?amount=X) - still
// requires the user's own explicit "Confirm and Lock" click to actually
// sign/broadcast anything, same as manual use of this page, just
// skipping the typing step.
async function initFromQuery() {
    await refresh();
    const autoAmount = new URLSearchParams(window.location.search).get("amount");
    if (!autoAmount) return;
    document.getElementById("amount").value = autoAmount;
    review();
}
initFromQuery();
