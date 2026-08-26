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
    const symbolLabel = symbol === "CCT" ? "CCT" : tokenLabel(symbol);
    return `
        <div class="asset-row" style="cursor:pointer;" onclick="location.href='asset?symbol=${symbol}'">
            <div class="asset-row-left">
                <div class="asset-icon">${coinIconSVG(symbol, 38)}</div>
                <div class="asset-info">
                    <span class="asset-symbol">${symbolLabel}</span>
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

// hdEthAddress reads the self-custodial Ethereum address (see
// hdwallet.js) - null if this wallet hasn't migrated to the HD wallet
// yet. Used only on mainnet, where the real, non-custodial ETH/USDC
// rows replace ETHB/USDCTEST (see loadAssets's isMainnet branch below).
function hdEthAddress() {
    try {
        const accounts = getHdAccounts();
        return accounts && accounts.eth ? accounts.eth.address : null;
    } catch (e) {
        return null;
    }
}

async function loadAssets() {
    if (!assetsWallet) return;
    const listEl = document.getElementById("assetsList");
    const isMainnet = window.CC_NETWORK === "mainnet";

    try {
        const [cctBalance, yenAddress] = await Promise.all([
            apiGet(`/token/balance?address=${assetsWallet.address}`),
            getYenAddress(assetsWallet),
        ]);
        const yenBalance = await apiGet(`/yene/balance?address=${yenAddress}`);

        // The "BTC" row shows BTC_CCC1 (vault-locked), the SAME figure
        // asset.html?symbol=BTCB now shows as its primary balance (see
        // that file's loadHero) - showing BTCB's old mirror-model figure
        // here instead was a real inconsistency: the landing page and
        // the token detail page told two different stories about "your
        // BTC balance." Applies on both networks, matching asset.html's
        // own (network-unconditional) BTCB branch.
        const btcCcc1 = await fetchCcc1Balance(assetsWallet, "BTC");
        const btcbBtc = smallestToDecimal(btcCcc1.balance, VAULT_CHAIN_INFO.BTC.decimals);

        // On mainnet, this row shows ETH_CCC1/USDC_CCC1 (vault-locked) -
        // the SAME figures asset.html?symbol=ETH/USDC now show as their
        // primary balance (see that file's loadHero) - same "landing
        // page and token detail page must agree" fix as the BTC row
        // above. Any real ETH/USDC sitting at the self-custodial address
        // but not yet vaulted shows on the token detail page's own
        // "Received - not yet vaulted" banner, not summed in here.
        // USDCTEST is blocked entirely by MAINNET_MODE
        // (api/mainnet_gate.go). Testnet keeps today's exact
        // ETHB/USDCTEST behavior unchanged.
        let ethRowHTML, usdcRowHTML, ethFiat, usdcFiat;
        if (isMainnet) {
            const ethAddr = hdEthAddress();
            if (ethAddr) {
                registerEthWatchAddressOnce(assetsWallet, ethAddr);
            }
            const [ethCcc1, usdcCcc1] = await Promise.all([
                fetchCcc1Balance(assetsWallet, "ETH"),
                fetchCcc1Balance(assetsWallet, "USDC"),
            ]);
            const ethEth = smallestToDecimal(ethCcc1.balance, VAULT_CHAIN_INFO.ETH.decimals);
            const usdcAmount = smallestToDecimal(usdcCcc1.balance, VAULT_CHAIN_INFO.USDC.decimals);
            [ethFiat, usdcFiat] = await Promise.all([fiatLine("ETH", ethEth), fiatLine("USDC", usdcAmount)]);
            ethRowHTML = assetRowHTML("ETH", "ETH", "Real Ethereum.", ethAddr, `${ethEth} ETH`, "", ethFiat);
            usdcRowHTML = assetRowHTML("USDC", "USDC", "Real USDC, self-custodial.", ethAddr, `${usdcAmount} USDC`, "", usdcFiat);
        } else {
            const [usdcTestAddress, ethbAddress] = await Promise.all([
                getUsdcTestAddress(assetsWallet),
                getEthbAddress(assetsWallet),
            ]);
            const [usdcTestBalance, ethbBalance, usdcPrice] = await Promise.all([
                apiGet(`/usdctest/balance?address=${usdcTestAddress}`),
                apiGet(`/ethb/balance?address=${ethbAddress}`),
                priceLine("USDCTEST"),
            ]);
            const ethbEth = weiToEth(ethbBalance.balance);
            [ethFiat, usdcFiat] = await Promise.all([
                fiatLine("ETHB", ethbEth),
                fiatLine("USDCTEST", usdcTestBalance.balance),
            ]);
            ethRowHTML = assetRowHTML("ETH", "ETHB", "Your ETH balance.", null, `${ethbEth} ETH`, "", ethFiat);
            usdcRowHTML = assetRowHTML("USDC", "USDCTEST", "Imported token", usdcTestAddress, `${usdcTestBalance.balance} USDCTEST`, usdcPrice, usdcFiat);
        }

        const [cctFiat, btcbFiat, yenFiat] = await Promise.all([
            fiatLine("CCT", cctFromSmallest(cctBalance.balance)),
            fiatLine("BTCB", btcbBtc),
            fiatLine("YENE", yenBalance.balance),
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

        // One "BTC" row, showing BTC_CCC1 (see btcbBtc above) - clicking
        // through lands on asset.html?symbol=BTCB, which shows the same
        // figure as ITS primary balance plus any real-but-not-yet-
        // vaulted BTC as a secondary banner. The self-custodial BTC
        // network address itself is reachable from that page's own
        // network breakdown, not a second top-level row here.
        const btcbRowHTML = assetRowHTML(
            "BTC",
            "BTCB",
            "Your BTC balance.",
            null,
            `${btcbBtc} BTC`,
            "",
            btcbFiat
        );

        // No separate ETH_CCC1/USDC_CCC1/BTC_CCC1 rows here - each
        // chain's vault-locked balance IS the figure shown above for
        // ETH/BTC/USDC, matching asset.html's own per-chain page (see
        // that file's loadHero) so the landing page and the token detail
        // page always agree.
        listEl.innerHTML =
            assetRowHTML("CCT", "CCT", "CreditChain native token", null, `${cctFromSmallest(cctBalance.balance)} CCT`, "Hub currency", cctFiat) +
            ethRowHTML +
            btcbRowHTML +
            usdcRowHTML +
            assetRowHTML("YEN", "YENE", isMainnet ? "Borrowed funds (Engage)" : "Testnet YEN-e", null, `${yenBalance.balance} YENE`, "", yenFiat) +
            customRowsHTML;
    } catch (e) {
        console.error(e);
        listEl.innerHTML = `<p class="explorer-empty">Could not load assets.</p>`;
    }
}

loadAssets();
setInterval(loadAssets, 15000);
