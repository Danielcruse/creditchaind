// send-bridged-btc.js - BTC bridge send flow (see the BTC bridge
// plan's Section B). Two genuinely separate balances, two separate
// send paths: "CCT network" moves the internal BTCB ledger balance
// instantly (flat sats fee, mirrors send-bridged-eth.js's CCT-network
// path via /token/transfer), "BTC network" spends the user's own
// real self-custodial testnet UTXOs via client-side BIP143 signing
// (website/js/vendor/btc-tx.js) and broadcasts to the real Bitcoin
// testnet network - a real, irreversible external broadcast, not an
// internal ledger move.

const btcWallet = requireWallet();

// BTCBCCTNetworkFeeSats mirrors api/apply.go's constant of the same
// name - flat sats fee, taken from the sender's own BTCB balance for
// a CCT-network BTCB send (BTC pays its own fee, never CCT).
const BTCBCCTNetworkFeeSats = 500;

const IS_MAINNET = window.CC_NETWORK === "mainnet";
const BTC_ADDRESS_PREFIX = IS_MAINNET ? "bc1" : "tb1";
const BTC_NETWORK_LABEL = IS_MAINNET ? "BTC network" : "BTC network (testnet)";
const BTC_EXPLORER_BASE = IS_MAINNET ? "https://blockstream.info/tx/" : "https://blockstream.info/testnet/tx/";
const BTC_EXPLORER_NAME = IS_MAINNET ? "Blockstream" : "Blockstream (testnet)";

let mode = "CCT";
let btcbAddress = null;
let latestBtcbBalanceSats = 0;
let realUtxos = [];
let latestRealBalanceSats = 0;

function hdBtcAccount() {
    const accounts = getHdAccounts();
    return (accounts && accounts.btc) || null;
}

function satsToBtc(sats) {
    const n = Number(sats || 0);
    const whole = Math.floor(n / 1e8);
    const frac = String(n % 1e8).padStart(8, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// showFeeUsdBtc is purely informational (see api/price_oracle.go's
// GET /price/external) - never changes what's actually charged, which
// stays denominated in BTC per the fee logic above.
async function showFeeUsdBtc(feeSats) {
    const el = document.getElementById("reviewFeeUsd");
    try {
        const prices = await apiGet("/price/external");
        if (!prices.btcUsd) {
            el.innerText = "-";
            return;
        }
        const feeBtc = Number(satsToBtc(feeSats));
        el.innerText = `~$${(feeBtc * prices.btcUsd).toFixed(2)}`;
    } catch (e) {
        el.innerText = "-";
    }
}

// btcToSats parses a decimal BTC string (e.g. "0.001") into an
// integer sats count without floating-point precision loss - mirrors
// send-bridged-eth.js's ethToWei helper, just base-1e8 instead of 1e18.
function btcToSats(btcStr) {
    const [wholeStr, fracStr = ""] = btcStr.trim().split(".");
    const whole = parseInt(wholeStr || "0", 10);
    const fracPadded = (fracStr + "00000000").slice(0, 8);
    return whole * 1e8 + parseInt(fracPadded || "0", 10);
}

function isValidCctAddress(text) {
    return /^(cc)?[0-9a-f]{40}$/i.test(text);
}

// isValidBtcAddress recognizes a native segwit address for whichever
// real Bitcoin network this deployment is on (bc1... mainnet, tb1...
// testnet) - the only kind this bridge's send flow can actually spend
// to (see btc-tx.js's disclosed legacy-address scope limit). Loose
// shape check only, mirrors the backend's own registration check
// (api/btc_bridge.go's HandleBtcRegisterAddress) - this used to
// hardcode tb1 unconditionally, which meant a real mainnet bc1...
// address always failed validation here.
function isValidBtcAddress(text) {
    const prefix = window.CC_NETWORK === "mainnet" ? "bc1" : "tb1";
    return new RegExp(`^${prefix}[0-9a-z]{11,87}$`, "i").test(text);
}

function setMode(newMode) {
    mode = newMode;
    document.getElementById("modeCctBtn").classList.toggle("active", mode === "CCT");
    document.getElementById("modeBtcBtn").classList.toggle("active", mode === "BTC");
    document.getElementById("toAddress").placeholder =
        mode === "CCT" ? "Recipient CreditChain address" : `Recipient Bitcoin address (${BTC_ADDRESS_PREFIX}...)`;
    document.getElementById("toAddress").value = "";
    document.getElementById("modeNote").innerText =
        mode === "CCT"
            ? `Instant internal move. Flat fee: ${satsToBtc(BTCBCCTNetworkFeeSats)} BTC (paid from your bridged BTC balance).`
            : `Real Bitcoin${IS_MAINNET ? "" : " testnet"} broadcast to any ${BTC_ADDRESS_PREFIX}... address. Fee = live network rate x estimated size, shown exactly on the review screen.`;
    document.getElementById("formStatus").innerText = "";
}

// btcAddressMatchesNetwork mirrors asset.js's identical helper (kept as
// its own copy here since this page has no shared top-level state with
// asset.js) - mainnet ("bc1...") and testnet ("tb1...") BTC accounts are
// derived from genuinely different BIP-84 paths, not just different
// address encodings of the same key, so a stale cached address from
// before a network switch needs a real re-derivation via
// btc-network-fix.html, not just a relabel.
function btcAddressMatchesNetwork(address) {
    if (!address) return true;
    const expectedPrefix = window.CC_NETWORK === "mainnet" ? "bc1" : "tb1";
    return address.startsWith(expectedPrefix);
}

async function registerBtcAddress() {
    const btcAccount = hdBtcAccount();
    if (!btcAccount) return;
    // Never register a wrong-network address - the server already
    // rejects this (api/btc_bridge.go's HandleBtcRegisterAddress), but
    // retrying it every page load just adds permanent dead weight to
    // the server's watch list for no benefit. refreshBalances() below
    // is what actually tells the user to fix this.
    if (!btcAddressMatchesNetwork(btcAccount.address)) return;
    try {
        await apiPostSigned("/btc-bridge/register-address", { btcAddress: btcAccount.address });
    } catch (e) {
        console.error("btc address registration failed", e);
    }
}

async function refreshBalances() {
    if (!btcWallet) return;

    btcbAddress = await deriveTokenAddress(btcWallet.publicKey, "BTCB-SUBADDRESS");
    document.getElementById("btcbAddress").innerText = btcbAddress;

    try {
        const info = await apiGet(`/btcb/balance?address=${btcbAddress}`);
        latestBtcbBalanceSats = Number(info.balance || 0);
        document.getElementById("btcbBalance").innerText = `${satsToBtc(latestBtcbBalanceSats)} BTC`;
    } catch (e) {
        console.error(e);
        document.getElementById("btcbBalance").innerText = "Couldn't load balance";
    }

    const btcAccount = hdBtcAccount();
    if (!btcAccount) {
        document.getElementById("realBtcAddress").innerText = "No Bitcoin account derived for this wallet.";
        document.getElementById("realBtcBalance").innerText = "N/A";
        return;
    }

    if (!btcAddressMatchesNetwork(btcAccount.address)) {
        document.getElementById("realBtcAddress").innerHTML =
            `${btcAccount.address} - derived for the other network. ` +
            `<a href="btc-network-fix" style="color:var(--cc-accent-gold, #c9a227);">Re-enter your recovery phrase to fix it &rarr;</a>`;
        document.getElementById("realBtcBalance").innerText = "Wrong network for this address";
        document.getElementById("modeBtcBtn").disabled = true;
        return;
    }
    document.getElementById("realBtcAddress").innerText = btcAccount.address;

    try {
        realUtxos = await apiGet(`/btc-bridge/utxos?address=${btcAccount.address}`);
        latestRealBalanceSats = realUtxos
            .filter((u) => u.status && u.status.confirmed)
            .reduce((sum, u) => sum + Number(u.value || 0), 0);
        document.getElementById("realBtcBalance").innerText = `${satsToBtc(latestRealBalanceSats)} BTC`;
    } catch (e) {
        console.error(e);
        document.getElementById("realBtcBalance").innerText = "Couldn't load balance";
    }
}

// estimateBtcNetworkFee does a lightweight preliminary coin-count pass
// (naive greedy, largest-first) to guess how many inputs a real send
// would need, then sizes a P2WPKH-in/P2WPKH-out segwit tx from that
// count. Two passes: first against a zero-fee target to find a rough
// input count, then again including that fee - close enough for a
// demo send flow, not a precision fee engine (same disclosed
// approximation class as btc-tx.js's naive coin selection itself).
async function estimateBtcNetworkFee(amountSats) {
    const rateResp = await apiGet("/btc-bridge/fee-estimate");
    const satsPerVByte = rateResp.satsPerVByte || 2;

    const confirmed = realUtxos
        .filter((u) => u.status && u.status.confirmed)
        .sort((a, b) => b.value - a.value);

    function vsizeForInputCount(n) {
        const baseVsize = 10.5;
        const perInputVsize = 68;
        const perOutputVsize = 31;
        return baseVsize + n * perInputVsize + 2 * perOutputVsize; // 2 outputs: recipient + change
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

    const rawTo = document.getElementById("toAddress").value.trim();
    const amountStr = document.getElementById("amount").value.trim();

    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }

    let to;
    if (mode === "CCT") {
        if (isValidBtcAddress(rawTo)) {
            statusEl.innerText = "That looks like a Bitcoin address - switch to BTC network to send there, or enter a CreditChain address for CCT network.";
            return;
        }
        if (!isValidCctAddress(rawTo)) {
            statusEl.innerText = "Enter a valid recipient CreditChain address.";
            return;
        }
        to = normalizeAddress(rawTo);
    } else {
        if (isValidCctAddress(rawTo) && !isValidBtcAddress(rawTo)) {
            statusEl.innerText = `That looks like a CreditChain address - switch to CCT network to send there, or enter a Bitcoin (${BTC_ADDRESS_PREFIX}...) address for BTC network.`;
            return;
        }
        if (!isValidBtcAddress(rawTo)) {
            statusEl.innerText = `Enter a valid native segwit Bitcoin${IS_MAINNET ? "" : " testnet"} address (${BTC_ADDRESS_PREFIX}...).`;
            return;
        }
        to = rawTo;
    }

    const amountSats = btcToSats(amountStr);

    document.getElementById("reviewNetwork").innerText = mode === "CCT" ? "CCT network" : BTC_NETWORK_LABEL;
    document.getElementById("reviewTo").innerText = to;
    document.getElementById("reviewAmount").innerText = `${satsToBtc(amountSats)} BTC`;

    const confirmBtn = document.getElementById("confirmSendBtn");
    const warningEl = document.getElementById("reviewWarning");

    if (mode === "CCT") {
        document.getElementById("reviewFee").innerText = `${satsToBtc(BTCBCCTNetworkFeeSats)} BTC`;
        warningEl.innerText = "This moves BTC instantly within CreditChain to the recipient's CreditChain address. The fee is paid in bridged BTC, not CCT.";
        const totalSats = amountSats + BTCBCCTNetworkFeeSats;
        confirmBtn.disabled = totalSats > latestBtcbBalanceSats;
        document.getElementById("reviewStatus").innerText = confirmBtn.disabled
            ? "Amount plus the BTC network fee exceeds your bridged BTC balance."
            : "";
        document.getElementById("reviewCard").dataset.feeSats = String(BTCBCCTNetworkFeeSats);
        showFeeUsdBtc(BTCBCCTNetworkFeeSats);
    } else {
        statusEl.innerText = "Estimating live network fee...";
        let feeSats;
        try {
            feeSats = await estimateBtcNetworkFee(amountSats);
        } catch (e) {
            statusEl.innerText = "Could not fetch the live fee estimate: " + e.message;
            return;
        }
        statusEl.innerText = "";

        const totalSats = amountSats + feeSats;
        document.getElementById("reviewFee").innerText = `${satsToBtc(feeSats)} BTC (estimated, live network rate)`;
        warningEl.innerText = `This broadcasts a real Bitcoin${IS_MAINNET ? "" : " testnet"} transaction from your own self-custodial address. This action is irreversible and cannot be undone or refunded by CreditChain.`;
        confirmBtn.disabled = totalSats > latestRealBalanceSats;
        document.getElementById("reviewStatus").innerText = confirmBtn.disabled
            ? "Amount plus the estimated fee exceeds your real BTC balance."
            : "";
        document.getElementById("reviewCard").dataset.feeSats = String(feeSats);
        showFeeUsdBtc(feeSats);
    }

    document.getElementById("reviewCard").dataset.mode = mode;
    document.getElementById("reviewCard").dataset.to = to;
    document.getElementById("reviewCard").dataset.amountSats = String(amountSats);

    document.getElementById("formCard").style.display = "none";
    document.getElementById("reviewCard").style.display = "block";
}

function cancelReview() {
    document.getElementById("reviewCard").style.display = "none";
    document.getElementById("formCard").style.display = "block";
    document.getElementById("reviewStatus").innerText = "";
}

async function confirmSend() {
    const statusEl = document.getElementById("reviewStatus");
    const reviewCard = document.getElementById("reviewCard");
    const confirmBtn = document.getElementById("confirmSendBtn");
    const sendMode = reviewCard.dataset.mode;
    const to = reviewCard.dataset.to;
    const amountSats = parseInt(reviewCard.dataset.amountSats, 10);
    const feeSats = parseInt(reviewCard.dataset.feeSats, 10);

    confirmBtn.disabled = true;
    statusEl.innerText = "Submitting...";

    try {
        if (sendMode === "CCT") {
            const result = await apiPostSigned("/token/transfer", { to, amount: amountSats, symbol: "BTCB" });

            if (result.success === false) {
                statusEl.innerText = `Insufficient balance - you need the amount plus the ${satsToBtc(BTCBCCTNetworkFeeSats)} BTC network fee.`;
                confirmBtn.disabled = false;
                return;
            }

            statusEl.innerText = `Sent (tx ${result.txId}) - takes effect once the next block confirms it. Returning to your wallet...`;
            setTimeout(refreshBalances, 9000);
            setTimeout(() => {
                location.href = "outernet";
            }, 1500);
        } else {
            const btcAccount = hdBtcAccount();
            if (!btcAccount) {
                statusEl.innerText = "No Bitcoin account available for this wallet.";
                confirmBtn.disabled = false;
                return;
            }

            const confirmed = realUtxos.filter((u) => u.status && u.status.confirmed);
            const utxosForSigning = confirmed.map((u) => ({ txid: u.txid, vout: u.vout, value: u.value }));

            let rawTxHex;
            try {
                const built = await BtcTx.signAndBuildTx(btcAccount.privateKey, {
                    utxos: utxosForSigning,
                    toAddress: to,
                    amountSats,
                    feeSats,
                    changeAddress: btcAccount.address,
                });
                rawTxHex = built.rawTxHex;
            } catch (e) {
                statusEl.innerText = "Could not build/sign the transaction: " + e.message;
                confirmBtn.disabled = false;
                return;
            }

            // Signed (not a bare apiPost) so the server learns which
            // CreditChain wallet is claiming responsibility for this
            // withdrawal and can record it - see api/btc_bridge.go's
            // HandleBtcBroadcast doc comment. The raw tx itself still
            // carries its own independent, already-complete Bitcoin
            // signature regardless of this wrapper's signer.
            const result = await apiPostSigned("/btc-bridge/broadcast", { rawTxHex, to, amountSats, feeSats });

            if (result.status === "broadcast_failed") {
                statusEl.innerText = `Broadcast failed: ${result.error}`;
                confirmBtn.disabled = false;
                return;
            }

            statusEl.innerHTML = `Sent - <a href="${BTC_EXPLORER_BASE}${result.txid}" target="_blank" rel="noopener" style="color:var(--cc-accent-gold, #c9a227);">view on ${BTC_EXPLORER_NAME}</a><br>Returning to your wallet shortly...`;

            let attempts = 0;
            const poll = setInterval(async () => {
                attempts++;
                await refreshBalances();
                if (attempts >= 6) clearInterval(poll);
            }, 5000);

            // Longer delay than the CCT-network branch above - this shows
            // a real external Blockstream link worth letting the user see
            // before navigating away, but still eventually redirects to
            // close the same refresh/back-button resubmit gap.
            setTimeout(() => {
                location.href = "outernet";
            }, 6000);
        }
    } catch (e) {
        statusEl.innerText = "Send failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

setMode("CCT");
registerBtcAddress();
refreshBalances();

// --- QR scanner (adapted from send-bridged-eth.js - validity check
// switches on the currently selected network mode) -------------------

let scanStream = null;
let scanRafId = null;
let scanLastAttempt = 0;
const SCAN_THROTTLE_MS = 300;

const scanCanvas = document.createElement("canvas");
const scanCanvasCtx = scanCanvas.getContext("2d", { willReadFrequently: true });
const SCAN_MAX_DIM = 900;

function decodeImageSource(source, sw, sh) {
    const scale = Math.min(1, SCAN_MAX_DIM / Math.max(sw, sh));
    const w = Math.max(1, Math.round(sw * scale));
    const h = Math.max(1, Math.round(sh * scale));
    scanCanvas.width = w;
    scanCanvas.height = h;
    scanCanvasCtx.drawImage(source, 0, 0, w, h);
    const imageData = scanCanvasCtx.getImageData(0, 0, w, h);
    return QRDecode.decode(imageData);
}

function isValidScannedAddress(text) {
    return mode === "CCT" ? isValidCctAddress(text) : isValidBtcAddress(text);
}

async function openScanModal() {
    document.getElementById("scanModalOverlay").style.display = "flex";
    const statusEl = document.getElementById("scanStatus");
    statusEl.innerText = "Requesting camera access...";

    try {
        scanStream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });

        const video = document.getElementById("scanVideo");
        video.srcObject = scanStream;
        await video.play().catch(() => {});

        statusEl.innerText = "Point your camera at an address QR code.";

        const scanFrame = () => {
            if (!scanStream) return;

            const now = performance.now();
            if (now - scanLastAttempt >= SCAN_THROTTLE_MS && video.videoWidth > 0) {
                scanLastAttempt = now;
                try {
                    const text = decodeImageSource(video, video.videoWidth, video.videoHeight);
                    if (text) {
                        if (isValidScannedAddress(text.trim())) {
                            document.getElementById("toAddress").value = text.trim();
                            closeScanModal();
                            return;
                        }
                        statusEl.innerText = "That QR code isn't a recognized address for this network - keep trying.";
                    }
                } catch (e) {
                    console.error(e);
                }
            }

            scanRafId = requestAnimationFrame(scanFrame);
        };
        scanRafId = requestAnimationFrame(scanFrame);
    } catch (e) {
        statusEl.innerText = "Camera unavailable: " + e.message + " - use \"take or choose a photo\" below instead.";
        closeScanModal({ keepModalOpen: true });
    }
}

async function scanFromFile(file) {
    const statusEl = document.getElementById("scanStatus");
    if (!file) return;

    try {
        const bitmap = await createImageBitmap(file);
        const text = decodeImageSource(bitmap, bitmap.width, bitmap.height);

        if (!text) {
            statusEl.innerText = "No QR code found in that photo - try again with a clearer, well-lit shot.";
            return;
        }

        if (!isValidScannedAddress(text.trim())) {
            statusEl.innerText = "That QR code isn't a recognized address for this network.";
            return;
        }

        document.getElementById("toAddress").value = text.trim();
        closeScanModal();
    } catch (e) {
        statusEl.innerText = "Couldn't read that photo: " + e.message;
    }
}

function closeScanModal(opts) {
    if (!opts || !opts.keepModalOpen) {
        document.getElementById("scanModalOverlay").style.display = "none";
    }

    if (scanRafId) {
        cancelAnimationFrame(scanRafId);
        scanRafId = null;
    }
    if (scanStream) {
        scanStream.getTracks().forEach((t) => t.stop());
        scanStream = null;
    }
}

document.getElementById("scanFileInput").addEventListener("change", (e) => {
    const file = e.target.files && e.target.files[0];
    scanFromFile(file);
    e.target.value = "";
});
