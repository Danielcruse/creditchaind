// wallet.html - the full-page version of outernet.html's wallet hero,
// split out per the user's request to separate wallet/assets/profile/
// network into their own pages, same pattern as Explorer/Marketplace
// already being separate pages. Reuses the exact same hero markup/CSS
// (.wallet-hero etc. in css/outernet.css) and js/assets.js for the
// Assets card, so there's no duplicated balance-fetching logic.

const wallet = requireWallet();

if (wallet) {
    document.getElementById("wallet").innerText = formatAddress(wallet.address);

    const username = localStorage.getItem("username");
    if (username) {
        document.getElementById("username").innerText = username;
    }
}

async function loadBalance() {
    if (!wallet) return;
    try {
        const tokenBalance = await apiGet(`/token/balance?address=${wallet.address}`);
        document.getElementById("balance").innerText = `${tokenBalance.balance} CCT`;
    } catch (e) {
        console.error(e);
    }
}

function copyReferralLink() {
    const link = document.getElementById("referralLink").innerText;
    if (navigator.clipboard) {
        navigator.clipboard.writeText(link).catch(() => {});
    }
}

async function loadReferrals() {
    if (!wallet) return;
    document.getElementById("referralLink").innerText =
        `${location.origin}/register.html?ref=${wallet.address}`;
    try {
        const r = await apiGet(`/referrals?address=${wallet.address}`);
        document.getElementById("referralCount").innerText = r.referralCount;
    } catch (e) {
        console.error(e);
    }
}

function showReceiveAddress() {
    if (!wallet) return;

    if (navigator.clipboard) {
        navigator.clipboard.writeText(formatAddress(wallet.address)).catch(() => {});
    }

    document.getElementById("qrAddressText").innerText = formatAddress(wallet.address);
    QR.renderToCanvas(document.getElementById("qrCanvas"), formatAddress(wallet.address), 6);
    document.getElementById("qrModalOverlay").style.display = "flex";
}

function closeQrModal() {
    document.getElementById("qrModalOverlay").style.display = "none";
}

async function claimFaucet() {
    if (!wallet) return;

    try {
        const result = await apiPostSigned("/faucet", {});
        document.getElementById("faucetStatus").innerText =
            `Faucet claim pending confirmation (tx ${result.txId}) - ${result.amount} CCT, ${result.ethTestAmount} ETHTEST, and ${result.usdcTestAmount} USDCTEST will land shortly (ETHTEST/USDCTEST go to your imported-token address).`;
        setTimeout(loadBalance, 9000);
    } catch (e) {
        document.getElementById("faucetStatus").innerText = e.message;
    }
}

loadBalance();
setInterval(loadBalance, 5000);
loadReferrals();
