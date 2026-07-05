function logout() {
    localStorage.removeItem("loggedIn");
    location.href = "login.html";
}

function resetWallet() {
    if (confirm("Forget this browser's wallet? This does not delete anything on-chain - your address, balances and loans remain on the node, you will just need to sign back in with the same address to see them again.")) {
        clearWallet();
        alert("Local wallet forgotten.");
        location.href = "register.html";
    }
}

async function requestSettingsEmailCode() {
    const email = document.getElementById("settingsRecoveryEmail").value.trim();
    const statusEl = document.getElementById("settingsCodeDisplay");
    if (!email) {
        statusEl.innerText = "Enter an email first.";
        return;
    }
    try {
        const result = await apiPostSigned("/recovery/request-email-code", { email });
        statusEl.innerText = `Code: ${result.code} (${result.note})`;
    } catch (e) {
        statusEl.innerText = "Could not request code: " + e.message;
    }
}

async function confirmSettingsEmail() {
    const email = document.getElementById("settingsRecoveryEmail").value.trim();
    const code = document.getElementById("settingsEmailCode").value.trim();
    const statusEl = document.getElementById("settingsEmailStatus");
    if (!email || !code) {
        statusEl.innerText = "Enter both email and code.";
        return;
    }
    try {
        const result = await apiPostSigned("/recovery/confirm-email", { email, code });
        statusEl.innerText = `Recovery email confirmed (tx ${result.txId}) - this wallet can now be migrated by email + creation year.`;
    } catch (e) {
        statusEl.innerText = "Could not confirm: " + e.message;
    }
}
