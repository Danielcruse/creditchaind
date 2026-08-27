// Same host the page was loaded from, backend port instead of the
// static-file port - works whether this is opened locally
// (localhost:8081 -> localhost:8080) or from a real deployed server
// (server-ip:8081 -> server-ip:8080), without hardcoding either. The
// port itself comes from window.CC_API_PORT (see network-config.js,
// which must load before this file) - defaults to 8080 (today's exact
// behavior) if that file didn't load for some reason, so this degrades
// gracefully rather than breaking a page that's missing the tag.
const API_BASE = `${window.location.protocol}//${window.CC_API_HOST || window.location.hostname}:${window.CC_API_PORT || 8080}`;

// escapeHtml is the ONE shared sanitizer for any user-submitted text
// (a company name/description, a hub trade's payment instructions, a
// chat message, an application note - anything another user typed,
// not this browser's own trusted UI strings) that a page needs to
// render via innerHTML rather than textContent/innerText directly.
// Before this existed, several pages either reimplemented the same
// div.innerText-round-trip trick under a different local name
// (escapeHtmlBuy in buy-cct.js, a page-scoped escapeHtml in admin.js/
// device-lock.js) or, in several real cases, skipped escaping
// entirely - stored XSS in a page that keeps this wallet's private key
// in localStorage means a malicious company/trade/message can silently
// exfiltrate it the moment another logged-in user views that content.
// api.js loads before every other page script (see each page's own
// <script> ordering), so this is always available without a page
// needing its own copy or an extra <script> tag.
function escapeHtml(s) {
    const div = document.createElement("div");
    div.innerText = s == null ? "" : String(s);
    return div.innerHTML;
}

// escapeHtmlMultiline is escapeHtml plus turning newlines into <br> -
// for free-text fields where the author's own line breaks (payment
// instructions, trade terms, a chat message) are part of the actual
// content and shouldn't silently collapse into one line just because
// this now escapes markup. Safe: the \n-to-<br> substitution runs
// AFTER escaping, so it only ever matches the literal newline
// character, never anything from the untrusted input itself.
function escapeHtmlMultiline(s) {
    return escapeHtml(s).replace(/\n/g, "<br>");
}

// Navigates to Mainnet's own domain - a genuinely separate host (see
// network-config.js), not just a different port on this one. One-way
// only, by design: this is exposed on Testnet as "switch to Mainnet"
// and is never wired up on Mainnet itself (showDemoBanner below skips
// building the control entirely when already on Mainnet's host), since
// Mainnet is the official production site and shouldn't advertise a
// path back down to a demo network. Lands on Mainnet's root
// (index.html) rather than a wallet view - Testnet and Mainnet are
// fully independent creditchaind processes with separate wallets/chain
// state, so there's nothing to "carry over"; the user just arrives at
// Mainnet's own landing page and logs in there if they have an account
// on it.
function switchCreditChainNetwork() {
    const mainnet = (window.CC_NETWORK_ENDPOINTS && window.CC_NETWORK_ENDPOINTS.mainnet) || {};
    const host = mainnet.host || window.location.hostname;
    localStorage.setItem("cc_network", "mainnet");

    // Carry the current wallet identity over via a one-time URL
    // fragment - see network-config.js's consumeWalletHandoff for the
    // receiving side and why a fragment (never sent to the server,
    // never logged) is the safe mechanism here. Only attached when a
    // local wallet actually exists - a visitor with none just lands on
    // Mainnet's plain landing page exactly as before, same as always.
    const wallet = getWallet();
    const fragment = wallet ? "#ccwallet=" + encodeURIComponent(JSON.stringify(wallet)) : "";
    location.href = `https://${host}/${fragment}`;
}

// buildDisclosureFooter builds the full disclosure text + whitepaper
// link that used to live in the top banner (see showDemoBanner below) -
// appended once, as document.body's LAST child, on every page that
// loads this script. Kept as a real page footer (not another fixed
// overlay) since this content isn't meant to be glanced at constantly
// like the network pill - it's read once, at the bottom, the way a
// disclosure/terms line normally works.
function buildDisclosureFooter(isMainnet) {
    const footer = document.createElement("footer");
    footer.style.cssText =
        "padding:14px 16px;text-align:center;font-family:Arial,sans-serif;font-size:11px;" +
        "line-height:1.5;color:rgba(255,255,255,0.55);border-top:1px solid rgba(255,255,255,0.12);" +
        "background:transparent;";

    const disclosure = document.createElement("span");
    disclosure.innerText = isMainnet
        ? "⚠ Preliminary internal review complete; full independent professional audit still pending — each account's total BTC + ETH holdings are capped during this stabilization period. "
        : "⚠ Testnet demo, not audited — tokens have no real value. \"ETHTEST\", \"CCT\" and \"USDCTEST\" are test/participation tokens only. ";
    footer.appendChild(disclosure);

    // Original CreditChain whitepaper - linked from every page's footer.
    // Self-hosted as a plain PDF (same downloads/ convention as the
    // source tarball, see validator.html) - the old link opened the
    // Office web viewer against the raw .docx on GitHub, which broke the
    // moment the repo went private (that URL requires public, unauthed
    // access - see project memory on the private-repo decision).
    const whitepaperLink = document.createElement("a");
    whitepaperLink.href = "downloads/CreditChain_Whitepaper_v0.9.pdf";
    whitepaperLink.target = "_blank";
    whitepaperLink.rel = "noopener";
    whitepaperLink.innerText = "Read the original whitepaper";
    whitepaperLink.style.cssText = "color:#ffd166;text-decoration:underline;font-weight:bold;";
    footer.appendChild(whitepaperLink);

    return footer;
}

// Injected on every page that loads this script - a small, glanceable
// network indicator, styled as a compact red pill on mainnet ("Not
// Audited") vs a neutral pill on testnet. Lives in the top-left corner
// as a small floating control (not a full-width bar) so it doesn't
// compete with page content or push everything down - the full
// disclosure paragraph lives in the page footer instead
// (buildDisclosureFooter above).
//
// One-directional by design: only Testnet's pill includes a "Switch to
// Mainnet" control (see switchCreditChainNetwork above). Mainnet is the
// official production site - its pill is a plain disclosure indicator
// with no way back to Testnet, so nothing on Mainnet advertises a demo
// network to switch to.
//
// Starts COLLAPSED (just the warning icon) rather than the full pill -
// a fixed-position full pill was found to sit directly over real page
// content on some layouts (e.g. outernet.html's sidebar logo). Rather
// than chase down and CSS-override every page this could ever overlap,
// the control itself stays small by default (a single emoji glyph
// rarely overlaps anything worth clicking) and expands to the full
// pill only on click - "shrink to stay out of the way, open back up on
// demand" per the explicit request this replaced the always-expanded
// version for.
(function showDemoBanner() {
    const isMainnet = window.CC_NETWORK === "mainnet";
    const banner = document.createElement("div");
    banner.id = "ccNetworkPill";

    const endpoints = window.CC_NETWORK_ENDPOINTS || { testnet: { label: "Testnet" }, mainnet: { label: "Mainnet" } };
    const disclosureTitle = isMainnet
        ? "Preliminary internal review complete; full independent security audit still pending. Each account's total BTC + ETH holdings are capped; see the receive page for the current limit. Full list of known gaps at /security, linked in the page footer. Click to expand."
        : "\"ETHTEST\", \"CCT\" and \"USDCTEST\" are test/participation tokens only, with no real monetary value. Full disclosure in the page footer. Click to expand.";

    const icon = document.createElement("span");
    icon.innerText = isMainnet ? "⚠" : "🧪";
    icon.style.cssText = "cursor:pointer;font-size:14px;";

    const expanded = document.createElement("span");
    expanded.style.cssText = "display:none;align-items:center;";

    const networkPill = document.createElement("span");
    networkPill.innerText = isMainnet ? "⚠ " + endpoints.mainnet.label + " - Not Audited" : "🧪 " + endpoints.testnet.label;
    networkPill.style.cssText =
        "display:inline-block;font-weight:bold;padding:2px 8px;border-radius:10px;margin-right:6px;cursor:pointer;" +
        (isMainnet
            ? "background:rgba(220,38,38,0.25);border:1px solid #f87171;color:#fecaca;"
            : "background:rgba(255,255,255,0.08);border:1px solid rgba(255,255,255,0.25);color:#ffd166;");
    networkPill.title = "Click to collapse.";
    expanded.appendChild(networkPill);

    // Only ever built on Testnet - see the function-level comment above
    // switchCreditChainNetwork for why Mainnet never gets the reverse
    // control.
    if (!isMainnet) {
        const switchButton = document.createElement("button");
        switchButton.type = "button";
        switchButton.innerText = "Switch to " + (endpoints.mainnet ? endpoints.mainnet.label : "Mainnet") + " →";
        switchButton.style.cssText =
            "font-size:11px;font-weight:bold;background:#1a1200;color:#ffd166;" +
            "border:1px solid #ffd166;border-radius:6px;padding:2px 6px;cursor:pointer;";
        switchButton.addEventListener("click", () => switchCreditChainNetwork());
        expanded.appendChild(switchButton);
    }

    banner.appendChild(icon);
    banner.appendChild(expanded);

    // The full disclosure text lives in the page footer (see
    // buildDisclosureFooter above) - the title attribute carries the
    // short version for a hover tooltip in either state.
    banner.title = disclosureTitle;

    function setCollapsed(collapsed) {
        icon.style.display = collapsed ? "inline" : "none";
        expanded.style.display = collapsed ? "none" : "inline-flex";
        banner.style.padding = collapsed ? "4px 6px" : "4px 8px";
    }
    setCollapsed(true);

    // Only the icon (collapsed) or the network-label span (expanded)
    // toggle - the switch button is a sibling of networkPill, not a
    // child, so clicking it doesn't also trigger networkPill's
    // collapse handler.
    icon.addEventListener("click", () => setCollapsed(false));
    networkPill.addEventListener("click", () => setCollapsed(true));

    // position:fixed, small corner control (not a full-width bar) - a
    // compact pill in the top-left corner, per the explicit "move to
    // the corner, top left" request. Deliberately does NOT reserve
    // space via body.paddingTop (unlike the old full-width bar) since
    // its footprint (collapsed: one glyph) is small enough to just
    // float over whatever's in that corner. On narrow/mobile viewports
    // it's nudged down below outernet.html's own sticky mobile topbar
    // (see the injected style rule below) so it never overlaps that
    // page's hamburger button.
    banner.style.cssText +=
        "position:fixed;top:8px;left:8px;z-index:9998;box-sizing:border-box;" +
        "padding:4px 6px;text-align:left;font-family:Arial,sans-serif;font-size:12px;" +
        "line-height:1.3;white-space:nowrap;display:flex;align-items:center;" +
        "background:#3a2a00;color:#ffd166;border:1px solid #ffd166;border-radius:10px;" +
        "box-shadow:0 2px 8px rgba(0,0,0,0.35);";

    // Self-contained media query (works on every page regardless of
    // which stylesheet, if any, that page loads) - pushes the pill
    // below the ~56px-tall sticky mobile topbar (js/mobile-nav.js,
    // css/outernet.css's .mobile-topbar) so it never sits on top of
    // the hamburger button on narrow viewports.
    const cornerStyle = document.createElement("style");
    cornerStyle.innerText = "@media (max-width:900px){ #ccNetworkPill{ top:60px; } }";
    document.addEventListener("DOMContentLoaded", () => {
        document.head.appendChild(cornerStyle);
        document.body.appendChild(banner);
        document.body.appendChild(buildDisclosureFooter(isMainnet));
    });
})();

// --- CCT smallest-unit helpers ---------------------------------------
//
// CCT moved from whole-unit uint64 balances to 18-decimal smallest-unit
// big-int balances (stored/transmitted as decimal STRINGS), mirroring
// how ETHB (wei) already works - see weiToEth in website/js/defi.js.
// Only the CCT-denominated fields that actually changed shape on the
// backend (dedicated CCT balance/supply endpoints, treasury, explorer
// CCT balance, AMM pool CCT reserves/shares/swap amounts) use these;
// generic whole-uint64 CCT amounts (transfers, staking, P2P offers,
// order book, collateral, validator stake) are untouched by this
// migration and must NOT be run through these helpers.

function cctFromSmallest(smallestStr) {
    if (smallestStr === undefined || smallestStr === null || smallestStr === "") return "0";
    const n = BigInt(smallestStr);
    const whole = n / 1000000000000000000n;
    const frac = (n % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

function cctToSmallest(displayStr) {
    // displayStr is a whole-or-decimal CCT amount typed by a user (e.g. "12.5") -> smallest-unit decimal string for the API.
    const s = String(displayStr).trim();
    const [wholePart, fracPartRaw = ""] = s.split(".");
    const fracPart = (fracPartRaw + "000000000000000000").slice(0, 18);
    const whole = BigInt(wholePart || "0");
    const frac = BigInt(fracPart || "0");
    return (whole * 1000000000000000000n + frac).toString();
}

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
const BTCB_ADDRESS_TAG = "BTCB-SUBADDRESS";

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

// ethbAddressCache caches getEthbAddress's resolved address per wallet
// for the lifetime of the page, same spirit as deriveTokenAddress's own
// cache. See getEthbAddress below for the actual resolution order.
const ethbAddressCache = new Map();

// registerEthWatchAddressOnce fires the same free registration
// getEthbAddress does, for call sites that resolve/display the
// self-custodial ETH address directly (assets.js, asset.js) without
// going through getEthbAddress itself - so deposit-detection starts
// watching it the moment a user's own address is ever shown to them,
// not only when something happens to touch the ETHB ledger. Harmless
// to call repeatedly - HandleEthRegisterAddress on the server is a
// no-op re-registration of the same address.
const ethWatchRegistered = new Set();
function registerEthWatchAddressOnce(wallet, ethAddress) {
    if (!ethAddress || ethWatchRegistered.has(wallet.address)) return;
    ethWatchRegistered.add(wallet.address);
    apiPostSigned("/eth-bridge/register-address", { ethAddress }).catch(() => {});
}

// selfCustodialEthAddressFor reads the free, real Ethereum address this
// wallet already derived from its own recovery phrase (see
// hdwallet.js/hd_migrate.js) - no server round-trip needed, since the
// browser already knows it. Returns null for a wallet that hasn't gone
// through HD derivation/migration yet.
// getHdAccounts resolves the browser's cached HD-derived accounts
// (eth/btc), transparently handling two storage shapes:
//   - legacy (pre-dual-network-fix): accounts.btc = {address,...} for
//     whichever single network was active at derivation time - kept
//     working exactly as before, including asset.js's existing
//     "wrong network, re-derive via btc-network-fix.html" fallback for
//     wallets that only ever cached one network.
//   - current: accounts.btc = {testnet:{...}, mainnet:{...}} (both
//     derived together at registration/migration/fix time, see
//     register.html, hd_migrate.js, btc-network-fix.js) - resolved
//     here to whichever network is currently active, so switching
//     networks never requires re-entering the recovery phrase again.
// Returns the same FLAT {eth, btc} shape either way, so every existing
// call site (asset.js, send-*.js, vault-deposit-*.js, etc.) keeps
// working unchanged - only the raw localStorage read is centralized
// here instead of duplicated ~12 times.
function getHdAccounts() {
    let raw;
    try {
        raw = JSON.parse(localStorage.getItem("cc_hd_accounts") || "null");
    } catch (e) {
        return null;
    }
    if (!raw) return null;
    if (raw.btc && (raw.btc.testnet || raw.btc.mainnet)) {
        const network = window.CC_NETWORK === "mainnet" ? "mainnet" : "testnet";
        return { eth: raw.eth, btc: raw.btc[network] || raw.btc.testnet || raw.btc.mainnet || null };
    }
    return raw;
}

function selfCustodialEthAddressFor(wallet) {
    const accounts = getHdAccounts();
    return accounts && accounts.eth ? accounts.eth.address : null;
}

// getEthbAddress resolves "this wallet's one real ETH address" - free
// and self-custodial (see core/eth_address.go's doc comment for why
// this replaced the earlier paid AWS-KMS-custodial design: a real
// deposit to this exact address is what the server watches and credits
// as ETHB, no second address or fee required). Registers the address
// with the server on first resolution (fire-and-forget - deposit
// detection just won't start until this lands, never blocks the
// caller) so the bridge's deposit-detection loop knows to watch it.
// Falls back to the legacy /eth-custodial/address lookup only for a
// wallet that somehow already has a custodial account from before this
// change, then to the old derived-hash placeholder as a last resort.
async function getEthbAddress(wallet) {
    if (ethbAddressCache.has(wallet.address)) {
        return ethbAddressCache.get(wallet.address);
    }

    let resolved = selfCustodialEthAddressFor(wallet);
    if (resolved) {
        apiPostSigned("/eth-bridge/register-address", { ethAddress: resolved }).catch(() => {});
    } else {
        try {
            const info = await apiGet(`/eth-custodial/address?address=${wallet.address}`);
            resolved = info.ethAddress;
        } catch (e) {
            resolved = await deriveTokenAddress(wallet.publicKey, ETHB_ADDRESS_TAG);
        }
    }

    ethbAddressCache.set(wallet.address, resolved);
    return resolved;
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
    if (symbol === "BTCB") return deriveTokenAddress(wallet.publicKey, BTCB_ADDRESS_TAG);
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
    if (symbol === "BTCB") return apiGet(`/btcb/balance?address=${address}`);
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
        window.location.href = "register";
        return null;
    }
    return wallet;
}
