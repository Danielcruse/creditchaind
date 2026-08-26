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

// dashSmallestToDecimal is a local copy of js/outernet.js's identically-
// named helper - same "each page keeps its own tiny copy rather than
// relying on load order" convention already established there.
function dashSmallestToDecimal(smallestStr, decimals) {
    const n = BigInt(smallestStr || "0");
    const scale = 10n ** BigInt(decimals);
    const whole = n / scale;
    const frac = (n % scale).toString().padStart(decimals, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// loadBalance mirrors outernet.js's loadWallet exactly - see that file's
// doc comment for why the total counts only CCT, YENE, and the three
// vault-backed CCC1 balances, deliberately excluding the real self-
// custodial ETH/USDC/BTCB balances sitting at a receive-only address
// (not spendable within CreditChain until vaulted).
async function loadBalance() {
    if (!wallet) return;

    try {
        const tokenBalance = await apiGet(`/token/balance?address=${wallet.address}`);
        const cctDisplay = cctFromSmallest(tokenBalance.balance);

        const legPromises = [
            fiatValue("CCT", cctDisplay),
            fetchTokenBalance(wallet, "YENE").then((b) => fiatValue("YENE", b.balance)),
            fetchCcc1Balance(wallet, "ETH").then((r) => fiatValue("ETH", dashSmallestToDecimal(r.balance, VAULT_CHAIN_INFO.ETH.decimals))),
            fetchCcc1Balance(wallet, "USDC").then((r) => fiatValue("USDC", dashSmallestToDecimal(r.balance, VAULT_CHAIN_INFO.USDC.decimals))),
            fetchCcc1Balance(wallet, "BTC").then((r) => fiatValue("BTC", dashSmallestToDecimal(r.balance, VAULT_CHAIN_INFO.BTC.decimals))),
        ];

        const legs = await Promise.allSettled(legPromises);

        let total = 0;
        let anyPriced = false;
        for (const leg of legs) {
            if (leg.status === "fulfilled" && leg.value !== null) {
                total += leg.value;
                anyPriced = true;
            }
        }

        if (anyPriced) {
            const currency = getDisplayCurrency();
            document.getElementById("balance").innerText =
                `${currencySymbolFor(currency)}${total.toLocaleString(undefined, { maximumFractionDigits: 2, minimumFractionDigits: 2 })}`;
        } else {
            document.getElementById("balance").innerText = `${cctDisplay} CCT`;
        }
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

// Receive is a two-step flow: pick which token you want to receive,
// then show ONLY that token's CreditChain-network address+QR - see
// outernet.js's openReceivePicker/selectReceiveToken for the same
// pattern (this page uses its own copy since it has its own top-level
// `wallet` var, not shared script state with outernet.js).
const RECEIVE_TOKEN_LABELS = {
    CCT: "CCT",
    ETHB: "ETH (bridged)",
    BTCB: "BTC (bridged)",
    USDCTEST: "USDC (testnet)",
    YENE: "YEN-e",
};

function openReceivePicker() {
    if (!wallet) return;
    document.getElementById("receivePickerOverlay").style.display = "flex";
}

// See js/outernet.js's identical functions for the full doc comment -
// on mainnet, "ETH"/"USDC" are the unified/self-custodial-first pages
// (asset.html?symbol=ETH/USDC, free, no custodial-account fee); on
// testnet these route to the bridged ETHB/USDCTEST pages exactly as
// before.
function receiveEthPicked() {
    closeReceivePicker();
    location.href = window.CC_NETWORK === "mainnet" ? "asset?symbol=ETH" : "asset?symbol=ETHB";
}

function receiveUsdcPicked() {
    if (window.CC_NETWORK === "mainnet") {
        closeReceivePicker();
        location.href = "asset?symbol=USDC";
        return;
    }
    selectReceiveToken("USDCTEST");
}

function closeReceivePicker() {
    document.getElementById("receivePickerOverlay").style.display = "none";
}

function backToReceivePicker() {
    closeQrModal();
    openReceivePicker();
}

async function selectReceiveToken(symbol) {
    if (!wallet) return;
    closeReceivePicker();

    const rawAddress = await balanceAddressForSymbol(wallet, symbol);
    const displayAddress = symbol === "ETHB" ? rawAddress : formatAddress(rawAddress);

    if (navigator.clipboard) {
        navigator.clipboard.writeText(displayAddress).catch(() => {});
    }

    document.getElementById("qrModalTitle").innerText = `Your ${RECEIVE_TOKEN_LABELS[symbol] || symbol} Address`;
    document.getElementById("qrAddressText").innerText = displayAddress;
    QR.renderToCanvas(document.getElementById("qrCanvas"), displayAddress, 6);
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
