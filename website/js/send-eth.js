// send-eth.js - Multi-Chain HD Wallet Phase B: real Sepolia ETH send
// for the self-custodial account (website/js/hdwallet.js). Structurally
// separate from send.js (CreditChain-internal CCT/ETHTEST/USDCTEST
// transfers, apiPostSigned("/token/transfer", ...)) at nearly every
// step: 0x address format, /eth-selfcustody/* balance source, and
// above all local ECDSA signing (website/js/vendor/eth-tx.js) instead
// of a CreditChain wallet signature - see the Multi-Chain HD Wallet
// Phase B plan for why this is a dedicated page, not a branch in
// send.js.

const GAS_LIMIT = 21000n;

// ETH_CHAIN_ID must match whatever api/eth_rpc.go's expectedEthChainID
// actually is for this network (1 = real Ethereum mainnet, 11155111 =
// Sepolia testnet) - signing with the wrong chain ID doesn't just show
// wrong wording, it makes the broadcast handler's own chain-ID check
// reject the transaction outright (see ethBroadcastRaw), so every real
// mainnet send from this page would silently fail without this.
const ETH_CHAIN_ID = window.CC_NETWORK === "mainnet" ? 1 : 11155111;
const ETH_EXPLORER_BASE = window.CC_NETWORK === "mainnet" ? "https://etherscan.io/tx/" : "https://sepolia.etherscan.io/tx/";
const ETH_EXPLORER_NAME = window.CC_NETWORK === "mainnet" ? "Etherscan" : "Sepolia Etherscan";

(function setNetworkAwareCopy() {
    const isMainnet = window.CC_NETWORK === "mainnet";
    document.getElementById("pageTitle").innerText = isMainnet ? "Send ETH" : "Send ETH (Sepolia testnet)";
    document.getElementById("networkNotice").innerText = isMainnet
        ? "This sends real ETH from your self-custodial account. Signing happens entirely in this browser - your private key never leaves it."
        : "This sends real Sepolia testnet ETH from your self-custodial account. Signing happens entirely in this browser - your private key never leaves it.";
    document.getElementById("confirmWarning").innerHTML = isMainnet
        ? "This sends real ETH. This action is <strong>irreversible</strong> and cannot be undone or refunded by CreditChain."
        : "This sends real Sepolia testnet ETH. This action is <strong>irreversible</strong> and cannot be undone or refunded by CreditChain.";
})();

function hdEthAccount() {
    const accounts = getHdAccounts();
    return accounts && accounts.eth ? accounts.eth : null;
}

function weiToEth(weiString) {
    const wei = BigInt(weiString);
    const whole = wei / 1000000000000000000n;
    const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// ethToWei parses a decimal ETH string (e.g. "0.05") into a wei BigInt
// without floating-point precision loss.
function ethToWei(ethStr) {
    const [wholeStr, fracStr = ""] = ethStr.trim().split(".");
    const whole = BigInt(wholeStr || "0");
    const fracPadded = (fracStr + "0".repeat(18)).slice(0, 18);
    return whole * 1000000000000000000n + BigInt(fracPadded || "0");
}

const account = hdEthAccount();
let latestBalanceInfo = null; // {weiBalance, nonce, suggestedGasPriceWei}

async function refreshBalance() {
    if (!account) {
        document.getElementById("ethBalance").innerText = "No self-custodial Ethereum account found - migrate your wallet first.";
        document.getElementById("formCard").style.display = "none";
        return;
    }
    document.getElementById("ethFromAddress").innerText = account.address;

    try {
        const info = await apiGet(`/eth-selfcustody/balance?address=${account.address}`);
        latestBalanceInfo = info;
        document.getElementById("ethBalance").innerText = `${weiToEth(info.weiBalance)} ETH`;
    } catch (e) {
        console.error(e);
        document.getElementById("ethBalance").innerText = window.CC_NETWORK === "mainnet"
            ? "Couldn't load balance (Ethereum RPC unavailable)"
            : "Couldn't load balance (Sepolia RPC unavailable)";
    }
}

function review() {
    const statusEl = document.getElementById("formStatus");
    if (!account) return;

    const to = document.getElementById("toAddress").value.trim();
    const amountStr = document.getElementById("amount").value.trim();

    if (!/^0x[0-9a-fA-F]{40}$/.test(to)) {
        statusEl.innerText = "Enter a valid 0x-prefixed Ethereum address.";
        return;
    }
    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    if (!latestBalanceInfo) {
        statusEl.innerText = "Balance/gas info isn't loaded yet - wait a moment and try again.";
        return;
    }

    const valueWei = ethToWei(amountStr);
    const gasPriceWei = BigInt(latestBalanceInfo.suggestedGasPriceWei);
    const feeWei = GAS_LIMIT * gasPriceWei;
    const totalWei = valueWei + feeWei;
    const balanceWei = BigInt(latestBalanceInfo.weiBalance);

    document.getElementById("reviewTo").innerText = to;
    document.getElementById("reviewAmount").innerText = `${weiToEth(valueWei.toString())} ETH`;
    document.getElementById("reviewFee").innerText = `~${weiToEth(feeWei.toString())} ETH`;
    document.getElementById("reviewTotal").innerText = `${weiToEth(totalWei.toString())} ETH`;

    const confirmBtn = document.getElementById("confirmSendBtn");
    confirmBtn.disabled = totalWei > balanceWei;
    document.getElementById("reviewStatus").innerText = confirmBtn.disabled
        ? "Amount plus estimated fee exceeds your balance."
        : "";

    document.getElementById("reviewCard").dataset.to = to;
    document.getElementById("reviewCard").dataset.valueWei = valueWei.toString();
    document.getElementById("reviewCard").dataset.nonce = latestBalanceInfo.nonce;
    document.getElementById("reviewCard").dataset.gasPriceWei = gasPriceWei.toString();

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

    confirmBtn.disabled = true;
    statusEl.innerText = "Signing locally...";

    try {
        // The only point account.privateKey is ever touched - it never
        // leaves this function, and this device is the only place it
        // has ever existed (derived client-side in hdwallet.js).
        const { rawTxHex, txHash } = await EthTx.signLegacyTx(account.privateKey, {
            nonce: parseInt(reviewCard.dataset.nonce, 10),
            gasPriceWei: reviewCard.dataset.gasPriceWei,
            gasLimit: Number(GAS_LIMIT),
            to: reviewCard.dataset.to,
            valueWei: reviewCard.dataset.valueWei,
            chainId: ETH_CHAIN_ID,
        });

        statusEl.innerText = "Broadcasting...";
        const result = await apiPost("/eth-selfcustody/broadcast", { rawTxHex });

        statusEl.innerHTML = `Sent - <a href="${ETH_EXPLORER_BASE}${result.txHash}" target="_blank" rel="noopener" style="color:var(--cc-accent-gold, #c9a227);">view on ${ETH_EXPLORER_NAME}</a><br>Returning to your wallet shortly...`;

        // Sepolia confirmation times vary more than CreditChain's own
        // block time - poll a few times over ~30s rather than a single
        // fixed-delay refresh.
        let attempts = 0;
        const poll = setInterval(async () => {
            attempts++;
            await refreshBalance();
            if (attempts >= 6) clearInterval(poll);
        }, 5000);

        // Longer delay than the internal-send pages - this page shows a
        // real external Etherscan link worth letting the user actually
        // see/click before navigating away, but it still redirects
        // eventually to close the same refresh/back-button resubmit gap
        // (a stale review card + reloaded nonce could otherwise produce
        // a second real broadcast).
        setTimeout(() => {
            location.href = "outernet";
        }, 6000);
    } catch (e) {
        statusEl.innerText = "Send failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

refreshBalance();

// --- QR scanner (adapted from send.js - uses website/js/qr-decode.js,
// same reasoning: the previous BarcodeDetector-only approach silently
// failed on every desktop browser, only the address-validation regex
// differs here since Ethereum addresses are 0x-prefixed) -----------

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
    return /^0x[0-9a-fA-F]{40}$/.test(text);
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

        statusEl.innerText = "Point your camera at an Ethereum address QR code.";

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
                        statusEl.innerText = "That QR code isn't a recognized Ethereum address - keep trying.";
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
            statusEl.innerText = "That QR code isn't a recognized Ethereum address.";
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
