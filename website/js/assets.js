// assets.html - the full-page version of outernet.html's Assets card,
// split out per the user's request to separate wallet/assets/profile/
// network into their own pages, same pattern as Explorer/Marketplace
// already being separate pages rather than sections of one giant
// dashboard. Reuses the exact same balance/derived-address/price data
// sources outernet.js already uses - no new backend surface needed.

const assetsWallet = requireWallet();

function tokenLabel(symbol) {
    return symbol === "ETHB" ? "ETH" : symbol === "BTCB" ? "BTC" : symbol === "USDCTEST" ? "USDC" : symbol;
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

// satsToBtc mirrors send-bridged-btc.js's helper - /btcb/balance
// returns a plain sats integer (safe within JS's number range, unlike
// ETHB's wei), so this is a much simpler base-1e8 conversion.
function satsToBtc(sats) {
    const n = Number(sats || 0);
    const whole = Math.floor(n / 1e8);
    const frac = String(n % 1e8).padStart(8, "0").replace(/0+$/, "") || "0";
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

function assetRowHTML(iconLabel, symbol, name, address, balanceText, priceText, fiatText) {
    const copyId = `assetCopy_${symbol}`;
    const addressHTML = address
        ? `<span class="asset-name asset-address">${address}</span>${copyIconHTML(address, copyId)}`
        : `<span class="asset-name">${name}</span>`;
    const fiatHTML = fiatText ? `<span class="asset-fiat" style="opacity:0.65;font-size:12px;">${fiatText}</span>` : "";
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
                ${fiatHTML}
                <span class="asset-price">${priceText}</span>
            </div>
        </div>
    `;
}

async function loadAssets() {
    if (!assetsWallet) return;
    const listEl = document.getElementById("assetsList");

    try {
        const [cctBalance, usdcTestAddress, ethbAddress, btcbAddress] = await Promise.all([
            apiGet(`/token/balance?address=${assetsWallet.address}`),
            getUsdcTestAddress(assetsWallet),
            getEthbAddress(assetsWallet),
            deriveTokenAddress(assetsWallet.publicKey, "BTCB-SUBADDRESS"),
        ]);

        const [usdcTestBalance, ethbBalance, btcbBalance, usdcPrice] = await Promise.all([
            apiGet(`/usdctest/balance?address=${usdcTestAddress}`),
            apiGet(`/ethb/balance?address=${ethbAddress}`),
            apiGet(`/btcb/balance?address=${btcbAddress}`),
            priceLine("USDCTEST"),
        ]);

        const ethbEth = weiToEth(ethbBalance.balance);
        const btcbBtc = satsToBtc(btcbBalance.balance);

        const [cctFiat, ethbFiat, btcbFiat, usdcFiat] = await Promise.all([
            fiatLine("CCT", cctBalance.balance),
            fiatLine("ETHB", ethbEth),
            fiatLine("BTCB", btcbBtc),
            fiatLine("USDCTEST", usdcTestBalance.balance),
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
            const fiats = await Promise.all(
                (customTokens || []).map((t, i) => fiatLine(t.symbol, balances[i].balance))
            );
            customRowsHTML = (customTokens || [])
                .map((t, i) =>
                    assetRowHTML(
                        t.symbol.slice(0, 4).toUpperCase(),
                        t.symbol,
                        t.name || "Developer-registered token",
                        null,
                        `${balances[i].balance} ${t.symbol}`,
                        prices[i],
                        fiats[i]
                    )
                )
                .join("");
        } catch (e) {
            console.error(e);
        }

        // Two BTC-related rows, mirroring the ETH row split: "BTC" is
        // the self-custodial testnet address itself (its own network,
        // real UTXOs, spent directly via BIP143 signing - see
        // send-bridged-btc.html's "BTC network" mode); "BTCB" is the
        // bridged ledger balance credited when deposits to that same
        // address are detected (see api/btc_bridge.go) - unlike ETHB
        // there's no separate custodial address for BTCB, deposits
        // land on the user's own self-custodial address.
        let btcRowHTML = "";
        const btcAccount = hdAccount("btc");
        if (btcAccount) {
            btcRowHTML = assetRowHTML(
                "BTC",
                "BTC",
                "Bitcoin (testnet), self-custodial address",
                btcAccount.address,
                "View on Bitcoin network",
                ""
            );
        }
        const btcbRowHTML = assetRowHTML(
            "BTC",
            "BTCB",
            "Bridged BTC (deposits to your Bitcoin address auto-credit here; send picks CCT or BTC network)",
            null,
            `${btcbBtc} BTC`,
            "",
            btcbFiat
        );

        // One "ETH" row - real bridged Sepolia ETH (the auto-bridge
        // balance, symbol ETHB internally). Deposits to your custodial
        // Sepolia address auto-credit here; send picks CCT network
        // (instant, internal) or ERC network (real Sepolia, triggers a
        // bridge withdrawal) - see send-bridged-eth.html. The old
        // synthetic ETHTEST faucet token is deliberately NOT shown or
        // added into this balance - it's a separate, no-real-value
        // internal token still used for staking/collateral on the
        // Borrow page, and mixing its number into real (if testnet)
        // ETH would misrepresent what's actually real.
        const ethRowHTML = assetRowHTML(
            "ETH",
            "ETHB",
            "Real bridged Sepolia ETH (deposits auto-credit; send picks CCT or ERC network)",
            null,
            `${ethbEth} ETH`,
            "",
            ethbFiat
        );

        listEl.innerHTML =
            assetRowHTML("CCT", "CCT", "CreditChain native token", null, `${cctBalance.balance} CCT`, "Hub currency", cctFiat) +
            ethRowHTML +
            btcbRowHTML +
            assetRowHTML("USDC", "USDCTEST", "Imported token", usdcTestAddress, `${usdcTestBalance.balance} USDCTEST`, usdcPrice, usdcFiat) +
            btcRowHTML +
            customRowsHTML;
    } catch (e) {
        console.error(e);
        listEl.innerHTML = `<p class="explorer-empty">Could not load assets.</p>`;
    }
}

loadAssets();
setInterval(loadAssets, 15000);
