const wallet = requireWallet();

function currentSymbol() {
    return document.getElementById("sendSymbol").value;
}

async function refresh() {
    if (!wallet) return;

    const symbol = currentSymbol();
    document.getElementById("sendSymbolLabel").innerText = symbol;
    document.getElementById("sendAddressHint").style.display = symbol === "CCT" ? "none" : "block";

    try {
        const data = await fetchTokenBalance(wallet, symbol);
        document.getElementById("balance").innerText = data.balance;
    } catch (e) {
        console.error(e);
    }
}

async function send() {
    if (!wallet) return;

    const symbol = currentSymbol();
    const to = document.getElementById("toAddress").value.trim();
    const amount = parseFloat(document.getElementById("amount").value);

    if (!to || to.length !== 40) {
        alert("Enter a valid recipient address.");
        return;
    }

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/token/transfer", {
            to,
            amount,
            symbol,
        });

        document.getElementById("sendStatus").innerText =
            `Sent (tx ${result.txId}) - takes effect once the next block confirms it.`;

        setTimeout(refresh, 9000);
    } catch (e) {
        alert("Send failed: " + e.message);
    }
}

document.getElementById("sendSymbol").addEventListener("change", refresh);

refresh();

// --- QR scanner ---------------------------------------------------
//
// Uses the browser's native BarcodeDetector API - same reasoning as
// using Web Crypto for signing instead of a vendored library: no
// third-party dependency to vet/trust for something reading a wallet
// address. Support is Chrome/Edge (desktop + Android) as of when this
// was written - Safari/Firefox users get a clear message instead of a
// silent failure, same pattern as the signing-unsupported-browser case
// in api.js.

let scanStream = null;
let scanRafId = null;

async function openScanModal() {
    document.getElementById("scanModalOverlay").style.display = "flex";
    const statusEl = document.getElementById("scanStatus");
    statusEl.innerText = "Requesting camera access...";

    if (!("BarcodeDetector" in window)) {
        statusEl.innerText =
            "This browser doesn't support QR scanning (BarcodeDetector API) - try a recent Chrome or Edge, or paste the address manually.";
        return;
    }

    try {
        scanStream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
    } catch (e) {
        statusEl.innerText = "Camera access denied or unavailable: " + e.message;
        return;
    }

    const video = document.getElementById("scanVideo");
    video.srcObject = scanStream;
    statusEl.innerText = "Point your camera at a wallet address QR code.";

    const detector = new BarcodeDetector({ formats: ["qr_code"] });

    async function scanFrame() {
        if (!scanStream) return;

        try {
            const codes = await detector.detect(video);
            if (codes.length > 0) {
                const text = (codes[0].rawValue || "").trim();
                if (/^[0-9a-f]{40}$/i.test(text)) {
                    document.getElementById("toAddress").value = text;
                    document.getElementById("sendStatus").innerText = "Address scanned.";
                    closeScanModal();
                    return;
                }
                statusEl.innerText = "That QR code isn't a recognized wallet address - keep trying.";
            }
        } catch (e) {
            console.error(e);
        }

        scanRafId = requestAnimationFrame(scanFrame);
    }
    scanRafId = requestAnimationFrame(scanFrame);
}

function closeScanModal() {
    document.getElementById("scanModalOverlay").style.display = "none";

    if (scanRafId) {
        cancelAnimationFrame(scanRafId);
        scanRafId = null;
    }
    if (scanStream) {
        scanStream.getTracks().forEach((t) => t.stop());
        scanStream = null;
    }
}
