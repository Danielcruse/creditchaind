// Same host the page was loaded from, backend port instead of the
// static-file port - works whether this is opened locally
// (localhost:8081 -> localhost:8080) or from a real deployed server
// (server-ip:8081 -> server-ip:8080), without hardcoding either.
const API_BASE = `${window.location.protocol}//${window.location.hostname}:8080`;

// Injected on every page that loads this script, so anyone clicking
// through the demo (e.g. an investor) sees an honest, unmissable
// statement of what this actually is - a testnet with disclosed,
// unaudited simplifications, not a live network with real value at
// stake. Keep this until there's been a real professional security
// audit.
(function showDemoBanner() {
    const banner = document.createElement("div");
    banner.innerText = "⚠ Testnet demo, not audited — tokens have no real value.";
    banner.title =
        "\"ETHTEST\", \"CCT\" and \"USDCTEST\" are test/participation tokens only, with no real monetary value.";
    // position:fixed (not sticky) is deliberate - some pages (e.g.
    // outernet.html) set body{display:flex} for their own sidebar/main
    // layout. A sticky (or any in-flow) banner inserted as body's
    // first child becomes an unintended THIRD flex item there: its
    // inline width:100% is read as a flex-basis claiming the entire
    // row, and default align-items:stretch makes it stretch to match
    // its tallest sibling - covering the whole page. Fixed positioning
    // removes it from flow entirely (block or flex) on every page, so
    // it can never interfere with a host page's own layout. The
    // tradeoff is it no longer reserves space in flow, so body gets an
    // explicit padding-top below sized to the banner's own height.
    banner.style.cssText =
        "position:fixed;top:0;left:0;z-index:9999;width:100%;max-width:100%;box-sizing:border-box;" +
        "padding:6px 10px;text-align:center;font-family:Arial,sans-serif;font-size:12px;" +
        "line-height:1.3;white-space:normal;overflow-wrap:anywhere;" +
        "background:#3a2a00;color:#ffd166;border-bottom:1px solid #ffd166;";
    document.addEventListener("DOMContentLoaded", () => {
        document.body.insertBefore(banner, document.body.firstChild);
        document.body.style.paddingTop = banner.offsetHeight + "px";
        // The banner's height isn't fixed - its text wraps to a second
        // line on narrow viewports, and paddingTop was previously only
        // ever set once here. Resizing the window, rotating a phone,
        // or zooming after load could all change how many lines the
        // banner wraps to without body.paddingTop ever being
        // recalculated, so the banner's second line would sit on top
        // of (overlap) the page content below it. Both a window
        // resize listener and a ResizeObserver on the banner itself
        // keep paddingTop in sync for the lifetime of the page, not
        // just at initial load - belt and suspenders, since not every
        // environment reflows a fixed-position element's box in a way
        // ResizeObserver alone reliably picks up.
        const syncPadding = () => {
            document.body.style.paddingTop = banner.offsetHeight + "px";
        };
        window.addEventListener("resize", syncPadding);
        new ResizeObserver(syncPadding).observe(banner);
    });
})();

async function apiGet(path) {
    const res = await fetch(API_BASE + path);
    if (!res.ok) {
        throw new Error((await res.text()) || `GET ${path} failed (${res.status})`);
    }
    return res.json();
}

async function apiPost(path, body) {
    const res = await fetch(API_BASE + path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body || {}),
    });
    if (!res.ok) {
        throw new Error((await res.text()) || `POST ${path} failed (${res.status})`);
    }
    return res.json();
}

// --- Signed requests -------------------------------------------------
//
// Every mutating endpoint now requires proof the caller actually
// controls the wallet's private key, not just a claimed address in
// the request body. apiPostSigned wraps the payload in a signed
// envelope the server verifies (see core/request_auth.go).

const signingKeyCache = new Map();

function hexToBytes(hex) {
    const bytes = new Uint8Array(hex.length / 2);
    for (let i = 0; i < bytes.length; i++) {
        bytes[i] = parseInt(hex.substr(i * 2, 2), 16);
    }
    return bytes;
}

function bytesToHex(bytes) {
    return Array.from(bytes)
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
}

function base64url(bytes) {
    let str = btoa(String.fromCharCode(...bytes));
    return str.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function importSigningKey(wallet) {
    if (signingKeyCache.has(wallet.address)) {
        return signingKeyCache.get(wallet.address);
    }

    if (!window.crypto || !window.crypto.subtle || !window.crypto.subtle.importKey) {
        throw new Error(
            "This browser does not support the Web Crypto API needed to sign transactions. Please use a current version of Chrome, Firefox, or Safari."
        );
    }

    // Go's ed25519.PrivateKey is seed(32 bytes) || publicKey(32 bytes).
    // WebCrypto's Ed25519 JWK import only wants the seed as "d".
    const privBytes = hexToBytes(wallet.privateKey);
    const seed = privBytes.slice(0, 32);
    const pubBytes = hexToBytes(wallet.publicKey);

    const jwk = {
        kty: "OKP",
        crv: "Ed25519",
        d: base64url(seed),
        x: base64url(pubBytes),
        key_ops: ["sign"],
        ext: true,
    };

    let key;
    try {
        key = await window.crypto.subtle.importKey("jwk", jwk, { name: "Ed25519" }, false, ["sign"]);
    } catch (e) {
        throw new Error(
            "This browser does not support Ed25519 signing (Web Crypto). Please use a current version of Chrome, Firefox, or Safari."
        );
    }

    signingKeyCache.set(wallet.address, key);
    return key;
}

async function apiPostSigned(path, payload) {
    return apiPostSignedRaw(path, JSON.stringify(payload || {}));
}

// apiPostSignedRaw signs and posts a pre-built JSON payload STRING,
// rather than an object apiPostSigned would run through
// JSON.stringify. Needed whenever a field carries a value
// JSON.stringify can't represent losslessly - e.g. a wei-scale amount
// (ETHB, bridge withdrawals) that exceeds JS's 2^53 safe-integer
// range: constructing it as a plain JS Number silently rounds it
// before serialization ever runs, so the caller must build the exact
// decimal digit string itself (typically via BigInt) and hand it to
// this function directly instead.
async function apiPostSignedRaw(path, payloadJSON) {
    const wallet = requireWallet();
    if (!wallet) throw new Error("no wallet");

    const key = await importSigningKey(wallet);

    const timestamp = Math.floor(Date.now() / 1000);
    const canonical = `${wallet.address}|${wallet.publicKey}|${timestamp}|${payloadJSON}`;

    const sigBuffer = await window.crypto.subtle.sign(
        { name: "Ed25519" },
        key,
        new TextEncoder().encode(canonical)
    );
    const signature = bytesToHex(new Uint8Array(sigBuffer));

    // Built manually (not via JSON.stringify on an object containing
    // the payload) so the payload bytes embedded here are guaranteed
    // byte-identical to what was actually signed above.
    const envelopeJSON =
        `{"address":${JSON.stringify(wallet.address)},` +
        `"publicKey":${JSON.stringify(wallet.publicKey)},` +
        `"timestamp":${timestamp},` +
        `"payload":${payloadJSON},` +
        `"signature":${JSON.stringify(signature)}}`;

    const res = await fetch(API_BASE + path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: envelopeJSON,
    });
    if (!res.ok) {
        throw new Error((await res.text()) || `POST ${path} failed (${res.status})`);
    }
    return res.json();
}

// --- ETHTEST/USDCTEST derived sub-addresses --------------------------
//
// ETHTEST and USDCTEST are "imported tokens" with their own address
// namespace, derived from the same public key as the wallet's normal
// CCT address but NOT interchangeable with it (see
// core.DeriveTokenAddress's doc comment on the Go side - this must
// compute byte-identical output: SHA256(tag + ":" + publicKeyBytes),
// truncated to the first 20 bytes, hex-encoded).
const ETHTEST_ADDRESS_TAG = "ETHTEST-SUBADDRESS";
const USDCTEST_ADDRESS_TAG = "USDCTEST-SUBADDRESS";
const YENE_ADDRESS_TAG = "YENE-SUBADDRESS";
const ETHB_ADDRESS_TAG = "ETHB-SUBADDRESS";

const subAddressCache = new Map();

async function deriveTokenAddress(publicKeyHex, tag) {
    const cacheKey = `${tag}:${publicKeyHex}`;
    if (subAddressCache.has(cacheKey)) {
        return subAddressCache.get(cacheKey);
    }

    const tagBytes = new TextEncoder().encode(tag + ":");
    const pubBytes = hexToBytes(publicKeyHex);
    const data = new Uint8Array(tagBytes.length + pubBytes.length);
    data.set(tagBytes, 0);
    data.set(pubBytes, tagBytes.length);

    const digest = new Uint8Array(await window.crypto.subtle.digest("SHA-256", data));
    const address = bytesToHex(digest.slice(0, 20));

    subAddressCache.set(cacheKey, address);
    return address;
}

function getEthTestAddress(wallet) {
    return deriveTokenAddress(wallet.publicKey, ETHTEST_ADDRESS_TAG);
}

function getUsdcTestAddress(wallet) {
    return deriveTokenAddress(wallet.publicKey, USDCTEST_ADDRESS_TAG);
}

function getYenAddress(wallet) {
    return deriveTokenAddress(wallet.publicKey, YENE_ADDRESS_TAG);
}

function getEthbAddress(wallet) {
    return deriveTokenAddress(wallet.publicKey, ETHB_ADDRESS_TAG);
}

// balanceAddressForSymbol mirrors api/subaddress.go's
// resolveBalanceAddress on the frontend - CCT/REPT/custom tokens stay
// on the wallet's own address, only ETHTEST/USDCTEST/YENE/ETHB resolve
// to their derived sub-address.
async function balanceAddressForSymbol(wallet, symbol) {
    if (symbol === "ETHTEST") return getEthTestAddress(wallet);
    if (symbol === "USDCTEST") return getUsdcTestAddress(wallet);
    if (symbol === "YENE") return getYenAddress(wallet);
    if (symbol === "ETHB") return getEthbAddress(wallet);
    return wallet.address;
}

// fetchTokenBalance looks up any symbol's balance at the correct
// endpoint AND the correct address (resolving ETHTEST/USDCTEST's
// derived sub-address first) - the one place every page that needs a
// generic "balance of this symbol for this wallet" lookup should call,
// instead of each duplicating the per-symbol endpoint/address logic.
async function fetchTokenBalance(wallet, symbol) {
    const address = await balanceAddressForSymbol(wallet, symbol);

    if (symbol === "CCT") return apiGet(`/token/balance?address=${address}`);
    if (symbol === "REPT") return apiGet(`/rept/balance?address=${address}`);
    if (symbol === "ETHTEST") return apiGet(`/ethtest/balance?address=${address}`);
    if (symbol === "USDCTEST") return apiGet(`/usdctest/balance?address=${address}`);
    if (symbol === "YENE") return apiGet(`/yene/balance?address=${address}`);
    if (symbol === "ETHB") return apiGet(`/ethb/balance?address=${address}`);
    return apiGet(`/tokens/balance?symbol=${encodeURIComponent(symbol)}&address=${address}`);
}

// formatAddress/normalizeAddress are purely a DISPLAY-layer convention
// - every address is still the same raw 40-hex-char value the backend
// has always used (SHA256(pubkey)[:20], see core/address.go); nothing
// server-side changed. formatAddress is what every page should call
// before SHOWING an address to a user (so every address a user sees
// visibly starts with "cc", easy to recognize as a CreditChain
// address). normalizeAddress is what every page should call on
// anything a user TYPES/PASTES/scans before sending it to the API, so
// a "cc"-prefixed address a user copies from elsewhere in this app
// still resolves correctly. This two-way convention is deliberately
// cosmetic/reversible rather than a real protocol change - safe to
// introduce without touching a single already-registered production
// wallet's real address.
function formatAddress(address) {
    if (!address) return address;
    return address.toLowerCase().startsWith("cc") ? address : "cc" + address;
}

function normalizeAddress(address) {
    if (!address) return address;
    return address.toLowerCase().startsWith("cc") ? address.slice(2) : address;
}

// copyToClipboard + copyIconHTML are the one shared implementation
// every address-copy affordance on the site should use, instead of
// each page re-implementing its own silent navigator.clipboard call
// (which is how outernet.html's wallet chip worked before - it copied
// on click with zero visible feedback, indistinguishable from doing
// nothing). copyIconHTML emits a small inline button; pass it the
// exact text to copy (already formatted, e.g. via formatAddress) and
// a unique id so multiple icons on one page don't collide.
function copyToClipboard(text, iconId) {
    if (!navigator.clipboard) return;
    navigator.clipboard.writeText(text).then(() => {
        const el = document.getElementById(iconId);
        if (!el) return;
        const original = el.innerText;
        el.innerText = "✓";
        el.classList.add("copy-icon-done");
        setTimeout(() => {
            el.innerText = original;
            el.classList.remove("copy-icon-done");
        }, 1400);
    }).catch(() => {});
}

function copyIconHTML(text, iconId) {
    const safeText = String(text).replace(/'/g, "\\'");
    return `<button type="button" class="copy-icon" id="${iconId}" title="Copy address" onclick="event.stopPropagation();copyToClipboard('${safeText}','${iconId}')">&#128203;</button>`;
}

function getWallet() {
    try {
        return JSON.parse(localStorage.getItem("cc_wallet"));
    } catch (e) {
        return null;
    }
}

function setWallet(wallet) {
    localStorage.setItem("cc_wallet", JSON.stringify(wallet));
}

function clearWallet() {
    localStorage.removeItem("cc_wallet");
}

function getWalletAddress() {
    const wallet = getWallet();
    return wallet ? wallet.address : null;
}

// requireWallet redirects to registration when the browser has no
// on-chain wallet yet, since every real page needs an address to
// query the shared node state for.
function requireWallet() {
    const wallet = getWallet();
    if (!wallet || !wallet.address) {
        window.location.href = "register.html";
        return null;
    }
    return wallet;
}
