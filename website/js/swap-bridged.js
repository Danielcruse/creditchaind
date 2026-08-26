// swap-bridged.js - fair-value swap between any real internal "vault"
// balance a bridge deposit actually creates (BTCB, ETHB) plus YENE -
// see api/swap_fair_value.go's doc comment. Priced off the live
// external oracle, NOT a floating CCT pool - CCT only appears as an
// accounting-unit figure shown for transparency.

const wallet = requireWallet();

// ETH_CCC1/BTC_CCC1 are deliberately NOT offered here on mainnet - see
// api/swap_fair_value.go's fairValueSwapSymbols doc comment for the
// real reason (the endpoint mints on demand with no check against
// real vault reserves, which is only safe for YEN-e/the legacy mirror
// tokens, never for assets meant to be redeemable 1:1 for something
// actually locked). Real ETH/BTC vault-to-vault trades go through the
// Trading Hub marketplace instead (see mainnetRedirectCard in
// swap-bridged.html) - this page still offers YEN-e on mainnet, just
// with nothing real to pair it against, so the form itself stays
// hidden there (see gateMainnetVaultSwap below). Testnet keeps
// offering BTCB/ETHB/YENE unchanged, since none of those ever claimed
// real 1:1 backing in the first place.
const SWAP_SYMBOLS = window.CC_NETWORK === "mainnet"
    ? ["YENE"]
    : ["BTCB", "ETHB", "YENE"];
const SWAP_LABELS = {
    BTCB: "BTC (bridged)", ETHB: "ETH (bridged)",
    YENE: "YEN-e",
};

// gateMainnetVaultSwap hides the swap form entirely on mainnet (there's
// nothing left to pair YEN-e against through this endpoint) and shows
// a redirect to the Trading Hub marketplace instead.
function gateMainnetVaultSwap() {
    if (window.CC_NETWORK !== "mainnet") return;
    document.getElementById("formCard").style.display = "none";
    document.getElementById("mainnetRedirectCard").style.display = "block";
    const introText = document.getElementById("swapIntroText");
    if (introText) {
        introText.innerText =
            "This page previously let you swap directly between vault-backed ETH and BTC - that path could mint tokens with no real deposit behind them, so it's been replaced. Trade real ETH/BTC through the Trading Hub marketplace below.";
    }
}

function weiToEth(weiString) {
    const wei = BigInt(weiString || "0");
    const whole = wei / 1000000000000000000n;
    const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// ethToWei parses a decimal ETHB string (e.g. "0.05") into a wei
// BigInt without floating-point precision loss - mirrors
// send-bridged-eth.js's helper of the same name.
function ethToWei(ethStr) {
    const [wholeStr, fracStr = ""] = ethStr.trim().split(".");
    const whole = BigInt(wholeStr || "0");
    const fracPadded = (fracStr + "0".repeat(18)).slice(0, 18);
    return whole * 1000000000000000000n + BigInt(fracPadded || "0");
}

function satsToBtc(sats) {
    const n = BigInt(sats || 0);
    const whole = n / 100000000n;
    const frac = (n % 100000000n).toString().padStart(8, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

function btcToSats(btcStr) {
    const [wholeStr, fracStr = ""] = btcStr.trim().split(".");
    const whole = BigInt(wholeStr || "0");
    const fracPadded = (fracStr + "0".repeat(8)).slice(0, 8);
    return whole * 100000000n + BigInt(fracPadded || "0");
}

function populateFromOptions() {
    const fromSelect = document.getElementById("fromSymbol");
    SWAP_SYMBOLS.forEach((s) => {
        const opt = document.createElement("option");
        opt.value = s;
        opt.innerText = SWAP_LABELS[s];
        fromSelect.appendChild(opt);
    });
}

function populateToOptions() {
    const fromSymbol = document.getElementById("fromSymbol").value;
    const toSelect = document.getElementById("toSymbol");
    const previous = toSelect.value;
    toSelect.innerHTML = "";
    SWAP_SYMBOLS.filter((s) => s !== fromSymbol).forEach((s) => {
        const opt = document.createElement("option");
        opt.value = s;
        opt.innerText = SWAP_LABELS[s];
        toSelect.appendChild(opt);
    });
    if ([...toSelect.options].some((o) => o.value === previous)) {
        toSelect.value = previous;
    }
}

function onFromChange() {
    populateToOptions();
    refreshBalance();
}

function onToChange() {
    // Nothing else depends on the To selection until Preview is clicked.
}

async function refreshBalance() {
    const fromSymbol = document.getElementById("fromSymbol").value;
    const el = document.getElementById("fromBalance");
    el.innerText = "Loading...";
    try {
        if (fromSymbol === "BTCB") {
            const addr = await deriveTokenAddress(wallet.publicKey, "BTCB-SUBADDRESS");
            const info = await apiGet(`/btcb/balance?address=${addr}`);
            el.innerText = `${satsToBtc(info.balance)} BTC`;
        } else if (fromSymbol === "ETHB") {
            // getEthbAddress resolves this wallet's own real,
            // self-custodial Sepolia address (falling back to the old
            // derived-hash placeholder only for a brand-new wallet with
            // no HD account yet) - the same resolution api/subaddress.go's
            // resolveBalanceAddress uses server-side. Reading the balance
            // at the stale derived address (this file's previous bug) will
            // always show zero, since real deposits credit the real
            // self-custodial address instead.
            const addr = await getEthbAddress(wallet);
            const info = await apiGet(`/ethb/balance?address=${addr}`);
            el.innerText = `${weiToEth(info.balance)} ETH`;
        } else {
            const addr = await getYenAddress(wallet);
            const info = await apiGet(`/yene/balance?address=${addr}`);
            el.innerText = `${info.balance} YENE`;
        }
    } catch (e) {
        console.error(e);
        el.innerText = "Couldn't load balance";
    }
}

// nativeAmountFor converts the human-typed decimal string into the
// symbol's real native unit, always as a BigInt so nothing is ever
// silently rounded before it reaches the server - BTCB is sats
// (base-1e8), ETHB is wei (base-1e18, easily exceeds JS's 2^53 safe-
// integer range for realistic amounts), YENE is a plain whole-unit
// integer (no fractional support in this ledger).
function nativeAmountFor(symbol, decimalStr) {
    const n = parseFloat(decimalStr);
    if (isNaN(n) || n <= 0) return null;
    if (symbol === "BTCB") return btcToSats(decimalStr);
    if (symbol === "ETHB") return ethToWei(decimalStr);
    return BigInt(Math.floor(n));
}

function displayAmountFor(symbol, nativeAmount) {
    if (symbol === "BTCB") return `${satsToBtc(nativeAmount)} BTC`;
    if (symbol === "ETHB") return `${weiToEth(nativeAmount.toString())} ETH`;
    return `${nativeAmount} YENE`;
}

// usdValuePerUnitClient mirrors api/swap_fair_value.go's
// usdValuePerUnit exactly - client-side, for display purposes only,
// the server always recomputes and is the actual source of truth.
function usdValuePerUnitClient(symbol, price) {
    if (symbol === "BTCB") return price.btcUsd > 0 ? price.btcUsd / 1e8 : null;
    if (symbol === "ETHB") return price.ethUsd > 0 ? price.ethUsd / 1e18 : null;
    return 1.0;
}

async function preview() {
    const statusEl = document.getElementById("formStatus");
    const fromSymbol = document.getElementById("fromSymbol").value;
    const toSymbol = document.getElementById("toSymbol").value;
    const amountStr = document.getElementById("amount").value.trim();

    if (!toSymbol || toSymbol === fromSymbol) {
        statusEl.innerText = "Pick a different \"To\" asset.";
        return;
    }

    const fromAmount = nativeAmountFor(fromSymbol, amountStr);
    if (!fromAmount || fromAmount <= 0n) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }

    try {
        const price = await apiGet("/price/external");
        const usdValuePerUnit = usdValuePerUnitClient(fromSymbol, price);
        const toUsdValuePerUnit = usdValuePerUnitClient(toSymbol, price);
        if (!usdValuePerUnit || !toUsdValuePerUnit) {
            statusEl.innerText = "Live price unavailable right now - try again shortly.";
            return;
        }
        // Client-side estimate only - Number-based is fine here (display
        // rounding, never submitted), the server recomputes exactly from
        // the exact BigInt fromAmount at confirmation time.
        const usdValue = Number(fromAmount) * usdValuePerUnit;
        const toAmountEstimate = BigInt(Math.floor(usdValue / toUsdValuePerUnit));
        if (toAmountEstimate <= 0n) {
            statusEl.innerText = "Amount too small to produce any " + SWAP_LABELS[toSymbol] + ".";
            return;
        }

        const cctInfo = await apiGet("/price/cct").catch(() => null);
        let cctValueStr = "-";
        if (cctInfo && cctInfo.cctPerEth > 0 && price.ethUsd > 0) {
            const cctUsdPrice = price.ethUsd / cctInfo.cctPerEth;
            cctValueStr = (usdValue / cctUsdPrice).toFixed(6) + " CCT";
        }

        document.getElementById("reviewFrom").innerText = displayAmountFor(fromSymbol, fromAmount);
        document.getElementById("reviewTo").innerText = "~" + displayAmountFor(toSymbol, toAmountEstimate);
        document.getElementById("reviewUsd").innerText = "$" + usdValue.toFixed(4);
        document.getElementById("reviewCct").innerText = cctValueStr;

        document.getElementById("reviewCard").dataset.fromSymbol = fromSymbol;
        document.getElementById("reviewCard").dataset.toSymbol = toSymbol;
        document.getElementById("reviewCard").dataset.fromAmount = fromAmount.toString();

        document.getElementById("formCard").style.display = "none";
        document.getElementById("reviewCard").style.display = "block";
        document.getElementById("reviewStatus").innerText = "";
    } catch (e) {
        console.error(e);
        statusEl.innerText = "Couldn't fetch a live price: " + e.message;
    }
}

function cancelReview() {
    document.getElementById("reviewCard").style.display = "none";
    document.getElementById("formCard").style.display = "block";
    document.getElementById("formStatus").innerText = "";
}

async function confirmSwap() {
    const statusEl = document.getElementById("reviewStatus");
    const confirmBtn = document.getElementById("confirmSwapBtn");
    const reviewCard = document.getElementById("reviewCard");

    confirmBtn.disabled = true;
    statusEl.innerText = "Submitting...";

    try {
        // Built as a raw JSON literal (not JSON.stringify on an object
        // carrying a JS Number) so fromAmount's exact digit string
        // survives untouched to the Go uint64 field - required for ETHB
        // wei amounts, which routinely exceed JS's 2^53 safe-integer
        // range. See apiPostSignedRaw's doc comment in api.js.
        const payloadJSON =
            `{"fromSymbol":${JSON.stringify(reviewCard.dataset.fromSymbol)},` +
            `"toSymbol":${JSON.stringify(reviewCard.dataset.toSymbol)},` +
            `"fromAmount":${reviewCard.dataset.fromAmount}}`;
        const result = await apiPostSignedRaw("/swap/fair-value", payloadJSON);

        statusEl.innerText = `Swap pending (tx ${result.txId}) - received ${displayAmountFor(reviewCard.dataset.toSymbol, BigInt(result.toAmount))} once the next block confirms.`;

        setTimeout(() => {
            location.href = "outernet";
        }, 3000);
    } catch (e) {
        statusEl.innerText = "Swap failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

function btcAddressMatchesNetwork(address) {
    if (!address) return true;
    const expectedPrefix = window.CC_NETWORK === "mainnet" ? "bc1" : "tb1";
    return address.startsWith(expectedPrefix);
}

async function loadNetworkAddresses() {
    try {
        const accounts = getHdAccounts();
        const btcAddr = accounts && accounts.btc ? accounts.btc.address : null;
        if (!btcAddr) {
            document.getElementById("btcNetAddress").innerText = "No self-custodial BTC account found - migrate your wallet first.";
        } else if (!btcAddressMatchesNetwork(btcAddr)) {
            document.getElementById("btcNetAddress").innerHTML =
                `${btcAddr}<br><a href="btc-network-fix" style="color:var(--cc-accent-gold, #c9a227);">This was derived for the other network - fix it &rarr;</a>`;
        } else {
            document.getElementById("btcNetAddress").innerText = btcAddr;
        }
    } catch (e) {
        document.getElementById("btcNetAddress").innerText = "Unavailable";
    }

    // Read-only lookup (getEthbAddress) - resolves this wallet's own
    // self-custodial Sepolia address, the same one shown elsewhere in
    // the app. CreditChain never provisions or holds a signing key
    // here; a wallet with no HD account yet still gets a real read (the
    // derived-hash placeholder, same fallback resolveBalanceAddress uses
    // server-side) until it migrates to get a real self-custodial one.
    try {
        document.getElementById("ethNetAddress").innerText = await getEthbAddress(wallet);
    } catch (e) {
        document.getElementById("ethNetAddress").innerText = "Unavailable";
    }
    // YEN-e's network address is deliberately left blank in the markup
    // itself - it has no real external contract to show, and showing
    // one would misrepresent it as bridged when it isn't.
}

gateMainnetVaultSwap();
populateFromOptions();
populateToOptions();
onFromChange();
loadNetworkAddresses();
