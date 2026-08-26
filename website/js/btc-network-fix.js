// btc-network-fix.js - re-derives the self-custodial BTC account for
// whichever network the user is currently on. Bitcoin mainnet/testnet
// addresses come from genuinely different BIP-84 derivation paths
// (coin type 0' vs 1'), so they are different keys entirely, not just
// different address encodings of the same key - switching the network
// selector alone can never produce the right one. The mnemonic is
// deliberately never persisted (shown once at wallet creation, then
// discarded - see hd_migrate.js), so this requires the user to
// re-enter it once; it never leaves the browser and nothing here ever
// transmits it anywhere.

const wallet = requireWallet();

function currentBtcAccount() {
    const accounts = getHdAccounts();
    return accounts && accounts.btc ? accounts.btc : null;
}

function loadCurrentState() {
    const network = window.CC_NETWORK === "mainnet" ? "mainnet" : "testnet";
    document.getElementById("btcfixNetworkLabel").innerText = network === "mainnet" ? "Mainnet" : "Testnet";
    const account = currentBtcAccount();
    document.getElementById("btcfixCurrentAddress").innerText = account ? account.address : "No BTC account derived yet.";
}

async function confirmSwitch() {
    const statusEl = document.getElementById("btcfixStatus");
    const phrase = document.getElementById("btcfixPhrase").value.trim().replace(/\s+/g, " ");

    statusEl.innerText = "Validating recovery phrase...";

    const valid = await HDWallet.validateMnemonic(phrase);
    if (!valid) {
        statusEl.innerText = "That doesn't look like a valid 12-word recovery phrase - check for typos.";
        return;
    }

    try {
        const seed = await HDWallet.mnemonicToSeed(phrase, "");
        const creditchain = await HDWallet.deriveCreditChainKeypair(seed);

        // Safety check: confirm this phrase actually belongs to the
        // wallet currently logged in here, not a different one typed
        // by mistake - prevents silently linking a stranger's (or the
        // wrong) BTC key into this account.
        if (creditchain.address.toLowerCase() !== wallet.address.toLowerCase()) {
            statusEl.innerText = "This recovery phrase doesn't match your current wallet - check you typed it correctly.";
            return;
        }

        // Derive BOTH networks now, while the phrase is in hand - this
        // is meant to be a ONE-TIME backfill (see this file's top
        // comment): after this, getHdAccounts() in api.js resolves
        // whichever network is active automatically, and this flow
        // should never need to run again for this wallet.
        const btcTestnet = await HDWallet.deriveBtcAddress(seed, "testnet");
        const btcMainnet = await HDWallet.deriveBtcAddress(seed, "mainnet");
        const network = window.CC_NETWORK === "mainnet" ? "mainnet" : "testnet";
        const btc = network === "mainnet" ? btcMainnet : btcTestnet;

        const accounts = JSON.parse(localStorage.getItem("cc_hd_accounts") || "{}") || {};
        accounts.btc = { testnet: btcTestnet, mainnet: btcMainnet };
        localStorage.setItem("cc_hd_accounts", JSON.stringify(accounts));

        statusEl.innerText = `Done - your ${network} BTC address is now ${btc.address}. Returning...`;
        setTimeout(() => {
            location.href = "outernet";
        }, 2000);
    } catch (e) {
        console.error(e);
        statusEl.innerText = "Couldn't derive an account from that phrase: " + e.message;
    }
}

loadCurrentState();
