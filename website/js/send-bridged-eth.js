// send-bridged-eth.js - the auto-bridge feature's network-selected send
// flow (see the Auto-Bridge plan's §4-5). ETHB is a single unified
// balance; the user only ever picks a destination NETWORK here, never
// manually "bridges" anything - CCT network moves it instantly inside
// CreditChain for a flat CCT fee (reuses /token/transfer, same as any
// other symbol), ERC network sends real Sepolia ETH to a 0x address
// for a flat bridge fee (POSTs to /eth-bridge/withdraw, mirrors
// send-eth.js's confirm-before-broadcast pattern since this triggers a
// real, irreversible external broadcast).

const ethbWallet = requireWallet();

// ETHBCCTNetworkFeeWei mirrors api/apply.go's constant of the same
// name (flat ETH fee, taken from the ETHB balance itself, for a
// CCT-network ETHB send - no longer charged in CCT, see that
// constant's doc comment). The ERC-network fee is no longer a flat
// mirrored constant either - it's live-tracked (gas price + 0.05%
// bridge fee), fetched from /eth-bridge/withdraw-quote just before
// review so the number shown is always current.
const ETHBCCTNetworkFeeWei = 100000000000n; // 0.0000001 ETH, illustrative

let mode = "CCT";
let ethbAddress = null;
let latestEthbBalanceWei = 0n;
let latestErcFeeWei = null;

// showFeeUsd is purely informational (see api/price_oracle.go's
// GET /price/external) - never changes what's actually charged, which
// stays denominated in ETH per the fee logic above.
async function showFeeUsd(feeWei) {
    const el = document.getElementById("reviewFeeUsd");
    try {
        const prices = await apiGet("/price/external");
        if (!prices.ethUsd) {
            el.innerText = "-";
            return;
        }
        const feeEth = Number(weiToEth(feeWei.toString()));
        el.innerText = `~$${(feeEth * prices.ethUsd).toFixed(2)}`;
    } catch (e) {
        el.innerText = "-";
    }
}

function weiToEth(weiString) {
    const wei = BigInt(weiString || "0");
    const whole = wei / 1000000000000000000n;
    const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// ethToWei parses a decimal ETHB string (e.g. "0.05") into a wei
// BigInt without floating-point precision loss - mirrors send-eth.js's
// helper of the same name.
function ethToWei(ethStr) {
    const [wholeStr, fracStr = ""] = ethStr.trim().split(".");
    const whole = BigInt(wholeStr || "0");
    const fracPadded = (fracStr + "0".repeat(18)).slice(0, 18);
    return whole * 1000000000000000000n + BigInt(fracPadded || "0");
}

function setMode(newMode) {
    mode = newMode;
    document.getElementById("modeCctBtn").classList.toggle("active", mode === "CCT");
    document.getElementById("modeErcBtn").classList.toggle("active", mode === "ERC");
    document.getElementById("toAddress").placeholder =
        mode === "CCT" ? "Recipient CreditChain address" : "Recipient Ethereum address (0x...)";
    document.getElementById("toAddress").value = "";
    document.getElementById("modeNote").innerText =
        mode === "CCT"
            ? `Instant internal move. Flat fee: ${weiToEth(ETHBCCTNetworkFeeWei.toString())} ETH (paid from your ETH balance - ETH pays its own fee, never CCT).`
            : `Real Sepolia broadcast to any 0x address. Fee = live network gas price + 0.05% bridge fee, both in ETH - shown exactly on the review screen.`;
    document.getElementById("formStatus").innerText = "";
}

async function refreshBalance() {
    if (!ethbWallet) return;
    ethbAddress = await getEthbAddress(ethbWallet);
    document.getElementById("ethbFromAddress").innerText = ethbAddress;

    try {
        const info = await apiGet(`/ethb/balance?address=${ethbAddress}`);
        latestEthbBalanceWei = BigInt(info.balance || "0");
        document.getElementById("ethbBalance").innerText = `${weiToEth(info.balance)} ETH`;
    } catch (e) {
        console.error(e);
        document.getElementById("ethbBalance").innerText = "Couldn't load balance";
    }
}

function isValidCctAddress(text) {
    return /^(cc)?[0-9a-f]{40}$/i.test(text);
}

function isValidEthAddress(text) {
    return /^0x[0-9a-fA-F]{40}$/.test(text);
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
        if (!isValidCctAddress(rawTo)) {
            statusEl.innerText = "Enter a valid recipient CreditChain address.";
            return;
        }
        to = normalizeAddress(rawTo);
    } else {
        if (!isValidEthAddress(rawTo)) {
            statusEl.innerText = "Enter a valid 0x-prefixed Ethereum address.";
            return;
        }
        to = rawTo;
    }

    const valueWei = ethToWei(amountStr);

    document.getElementById("reviewNetwork").innerText = mode === "CCT" ? "CCT network" : "ERC network (Sepolia)";
    document.getElementById("reviewTo").innerText = to;
    document.getElementById("reviewAmount").innerText = `${weiToEth(valueWei.toString())} ETH`;

    const confirmBtn = document.getElementById("confirmSendBtn");
    const gasRow = document.getElementById("reviewGasRow");
    const warningEl = document.getElementById("reviewWarning");

    if (mode === "CCT") {
        document.getElementById("reviewFee").innerText = `${weiToEth(ETHBCCTNetworkFeeWei.toString())} ETH`;
        gasRow.style.display = "none";
        warningEl.innerText = "This moves ETH instantly within CreditChain to the recipient's CreditChain address. The fee is paid in ETH, not CCT.";
        const totalWei = valueWei + ETHBCCTNetworkFeeWei;
        confirmBtn.disabled = totalWei > latestEthbBalanceWei;
        document.getElementById("reviewStatus").innerText = confirmBtn.disabled
            ? "Amount plus the ETH network fee exceeds your ETH balance."
            : "";
        document.getElementById("reviewCard").dataset.feeWei = ETHBCCTNetworkFeeWei.toString();
        showFeeUsd(ETHBCCTNetworkFeeWei);
    } else {
        statusEl.innerText = "Fetching live network fee...";
        let feeWei;
        try {
            const quote = await apiGet(`/eth-bridge/withdraw-quote?address=${ethbWallet.address}&amountWei=${valueWei}`);
            feeWei = BigInt(quote.feeWei);
        } catch (e) {
            statusEl.innerText = "Could not fetch the live fee quote: " + e.message;
            return;
        }
        statusEl.innerText = "";
        latestErcFeeWei = feeWei;

        const totalWei = valueWei + feeWei;
        document.getElementById("reviewFee").innerText = `${weiToEth(feeWei.toString())} ETH (live network gas + 0.05% bridge fee)`;
        gasRow.style.display = "flex";
        document.getElementById("reviewGas").innerText = "included in the fee above - reflects the current live Sepolia gas price";
        warningEl.innerText =
            "This broadcasts a real Sepolia testnet transaction. This action is irreversible and cannot be undone or refunded by CreditChain.";
        confirmBtn.disabled = totalWei > latestEthbBalanceWei;
        document.getElementById("reviewStatus").innerText = confirmBtn.disabled
            ? "Amount plus the live fee exceeds your ETH balance."
            : "";
        document.getElementById("reviewCard").dataset.feeWei = feeWei.toString();
        showFeeUsd(feeWei);
    }

    document.getElementById("reviewCard").dataset.mode = mode;
    document.getElementById("reviewCard").dataset.to = to;
    document.getElementById("reviewCard").dataset.valueWei = valueWei.toString();

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
    const valueWei = reviewCard.dataset.valueWei;

    confirmBtn.disabled = true;
    statusEl.innerText = "Submitting...";

    try {
        if (sendMode === "CCT") {
            // amount is wei-scale and can exceed JS's 2^53 safe-integer
            // range - built as a raw JSON literal (not passed through
            // JSON.stringify on a Number) so the exact digit string
            // survives to the Go uint64 field untouched. See
            // apiPostSignedRaw's doc comment in api.js.
            const payloadJSON = `{"to":${JSON.stringify(to)},"amount":${valueWei},"symbol":"ETHB"}`;
            const result = await apiPostSignedRaw("/token/transfer", payloadJSON);

            if (result.success === false) {
                statusEl.innerText = `Insufficient balance - you need the amount plus the ${weiToEth(ETHBCCTNetworkFeeWei.toString())} ETH network fee.`;
                confirmBtn.disabled = false;
                return;
            }

            statusEl.innerText = `Sent (tx ${result.txId}) - takes effect once the next block confirms it.`;
            setTimeout(refreshBalance, 9000);
        } else {
            const result = await apiPostSigned("/eth-bridge/withdraw", { to, amountWei: valueWei });

            if (result.status === "broadcast_failed") {
                statusEl.innerText = `Broadcast failed: ${result.error}`;
                confirmBtn.disabled = false;
                return;
            }

            statusEl.innerHTML = `Sent - <a href="https://sepolia.etherscan.io/tx/${result.ethTxHash}" target="_blank" rel="noopener" style="color:var(--cc-accent-gold, #c9a227);">view on Sepolia Etherscan</a>`;

            let attempts = 0;
            const poll = setInterval(async () => {
                attempts++;
                await refreshBalance();
                if (attempts >= 6) clearInterval(poll);
            }, 5000);
        }
    } catch (e) {
        statusEl.innerText = "Send failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

setMode("CCT");
refreshBalance();

// --- QR scanner (adapted from send.js/send-eth.js - validity check
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
    return mode === "CCT" ? isValidCctAddress(text) : isValidEthAddress(text);
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
