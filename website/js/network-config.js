// network-config.js - runtime-switchable network selection
// (localStorage-backed), replacing the earlier "one static file swapped
// per deployment" model. Testnet and Mainnet are two fully independent
// creditchaind processes (separate DATA_DIR, separate chain, separate
// validator pool) reachable on different ports of the SAME website
// deployment - switching here is the equivalent of MetaMask's network
// dropdown, not a different site. See api.js's showDemoBanner() for the
// actual selector control users interact with.
//
// Defaults to "testnet" whenever nothing's been chosen yet (fresh
// browser, cleared storage) - today's exact behavior, so no existing
// bookmark/session is surprised by this becoming switchable.
//
// Must load BEFORE api.js (and before any other script that reads these
// globals) - every HTML page's script tag order already puts api.js
// first among the shared scripts, so this file's own tag goes even
// earlier than that.
window.CC_NETWORK_ENDPOINTS = {
    testnet: { label: "Testnet", port: 8080 },
    // Mainnet runs on a genuinely separate host (a different AWS account/
    // region entirely, not just a different port on this same box - the
    // original same-host port-9090 plan hit an unresolvable firewall lockout
    // on the original account). "host" here is an explicit override; if a
    // network entry omits "host", api.js falls back to window.location.hostname
    // (same-host-different-port), which is what testnet still uses.
    mainnet: { label: "Mainnet", host: "service.creditchain.site", port: 9090 },
};

// Default when nothing's been chosen yet (fresh browser, cleared
// storage): match the network whose OWN configured host this page is
// actually being served from (e.g. a first-time visit to
// service.creditchain.site should default to mainnet, not testnet -
// nothing on that box even listens on testnet's port), falling back to
// "testnet" otherwise. This preserves the original goal (no existing
// creditchain.site bookmark/session gets surprised - that host has no
// mainnet entry to match, so it falls through to testnet exactly as
// before) while fixing mainnet's own domain defaulting to a network
// whose API port isn't even reachable from this host.
function ccDefaultNetworkForHost() {
    for (var key in window.CC_NETWORK_ENDPOINTS) {
        if (window.CC_NETWORK_ENDPOINTS[key].host === window.location.hostname) {
            return key;
        }
    }
    return "testnet";
}

// Self-healing against stale/poisoned storage: an earlier version of
// switchCreditChainNetwork() (api.js) wrote "mainnet" into whichever
// origin was calling it, including Testnet's own - anyone who clicked
// the old broken toggle got cc_network permanently stuck at "mainnet"
// in their Testnet browser storage, showing Mainnet-styled content on
// creditchain.site forever, with no way for them to know to clear it.
// The actual hostname is the one thing that can never lie about which
// network this is, so it's now the tiebreaker: a stored value is only
// trusted when it's consistent with the host actually being loaded
// from. Concretely - if this host doesn't match Mainnet's own
// configured host, "mainnet" cannot be a genuine, current selection
// for it (nothing on this box even serves that network), so it's
// treated as exactly the leftover poison described above and
// corrected back to this host's real default, the same as if storage
// had never been set. The corrected value is written back so this
// self-heals permanently on the very next load, not just this one.
var ccStoredNetwork = localStorage.getItem("cc_network");
var ccIsMainnetHost = window.location.hostname === window.CC_NETWORK_ENDPOINTS.mainnet.host;
var ccSelectedNetwork;
if (ccIsMainnetHost) {
    // Being on Mainnet's own domain is unambiguous regardless of
    // whatever a prior visit (or a synced/shared browser profile)
    // left in storage.
    ccSelectedNetwork = "mainnet";
} else if (ccStoredNetwork === "testnet") {
    ccSelectedNetwork = "testnet";
} else {
    // Covers both "nothing stored yet" and the poisoned
    // "mainnet"-while-not-on-Mainnet's-host case - both correctly
    // resolve to this host's real default.
    ccSelectedNetwork = ccDefaultNetworkForHost();
}
if (ccSelectedNetwork !== ccStoredNetwork) {
    localStorage.setItem("cc_network", ccSelectedNetwork);
}
window.CC_NETWORK = ccSelectedNetwork;
window.CC_API_PORT = window.CC_NETWORK_ENDPOINTS[ccSelectedNetwork].port;
window.CC_API_HOST = window.CC_NETWORK_ENDPOINTS[ccSelectedNetwork].host || window.location.hostname;

// Mainnet-only plain-white flattening for the light-themed pages
// (outernet.html/wallet.html/assets.html/asset.html - anything with
// body.outernet-light already set as a static HTML class). Scoped by
// checking for that existing class rather than listing pages here, so
// this stays a one-line addition instead of a per-page script tag.
// Testnet keeps the existing mild mint tint unchanged - see
// css/outernet.css's body.outernet-light.outernet-mainnet rule.
if (ccSelectedNetwork === "mainnet" && document.body && document.body.classList.contains("outernet-light")) {
    document.body.classList.add("outernet-mainnet");
}

// --- Cross-network wallet handoff -------------------------------------
//
// Consumes a one-time wallet handoff carried in the URL fragment by
// switchCreditChainNetwork() (api.js) - fragments are never sent to the
// server (not part of the HTTP request line, never logged server-side),
// so this is the standard safe pattern for a client-side-only
// cross-origin secret handoff. Lets the same private key work on both
// networks without a manual re-import: Testnet and Mainnet are separate
// creditchaind processes with separate wallet stores (see this file's
// own doc comment above), but the identity itself (an address/keypair)
// isn't network-specific - only the balance/reputation attached to it
// is, and that's expected to start fresh on whichever network the
// address hasn't transacted on yet, exactly like importing the same
// private key into login.html manually already behaves today.
//
// Runs here (before api.js) rather than in api.js itself, since several
// pages redirect to register.html the instant they find no local
// wallet (requireWallet(), api.js) - this has to consume and store the
// handoff before that check ever runs. Writes directly to localStorage
// rather than calling setWallet() (api.js) since this file loads first
// and that function isn't defined yet.
(function consumeWalletHandoff() {
    if (!location.hash || location.hash.indexOf("#ccwallet=") !== 0) return;

    try {
        const payload = decodeURIComponent(location.hash.slice("#ccwallet=".length));
        const wallet = JSON.parse(payload);
        if (wallet && wallet.address && wallet.privateKey) {
            localStorage.setItem("cc_wallet", JSON.stringify(wallet));
        }
    } catch (e) {
        // Malformed/tampered fragment - ignore; the page falls through
        // to its normal no-wallet behavior (e.g. requireWallet()'s
        // redirect to register.html).
    }

    // Strip the fragment immediately regardless of parse success - a
    // private key must never sit visibly in the URL bar or browser
    // history for even one render.
    history.replaceState(null, "", location.pathname + location.search);
})();
