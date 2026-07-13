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
        const [cctBalance, ethTestAddress, usdcTestAddress] = await Promise.all([
            apiGet(`/token/balance?address=${assetsWallet.address}`),
            getEthTestAddress(assetsWallet),
            getUsdcTestAddress(assetsWallet),
        ]);

        const [ethTestBalance, usdcTestBalance, ethPrice, usdcPrice] = await Promise.all([
            apiGet(`/ethtest/balance?address=${ethTestAddress}`),
            apiGet(`/usdctest/balance?address=${usdcTestAddress}`),
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

        listEl.innerHTML =
            assetRowHTML("CCT", "CCT", "CreditChain native token", null, `${cctBalance.balance} CCT`, "Hub currency") +
            assetRowHTML("ETH", "ETHTEST", "Imported token", ethTestAddress, `${ethTestBalance.balance} ETHTEST`, ethPrice) +
            assetRowHTML("USDC", "USDCTEST", "Imported token", usdcTestAddress, `${usdcTestBalance.balance} USDCTEST`, usdcPrice) +
            customRowsHTML;
    } catch (e) {
        console.error(e);
        listEl.innerHTML = `<p class="explorer-empty">Could not load assets.</p>`;
    }
}

loadAssets();
setInterval(loadAssets, 15000);
