// send-usdc.js - real, self-custodial USDC (ERC-20) send. Same account
// as send-eth.js (the HD-derived Ethereum account, website/js/hdwallet.js)
// - an ERC-20 token balance lives at whatever address holds it, no
// separate key. This is NOT the KMS-custodial DeFi Corner - CreditChain
// never holds this key, matching self-custodial ETH exactly. A USDC
// transfer is a contract call (to=USDC contract, value=0, data=ABI-
// encoded transfer(recipient, amount)), signed with the same
// secp256k1/RLP machinery already proven correct for plain ETH sends
// (website/js/vendor/eth-tx.js) - only the calldata differs.

// A standard ERC-20 transfer costs ~50-65k gas; USDC's real contract is
// a proxy (slightly more overhead per call) and no live gas-estimation
// endpoint exists yet, so this uses a fixed, generous estimate rather
// than the exact minimum - a real broadcast still only spends what it
// actually uses, any unused gas allowance is simply not charged.
const USDC_GAS_LIMIT = 120000n;
const USDC_DECIMALS = 6;

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

// usdcToSmallest/smallestToUsdc: same base-1e6 string arithmetic as
// weiToEth/ethToWei, just 6 decimals instead of 18 - no float math.
function smallestToUsdc(smallestStr) {
    const n = BigInt(smallestStr || "0");
    const scale = 1000000n;
    const whole = n / scale;
    const frac = (n % scale).toString().padStart(USDC_DECIMALS, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

function usdcToSmallest(usdcStr) {
    const [wholeStr, fracStr = ""] = usdcStr.trim().split(".");
    const whole = BigInt(wholeStr || "0");
    const fracPadded = (fracStr + "0".repeat(USDC_DECIMALS)).slice(0, USDC_DECIMALS);
    return whole * 1000000n + BigInt(fracPadded || "0");
}

const account = hdEthAccount();
let latestUsdcInfo = null; // {contract, balance, nonce, suggestedGasPriceWei}
let latestEthWei = "0";

async function refreshBalance() {
    if (!account) {
        document.getElementById("usdcBalance").innerText = "No self-custodial Ethereum account found - migrate your wallet first.";
        document.getElementById("formCard").style.display = "none";
        return;
    }
    document.getElementById("usdcFromAddress").innerText = account.address;

    try {
        const ethInfo = await apiGet(`/eth-selfcustody/balance?address=${account.address}`);
        latestEthWei = ethInfo.weiBalance;
        document.getElementById("ethFeeBalance").innerText = `${weiToEth(latestEthWei)} ETH`;
    } catch (e) {
        console.error(e);
        document.getElementById("ethFeeBalance").innerText = "Couldn't load ETH balance";
    }

    try {
        const info = await apiGet(`/erc20/usdc-balance?address=${account.address}`);
        latestUsdcInfo = info;
        document.getElementById("usdcBalance").innerText = `${smallestToUsdc(info.balance)} USDC`;
    } catch (e) {
        console.error(e);
        document.getElementById("usdcBalance").innerText = "Couldn't load USDC balance (ETH RPC unavailable)";
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
    if (!latestUsdcInfo) {
        statusEl.innerText = "Balance/gas info isn't loaded yet - wait a moment and try again.";
        return;
    }

    const amountSmallest = usdcToSmallest(amountStr);
    const balanceSmallest = BigInt(latestUsdcInfo.balance);
    const gasPriceWei = BigInt(latestUsdcInfo.suggestedGasPriceWei);
    const feeWei = USDC_GAS_LIMIT * gasPriceWei;

    document.getElementById("reviewTo").innerText = to;
    document.getElementById("reviewAmount").innerText = `${smallestToUsdc(amountSmallest.toString())} USDC`;
    document.getElementById("reviewFee").innerText = `~${weiToEth(feeWei.toString())} ETH`;

    const confirmBtn = document.getElementById("confirmSendBtn");
    const insufficientUsdc = amountSmallest > balanceSmallest;
    const insufficientEthForFee = feeWei > BigInt(latestEthWei || "0");
    confirmBtn.disabled = insufficientUsdc || insufficientEthForFee;

    document.getElementById("reviewStatus").innerText = insufficientUsdc
        ? "Amount exceeds your USDC balance."
        : insufficientEthForFee
        ? "Not enough ETH in this account to cover the estimated network fee."
        : "";

    document.getElementById("reviewCard").dataset.to = to;
    document.getElementById("reviewCard").dataset.amountSmallest = amountSmallest.toString();
    document.getElementById("reviewCard").dataset.nonce = latestUsdcInfo.nonce;
    document.getElementById("reviewCard").dataset.gasPriceWei = gasPriceWei.toString();
    document.getElementById("reviewCard").dataset.contract = latestUsdcInfo.contract;

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
        const calldata = Erc20Abi.erc20TransferCalldata(reviewCard.dataset.to, reviewCard.dataset.amountSmallest);

        // The only point account.privateKey is ever touched - it never
        // leaves this function, and this device is the only place it
        // has ever existed (derived client-side in hdwallet.js). The
        // transaction's `to` is the USDC CONTRACT (not the recipient) -
        // the recipient/amount are encoded inside `data` instead, since
        // this is a contract call, not a plain value transfer.
        const { rawTxHex } = await EthTx.signLegacyTx(account.privateKey, {
            nonce: parseInt(reviewCard.dataset.nonce, 10),
            gasPriceWei: reviewCard.dataset.gasPriceWei,
            gasLimit: Number(USDC_GAS_LIMIT),
            to: reviewCard.dataset.contract,
            valueWei: "0",
            chainId: window.CC_NETWORK === "mainnet" ? 1 : 11155111,
            data: calldata,
        });

        statusEl.innerText = "Broadcasting...";
        const result = await apiPost("/eth-selfcustody/broadcast", { rawTxHex });
        const txHash = result.txHash;

        const explorerBase = window.CC_NETWORK === "mainnet" ? "https://etherscan.io/tx/" : "https://sepolia.etherscan.io/tx/";
        statusEl.innerHTML = `Sent - <a href="${explorerBase}${txHash}" target="_blank" rel="noopener" style="color:var(--cc-accent-gold, #16B8A6);">view on Etherscan</a><br>Returning to your wallet shortly...`;

        let attempts = 0;
        const poll = setInterval(async () => {
            attempts++;
            await refreshBalance();
            if (attempts >= 6) clearInterval(poll);
        }, 5000);

        setTimeout(() => {
            location.href = "outernet";
        }, 6000);
    } catch (e) {
        statusEl.innerText = "Send failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

refreshBalance();

// --- QR scanner (identical to send-eth.js's) --------------------------

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
