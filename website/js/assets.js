// assets.html - the full-page version of outernet.html's Assets card,
// split out per the user's request to separate wallet/assets/profile/
// network into their own pages, same pattern as Explorer/Marketplace
// already being separate pages rather than sections of one giant
// dashboard. Reuses the exact same balance/derived-address/price data
// sources outernet.js already uses - no new backend surface needed.

const assetsWallet = requireWallet();

function tokenLabel(symbol) {
    return symbol === "ETHTEST" ? "ETH" : symbol === "USDCTEST" ? "USDC" : symbol;
}

// weiToEth mirrors asset.js/defi.js's helper of the same name - ETHB
// balances come back from /ethb/balance in wei, same as the custodial
// ETH corner, since ETHB tracks real bridged Sepolia ETH 1:1.
function weiToEth(weiString) {
    const wei = BigInt(weiString || "0");
    const whole = wei / 1000000000000000000n;
    const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// hdAccount reads the self-custodial Ethereum/Bitcoin accounts derived
// at registration/migration time (see hdwallet.js) - null if this
// wallet predates the Multi-Chain HD Wallet and hasn't migrated yet.
function hdAccount(chain) {
    try {
        const accounts = JSON.parse(localStorage.getItem("cc_hd_accounts") || "null");
        return accounts ? accounts[chain] : null;
    } catch (e) {
        return null;
    }
}

async function priceLine(symbol) {
    try {
        const history = await apiGet(`/pool/price-history?symbol=${encodeURIComponent(symbol)}`);
        if (!history || history.length < 2) return "No trades yet";
        const last = history[history.length - 1].price / 1e6;
        return `1 ${tokenLabel(symbol)} = ${last.toFixed(6)} CCT`;
    } catch (e) {
        return "";
    }
}

function assetRowHTML(iconLabel, symbol, name, address, balanceText, priceText) {
    const copyId = `assetCopy_${symbol}`;
    const addressHTML = address
        ? `<span class="asset-name asset-address">${address}</span>${copyIconHTML(address, copyId)}`
        : `<span class="asset-name">${name}</span>`;
    return `
        <div class="asset-row" style="cursor:pointer;" onclick="location.href='asset.html?symbol=${symbol}'">
            <div class="asset-row-left">
                <div class="asset-icon">${iconLabel}</div>
                <div class="asset-info">
                    <span class="asset-symbol">${symbol === "CCT" ? "CCT" : tokenLabel(symbol)}</span>
                    ${addressHTML}
                </div>
            </div>
            <div class="asset-values">
                <span class="asset-balance">${balanceText}</span>
                <span class="asset-price">${priceText}</span>
            </div>
        </div>
    `;
}

async function loadAssets() {
    if (!assetsWallet) return;
    const listEl = document.getElementById("assetsList");

    try {
        const [cctBalance, ethTestAddress, usdcTestAddress, ethbAddress] = await Promise.all([
            apiGet(`/token/balance?address=${assetsWallet.address}`),
            getEthTestAddress(assetsWallet),
            getUsdcTestAddress(assetsWallet),
            getEthbAddress(assetsWallet),
        ]);

        const [ethTestBalance, usdcTestBalance, ethbBalance, ethPrice, usdcPrice] = await Promise.all([
            apiGet(`/ethtest/balance?address=${ethTestAddress}`),
            apiGet(`/usdctest/balance?address=${usdcTestAddress}`),
            apiGet(`/ethb/balance?address=${ethbAddress}`),
            priceLine("ETHTEST"),
            priceLine("USDCTEST"),
        ]);

        let customRowsHTML = "";
        try {
            const customTokens = await apiGet("/tokens");
            const [balances, prices] = await Promise.all([
                Promise.all(
                    (customTokens || []).map((t) =>
                        apiGet(`/tokens/balance?symbol=${encodeURIComponent(t.symbol)}&address=${assetsWallet.address}`)
                    )
                ),
                Promise.all((customTokens || []).map((t) => priceLine(t.symbol))),
            ]);
            customRowsHTML = (customTokens || [])
                .map((t, i) =>
                    assetRowHTML(
                        t.symbol.slice(0, 4).toUpperCase(),
                        t.symbol,
                        t.name || "Developer-registered token",
                        null,
                        `${balances[i].balance} ${t.symbol}`,
                        prices[i]
                    )
                )
                .join("");
        } catch (e) {
            console.error(e);
        }

        // The ETH row groups CreditChain's own ETHTEST representation
        // with the real, self-custodial Ethereum account derived from
        // the same recovery phrase (see the Multi-Chain HD Wallet
        // plan's §A.6) - one row, not two, even though two independent
        // balances sit behind it. Its detail page (asset.html?symbol=
        // ETHTEST) shows both networks' addresses/Receive actions; this
        // list row itself only ever shows the CreditChain-side balance,
        // since Phase A has no live Ethereum balance lookup yet.
        const ethAccount = hdAccount("eth");
        const ethSubtitle = ethAccount ? "CreditChain + Ethereum (self-custodial)" : "Imported token";
        let btcRowHTML = "";
        const btcAccount = hdAccount("btc");
        if (btcAccount) {
            btcRowHTML = assetRowHTML(
                "BTC",
                "BTC",
                "Bitcoin (testnet), self-custodial - receive-only",
                btcAccount.address,
                "Phase A: receive-only",
                ""
            );
        }

        // ETHB is a distinct, fourth ETH-ish row - the auto-bridge
        // feature's unified balance, anchored on the KMS-custodial
        // Sepolia corner (defi.html), not the self-custodial HD
        // Ethereum account the ETH row above already covers, and not
        // ETHTEST's synthetic-faucet meaning either. See the auto-bridge
        // plan's §5.
        const ethbRowHTML = assetRowHTML(
            "ETHB",
            "ETHB",
            "Bridged ETH (auto-credited from your custodial Sepolia deposit address)",
            null,
            `${weiToEth(ethbBalance.balance)} ETHB`,
            ""
        );

        listEl.innerHTML =
            assetRowHTML("CCT", "CCT", "CreditChain native token", null, `${cctBalance.balance} CCT`, "Hub currency") +
            assetRowHTML("ETH", "ETHTEST", ethSubtitle, ethTestAddress, `${ethTestBalance.balance} ETHTEST`, ethPrice) +
            assetRowHTML("USDC", "USDCTEST", "Imported token", usdcTestAddress, `${usdcTestBalance.balance} USDCTEST`, usdcPrice) +
            ethbRowHTML +
            btcRowHTML +
            customRowsHTML;
    } catch (e) {
        console.error(e);
        listEl.innerHTML = `<p class="explorer-empty">Could not load assets.</p>`;
    }
}

loadAssets();
setInterval(loadAssets, 15000);
