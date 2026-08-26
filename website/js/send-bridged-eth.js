// send-bridged-eth.js - the single, unified "Send ETH" flow for every
// ETH entry point in the app (asset.html's ETH page, the generic
// send.html dropdown's ETH and ETH-bridge options, etc.) - the user
// only ever picks a MODE here, never manually "bridges" anything.
// Two modes, one balance: Direct broadcasts straight from the
// self-custodial account (EthTx.signLegacyTx + /eth-selfcustody/
// broadcast, no CreditChain fee, real network gas only - signed
// entirely in this browser, the key never leaves it). CCT network
// moves the ETHB ledger balance instantly to another CreditChain user
// for a flat ETH fee (reuses /token/transfer, same as any other
// symbol). There used to be a third "ERC network" mode that broadcast
// via a CreditChain-held custodial signer - retired (see
// api/eth_bridge_withdraw.go's HandleBridgedEthWithdraw doc comment):
// CreditChain never holds an ETH signing key on anyone's behalf, and
// Direct mode already covers real-address sends for free.

const ethbWallet = requireWallet();

// ETHBCCTNetworkFeeWei mirrors api/apply.go's constant of the same
// name (flat ETH fee, taken from the ETHB balance itself, for a
// CCT-network ETHB send - no longer charged in CCT, see that
// constant's doc comment).
const ETHBCCTNetworkFeeWei = 100000000000n; // 0.0000001 ETH, illustrative

// GAS_LIMIT/ETH_CHAIN_ID and hdEthAccount() mirror send-eth.js exactly -
// Direct mode reuses that exact same self-custodial signing path
// (EthTx.signLegacyTx + /eth-selfcustody/broadcast), just as a third
// mode on this one page instead of a second, separate page - see this
// file's top-of-file comment for why all three modes now live together.
const GAS_LIMIT = 21000n;
const ETH_CHAIN_ID = window.CC_NETWORK === "mainnet" ? 1 : 11155111;

function hdEthAccount() {
    const accounts = getHdAccounts();
    return accounts && accounts.eth ? accounts.eth : null;
}

const IS_MAINNET = window.CC_NETWORK === "mainnet";
const ETH_NETWORK_LABEL = IS_MAINNET ? "real ETH" : "real Sepolia";
const ETH_EXPLORER_BASE = IS_MAINNET ? "https://etherscan.io/tx/" : "https://sepolia.etherscan.io/tx/";
const ETH_EXPLORER_NAME = IS_MAINNET ? "Etherscan" : "Sepolia Etherscan";

let mode = "CCT";
let ethbAddress = null;
let latestEthbBalanceWei = 0n;
let latestSelfCustodialInfo = null; // {weiBalance, nonce, suggestedGasPriceWei}

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
    document.getElementById("modeDirectBtn").classList.toggle("active", mode === "DIRECT");
    document.getElementById("modeCctBtn").classList.toggle("active", mode === "CCT");
    document.getElementById("toAddress").placeholder =
        mode === "CCT" ? "Recipient CreditChain address" : "Recipient Ethereum address (0x...)";
    document.getElementById("toAddress").value = "";
    if (mode === "CCT") {
        document.getElementById("modeNote").innerText =
            `Instant internal move. Flat fee: ${weiToEth(ETHBCCTNetworkFeeWei.toString())} ETH (paid from your ETH balance - ETH pays its own fee, never CCT).`;
    } else {
        document.getElementById("modeNote").innerText =
            `${ETH_NETWORK_LABEL.charAt(0).toUpperCase() + ETH_NETWORK_LABEL.slice(1)} broadcast signed entirely in this browser - your private key never leaves it. No CreditChain fee, only the real network gas cost.`;
    }
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

    const account = hdEthAccount();
    if (!account) {
        document.getElementById("realEthAddress").innerText = "No self-custodial Ethereum account found - migrate your wallet first.";
        document.getElementById("realEthBalance").innerText = "N/A";
        document.getElementById("modeDirectBtn").disabled = true;
        return;
    }
    document.getElementById("realEthAddress").innerText = account.address;

    try {
        const info = await apiGet(`/eth-selfcustody/balance?address=${account.address}`);
        latestSelfCustodialInfo = info;
        document.getElementById("realEthBalance").innerText = `${weiToEth(info.weiBalance)} ETH`;
    } catch (e) {
        console.error(e);
        document.getElementById("realEthBalance").innerText = IS_MAINNET
            ? "Couldn't load balance (Ethereum RPC unavailable)"
            : "Couldn't load balance (Sepolia RPC unavailable)";
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

    document.getElementById("reviewNetwork").innerText = mode === "CCT" ? "CCT network" : "Direct";
    document.getElementById("reviewTo").innerText = to;
    document.getElementById("reviewAmount").innerText = `${weiToEth(valueWei.toString())} ETH`;

    const confirmBtn = document.getElementById("confirmSendBtn");
    const gasRow = document.getElementById("reviewGasRow");
    const warningEl = document.getElementById("reviewWarning");

    if (mode === "DIRECT") {
        if (!latestSelfCustodialInfo) {
            statusEl.innerText = "Balance/gas info isn't loaded yet - wait a moment and try again.";
            return;
        }
        const gasPriceWei = BigInt(latestSelfCustodialInfo.suggestedGasPriceWei);
        const feeWei = GAS_LIMIT * gasPriceWei;
        const totalWei = valueWei + feeWei;
        const balanceWei = BigInt(latestSelfCustodialInfo.weiBalance);

        document.getElementById("reviewFee").innerText = `~${weiToEth(feeWei.toString())} ETH (real network gas cost, no CreditChain fee)`;
        gasRow.style.display = "none";
        warningEl.innerHTML = IS_MAINNET
            ? "This sends real ETH directly from your self-custodial account. This action is <strong>irreversible</strong> and cannot be undone or refunded by CreditChain."
            : "This sends real Sepolia testnet ETH directly from your self-custodial account. This action is <strong>irreversible</strong> and cannot be undone or refunded by CreditChain.";
        confirmBtn.disabled = totalWei > balanceWei;
        document.getElementById("reviewStatus").innerText = confirmBtn.disabled
            ? "Amount plus estimated fee exceeds your real ETH balance."
            : "";
        document.getElementById("reviewCard").dataset.feeWei = feeWei.toString();
        document.getElementById("reviewCard").dataset.nonce = latestSelfCustodialInfo.nonce;
        document.getElementById("reviewCard").dataset.gasPriceWei = gasPriceWei.toString();
        showFeeUsd(feeWei);
    } else {
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
        if (sendMode === "DIRECT") {
            const account = hdEthAccount();
            if (!account) {
                statusEl.innerText = "No self-custodial Ethereum account available for this wallet.";
                confirmBtn.disabled = false;
                return;
            }

            statusEl.innerText = "Signing locally...";
            // The only point account.privateKey is ever touched - it
            // never leaves this function, mirrors send-eth.js exactly.
            const { rawTxHex } = await EthTx.signLegacyTx(account.privateKey, {
                nonce: parseInt(reviewCard.dataset.nonce, 10),
                gasPriceWei: reviewCard.dataset.gasPriceWei,
                gasLimit: Number(GAS_LIMIT),
                to,
                valueWei,
                chainId: ETH_CHAIN_ID,
            });

            statusEl.innerText = "Broadcasting...";
            const result = await apiPost("/eth-selfcustody/broadcast", { rawTxHex });

            statusEl.innerHTML = `Sent - <a href="${ETH_EXPLORER_BASE}${result.txHash}" target="_blank" rel="noopener" style="color:var(--cc-accent-gold, #c9a227);">view on ${ETH_EXPLORER_NAME}</a><br>Returning to your wallet shortly...`;

            let attempts = 0;
            const poll = setInterval(async () => {
                attempts++;
                await refreshBalance();
                if (attempts >= 6) clearInterval(poll);
            }, 5000);

            setTimeout(() => {
                location.href = "outernet";
            }, 6000);
        } else if (sendMode === "CCT") {
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

            statusEl.innerText = `Sent (tx ${result.txId}) - takes effect once the next block confirms it. Returning to your wallet...`;
            setTimeout(refreshBalance, 9000);
            setTimeout(() => {
                location.href = "outernet";
            }, 1500);
        }
    } catch (e) {
        statusEl.innerText = "Send failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

// Entry pages can deep-link a starting mode, e.g. asset.html's "ETH"
// Send button links here with ?mode=DIRECT so a self-custodial-first
// user lands on that mode instead of always defaulting to CCT network.
(function initialMode() {
    const requested = new URLSearchParams(window.location.search).get("mode");
    setMode(requested === "DIRECT" ? requested : "CCT");
})();
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
