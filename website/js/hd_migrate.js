// hd_migrate.js - prompts a legacy (pre-Multi-Chain-HD-Wallet) wallet
// to migrate to a new HD-derived address, signed by the OLD key the
// user still controls (see core/transactions.go's TxHDWalletMigrate).
// This can't be silent/automatic - a self-custodial wallet's new
// mnemonic has to be generated in the user's own browser and shown to
// them, so this is an explicit, user-driven flow prompted on login.
//
// Deliberately does not declare a top-level `wallet` const (outernet.js
// already does) - see borrow.js's header comment for why that
// collision is fatal.

function hdMigrateLegacyWallet() {
    const w = getWallet();
    return w && w.address && w.kind !== "hd" ? w : null;
}

(function initHDMigratePrompt() {
    const legacy = hdMigrateLegacyWallet();
    if (!legacy) return;
    document.getElementById("hdMigrateOverlay").style.display = "flex";
})();

function dismissHDMigratePrompt() {
    document.getElementById("hdMigrateOverlay").style.display = "none";
}

document.getElementById("hdMigratePhraseAck") && document.getElementById("hdMigratePhraseAck").addEventListener("change", (e) => {
    document.getElementById("hdMigrateContinueBtn").disabled = !e.target.checked;
});

async function startHDMigration() {
    const legacy = hdMigrateLegacyWallet();
    if (!legacy) return;

    const startBtn = document.getElementById("hdMigrateStartBtn");
    const statusEl = document.getElementById("hdMigrateStatus");
    startBtn.disabled = true;
    statusEl.innerText = "Generating your new wallet...";

    try {
        const mnemonic = await HDWallet.generateMnemonic();
        const accounts = await HDWallet.deriveAllAccounts(mnemonic, "");

        statusEl.innerText = "Registering new address...";
        await apiPost("/wallet/create", {
            address: accounts.creditchain.address,
            publicKey: accounts.creditchain.publicKey,
        });

        // Signed with the OLD key - getWallet() still returns the
        // legacy wallet at this point, since setWallet() for the new
        // one hasn't been called yet. That's required: verifyTxAuth
        // for TxHDWalletMigrate rejects anything not signed by the
        // address being migrated away from.
        statusEl.innerText = "Authorizing migration with your current wallet...";
        await apiPostSigned("/wallet/hd-migrate", {
            newAddress: accounts.creditchain.address,
            newPublicKey: accounts.creditchain.publicKey,
        });

        setWallet({
            address: accounts.creditchain.address,
            publicKey: accounts.creditchain.publicKey,
            privateKey: accounts.creditchain.privateKey,
            kind: "hd",
        });
        localStorage.setItem("cc_hd_accounts", JSON.stringify({
            eth: accounts.eth,
            btc: accounts.btc,
        }));

        document.getElementById("hdMigrateIntro").style.display = "none";
        document.getElementById("hdMigrateResult").style.display = "block";
        document.getElementById("hdMigratePhraseText").innerText = mnemonic;
    } catch (e) {
        statusEl.innerText = "Migration failed: " + e.message + " - your old wallet is unchanged, try again.";
        startBtn.disabled = false;
    }
}
