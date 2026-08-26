const wallet = requireWallet();

const symbol = new URLSearchParams(window.location.search).get("symbol") || "CCT";

const ASSET_DESCRIPTIONS = {
    CCT: "CreditChain's native network token - the universal hub every swap prices through.",
    REPT: "Reputation-progression token, minted as a one-time reward the first time your reputation reaches a new credit tier.",
    ETHTEST: "Imported test ETH token - lives at its own address derived from this wallet's key, separate from your CCT address. This is CreditChain's own internal representation, not real Ethereum.",
    USDCTEST: "Imported test USDC token - lives at its own address derived from this wallet's key, separate from your CCT address.",
    ETH: "Real Ethereum, self-custodial - derived from the same recovery phrase as your CreditChain account, but an entirely separate blockchain with its own address and key.",
    BTC: "Real Bitcoin, self-custodial - derived from the same recovery phrase as your CreditChain account, but an entirely separate blockchain with its own address and key.",
    USDC: "Real USDC (an ERC-20 token), self-custodial - lives at the same address as your Ethereum account above, since ERC-20 tokens live on Ethereum itself. CreditChain never holds this key.",
    ETHB: "Your ETH balance.",
    BTCB: "Your BTC balance.",
};

// EXTERNAL_CHAIN_SYMBOLS are not CreditChain tokens at all - they live
// on their own independent blockchain (see website/js/hdwallet.js).
// USDC maps to the SAME "eth" HD account as ETH, since an ERC-20 token
// balance lives at whatever address holds it - there is no separate
// "USDC account" to derive. CreditChain's own explorer must never
// claim to show history for any of these (it's CreditChain-internal
// only, see api/explorer.go).
const EXTERNAL_CHAIN_SYMBOLS = { ETH: "eth", BTC: "btc", USDC: "eth" };

function descriptionFor(sym) {
    return ASSET_DESCRIPTIONS[sym] || "Custom token registered on OUTERNET.";
}

// setFiat computes and shows a fiat-equivalent line under the hero
// balance via currency.js's shared fiatLine() helper - shows nothing
// if no conversion path exists yet (e.g. an untraded custom token).
async function setFiat(sym, balance) {
    const el = document.getElementById("assetFiat");
    if (!el) return;
    el.innerText = await fiatLine(sym, balance);
}

// weiToEth mirrors defi.js's helper of the same name - both convert a
// decimal-wei string into a human ETH string using BigInt (no
// float-precision loss), kept as separate small copies rather than a
// shared import since each page already loads independently and this
// is only 5 lines.
function weiToEth(weiString) {
    const wei = BigInt(weiString);
    const whole = wei / 1000000000000000000n;
    const frac = (wei % 1000000000000000000n).toString().padStart(18, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// satsToBtcAsset mirrors send-bridged-btc.js's satsToBtc helper -
// BTCB/real-BTC balances are plain sats integers (safe within JS's
// number range), a much simpler base-1e8 conversion than weiToEth.
function satsToBtcAsset(sats) {
    const n = Number(sats || 0);
    const whole = Math.floor(n / 1e8);
    const frac = String(n % 1e8).padStart(8, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// smallestToDecimal converts an arbitrary smallest-unit decimal string
// into a human decimal string for a token with `decimals` places (e.g.
// USDC = 6) - same BigInt-based, no-float-precision-loss approach as
// weiToEth/satsToBtcAsset, generalized to any decimal count.
function smallestToDecimal(smallestStr, decimals) {
    const n = BigInt(smallestStr || "0");
    const scale = 10n ** BigInt(decimals);
    const whole = n / scale;
    const frac = (n % scale).toString().padStart(decimals, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// btcAddressMatchesNetwork checks the cached BTC address's bech32
// prefix against the currently active network - mainnet ("bc1...")
// and testnet ("tb1...") BTC accounts are derived from genuinely
// different BIP-84 paths (coin type 0' vs 1'), not just different
// address encodings of the same key, so a stale cached address from
// before a network switch needs a real re-derivation, not just a
// relabel - see btc-network-fix.html.
function btcAddressMatchesNetwork(address) {
    if (!address) return true; // nothing cached yet - not a mismatch, just missing
    const expectedPrefix = window.CC_NETWORK === "mainnet" ? "bc1" : "tb1";
    return address.startsWith(expectedPrefix);
}

function externalChainAddress(sym) {
    const key = EXTERNAL_CHAIN_SYMBOLS[sym];
    if (!key) return null;
    try {
        const accounts = getHdAccounts();
        return accounts && accounts[key] ? accounts[key].address : null;
    } catch (e) {
        return null;
    }
}

// assetAddress resolves which address actually holds this symbol's
// balance - CCT/REPT/custom tokens stay on the wallet's own address,
// ETHTEST/USDCTEST resolve to their derived sub-address (mirrors
// api/subaddress.go's resolveBalanceAddress), and ETH/BTC resolve to
// their own independent-blockchain address (see hdwallet.js) - none of
// these three schemes are interchangeable.
async function assetAddress() {
    if (EXTERNAL_CHAIN_SYMBOLS[symbol]) {
        return externalChainAddress(symbol) || "";
    }
    return balanceAddressForSymbol(wallet, symbol);
}

// loadVaultSection shows this chain's vault-locked (CCC1) balance
// directly on its own asset page instead of as a separate top-level
// Assets-list row - one "BTC" page covering both the real BTC balance
// and any BTC_CCC1 locked in the vault, same for ETH/USDC, per the
// user's "btc and btc ccc1 are in one place" request. vaultChain is a
// VAULT_CHAIN_INFO key (ETH/USDC/BTC, see vault.js), not this page's
// own `symbol` - BTCB's page shows the BTC vault chain, ETHB's page
// shows the ETH vault chain, so callers pass it explicitly rather than
// this function trying to infer it from `symbol` itself.
async function loadVaultSection(vaultChain) {
    try {
        const info = VAULT_CHAIN_INFO[vaultChain];
        const { balance } = await fetchCcc1Balance(wallet, vaultChain);
        const display = smallestToDecimal(balance, info.decimals);
        document.getElementById("ovVaultBalance").innerText = `${display} ${info.ccc1Symbol}`;
        document.getElementById("ovVaultRow").style.display = "flex";
        document.getElementById("ovVaultSendLink").href = `vault-withdraw?chain=${vaultChain}`;
        document.getElementById("ovVaultSendLink").innerText = `Send ${info.ccc1Symbol} →`;
        document.getElementById("ovVaultSendWrap").style.display = "block";
    } catch (e) {
        console.error(e);
    }
}

function showAssetTab(tab) {
    document.getElementById("assetTabOverviewBtn").classList.toggle("active", tab === "overview");
    document.getElementById("assetTabHistoryBtn").classList.toggle("active", tab === "history");
    document.getElementById("assetTabOverview").classList.toggle("active", tab === "overview");
    document.getElementById("assetTabHistory").classList.toggle("active", tab === "history");
}

// displaySymbol is what's shown to the user - ETHB is real bridged
// Sepolia ETH, so it's just labeled "ETH" everywhere on this page
// (the internal symbol/API calls still use "ETHB", only the on-screen
// text changes).
const displaySymbol = symbol === "ETHB" ? "ETH" : symbol === "BTCB" ? "BTC" : symbol;

async function loadHero() {
    document.getElementById("assetHeroIcon").innerHTML = coinIconSVG(symbol === "ETH" ? "ETHTEST" : symbol === "BTC" ? "BTCB" : symbol, 56);
    document.getElementById("assetSymbol").innerText = displaySymbol;
    document.getElementById("ovSymbol").innerText = displaySymbol;
    document.getElementById("ovDescription").innerText = descriptionFor(symbol);

    if (symbol === "ETH") {
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = address || "No wallet found for this network.";

        // The "ETH" figure shown here is the vault-locked ETH_CCC1
        // balance, not the raw real on-chain balance - this address's
        // job is to receive real ETH and forward it into the vault
        // (see the pending-lock banner below); ETH_CCC1 is what's
        // actually spendable/sendable within CreditChain. This mirrors
        // BTC_CCC1/USDC_CCC1's own "your real balance lives behind the
        // vault" model, just surfaced as the primary figure here instead
        // of a secondary row, per the user's "one balance" request.
        if (address) {
            registerEthWatchAddressOnce(wallet, address);
            try {
                const { balance: ccc1Balance } = await fetchCcc1Balance(wallet, "ETH");
                const ccc1Amount = smallestToDecimal(ccc1Balance, VAULT_CHAIN_INFO.ETH.decimals);
                const ethStr = `${ccc1Amount} ETH`;
                document.getElementById("assetBalance").innerText = ethStr;
                document.getElementById("ovBalance").innerText = ethStr;
                setFiat("ETH", ccc1Amount);
            } catch (e) {
                console.error(e);
                document.getElementById("assetBalance").innerText = "Couldn't load balance";
                document.getElementById("ovBalance").innerText = "-";
            }

            // GAS_LIMIT_DEPOSIT_ESTIMATE mirrors vault-deposit-eth.js's
            // own GAS_LIMIT_DEPOSIT - real ETH sitting at this address
            // above what a lock transaction's own gas would cost is
            // "pending" (received, not yet locked into ETH_CCC1 above).
            try {
                const info = await apiGet(`/eth-selfcustody/balance?address=${address}`);
                const realWei = BigInt(info.weiBalance || "0");
                const gasReserveWei = 150000n * BigInt(info.suggestedGasPriceWei || "0");
                const pendingWei = realWei > gasReserveWei ? realWei - gasReserveWei : 0n;
                if (pendingWei > 0n) {
                    const pendingDisplay = weiToEth(pendingWei.toString());
                    document.getElementById("ovVaultLabel").innerText = "Received - not yet vaulted";
                    document.getElementById("ovVaultBalance").innerText = `${pendingDisplay} ETH`;
                    document.getElementById("ovVaultRow").style.display = "flex";
                    document.getElementById("ovVaultSendLink").href = `vault-deposit-eth?chain=ETH&amount=${pendingDisplay}`;
                    document.getElementById("ovVaultSendLink").innerText = "Vault to CreditChain network →";
                    document.getElementById("ovVaultSendWrap").style.display = "block";
                }
            } catch (e) {
                console.error(e);
            }
        }

        document.getElementById("ovOtherNetworksList").innerHTML =
            `This account holds real ETH directly - the key never leaves this browser. ` +
            `Deposits here are what get locked into your spendable ETH_CCC1 balance above; ` +
            `<a href="send-bridged-eth" style="color:var(--cc-accent-gold, #c9a227);">CCT network / direct-from-address send options</a> are also available.`;
        document.getElementById("ovOtherNetworks").style.display = "block";
        return;
    }

    if (symbol === "USDC") {
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = address || "No wallet found for this network.";

        // Same "the CCC1 balance IS the figure shown" model as ETH/BTC
        // above - this address's job is to receive real USDC and forward
        // it into the vault; USDC_CCC1 is what's actually spendable.
        if (address) {
            try {
                const { balance: ccc1Balance } = await fetchCcc1Balance(wallet, "USDC");
                const ccc1Amount = smallestToDecimal(ccc1Balance, VAULT_CHAIN_INFO.USDC.decimals);
                const usdcStr = `${ccc1Amount} USDC`;
                document.getElementById("assetBalance").innerText = usdcStr;
                document.getElementById("ovBalance").innerText = usdcStr;
                setFiat("USDC", ccc1Amount);
            } catch (e) {
                console.error(e);
                document.getElementById("assetBalance").innerText = "Couldn't load balance";
                document.getElementById("ovBalance").innerText = "-";
            }

            // No gas-reserve subtraction here (unlike ETH) - USDC's own
            // transfer/vault-deposit gas is paid from the real ETH
            // balance at this same address, not from the USDC amount
            // itself, so the full real USDC balance is "pending."
            try {
                const info = await apiGet(`/erc20/usdc-balance?address=${address}`);
                const decimals = Number(info.decimals || 6);
                const pendingAmount = smallestToDecimal(info.balance, decimals);
                if (parseFloat(pendingAmount) > 0) {
                    document.getElementById("ovVaultLabel").innerText = "Received - not yet vaulted";
                    document.getElementById("ovVaultBalance").innerText = `${pendingAmount} USDC`;
                    document.getElementById("ovVaultRow").style.display = "flex";
                    document.getElementById("ovVaultSendLink").href = `vault-deposit-eth?chain=USDC&amount=${pendingAmount}`;
                    document.getElementById("ovVaultSendLink").innerText = "Vault to CreditChain network →";
                    document.getElementById("ovVaultSendWrap").style.display = "block";
                }
            } catch (e) {
                console.error(e);
            }
        }

        document.getElementById("ovOtherNetworksList").innerHTML =
            `Lives at the same address as your ETH account above - the key never leaves this browser. Deposits ` +
            `here are what get vaulted into your spendable USDC balance above.`;
        document.getElementById("ovOtherNetworks").style.display = "block";
        return;
    }

    if (symbol === "ETHB") {
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = formatAddress(address);

        try {
            const info = await apiGet(`/ethb/balance?address=${address}`);
            const ethAmount = weiToEth(info.balance);
            const ethStr = `${ethAmount} ETH`;
            document.getElementById("assetBalance").innerText = ethStr;
            document.getElementById("ovBalance").innerText = ethStr;
            setFiat("ETHB", ethAmount);
        } catch (e) {
            console.error(e);
            document.getElementById("assetBalance").innerText = "Couldn't load balance";
            document.getElementById("ovBalance").innerText = "-";
        }

        document.getElementById("ovOtherNetworksList").innerHTML =
            `<a href="send-bridged-eth" style="color:var(--cc-accent-gold, #c9a227);">Send</a>`;
        document.getElementById("ovOtherNetworks").style.display = "block";
        loadVaultSection("ETH");
        return;
    }

    if (symbol === "BTCB") {
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = formatAddress(address);

        // The "BTC" figure shown here is the vault-locked BTC_CCC1
        // balance, not BTCB's old auto-credited mirror-model figure -
        // same "this address's job is to receive and forward into the
        // vault" model as the ETH page above. The real self-custodial
        // BTC address (externalChainAddress("BTC"), NOT this BTCB
        // subaddress) is what actually receives real BTC and is what
        // the pending-lock banner below checks.
        try {
            const { balance: ccc1Balance } = await fetchCcc1Balance(wallet, "BTC");
            const ccc1Display = smallestToDecimal(ccc1Balance, VAULT_CHAIN_INFO.BTC.decimals);
            const btcStr = `${ccc1Display} BTC`;
            document.getElementById("assetBalance").innerText = btcStr;
            document.getElementById("ovBalance").innerText = btcStr;
            setFiat("BTC", ccc1Display);
        } catch (e) {
            console.error(e);
            document.getElementById("assetBalance").innerText = "Couldn't load balance";
            document.getElementById("ovBalance").innerText = "-";
        }

        const realBtcAddress = externalChainAddress("BTC");
        if (realBtcAddress && btcAddressMatchesNetwork(realBtcAddress)) {
            try {
                const [utxos, feeInfo] = await Promise.all([
                    apiGet(`/btc-bridge/utxos?address=${realBtcAddress}`),
                    apiGet("/btc-bridge/fee-estimate").catch(() => ({ satsPerVByte: 2 })),
                ]);
                const confirmedSats = (utxos || [])
                    .filter((u) => u.status && u.status.confirmed)
                    .reduce((sum, u) => sum + Number(u.value || 0), 0);
                // ~220 vBytes covers a typical 1-input lock tx (vault
                // P2WSH + OP_RETURN memo + change output) - same vsize
                // math as vault-deposit-btc.js's estimateVaultDepositFee,
                // just a fixed single-input estimate here since this is
                // only for deciding whether "pending" is worth a banner,
                // not the real fee charged (that's recomputed exactly at
                // Review time on the deposit page itself).
                const feeReserveSats = Math.ceil((feeInfo.satsPerVByte || 2) * 220);
                const pendingSats = confirmedSats > feeReserveSats ? confirmedSats - feeReserveSats : 0;
                if (pendingSats > 0) {
                    const pendingDisplay = satsToBtcAsset(pendingSats);
                    document.getElementById("ovVaultLabel").innerText = "Received - not yet vaulted";
                    document.getElementById("ovVaultBalance").innerText = `${pendingDisplay} BTC`;
                    document.getElementById("ovVaultRow").style.display = "flex";
                    document.getElementById("ovVaultSendLink").href = `vault-deposit-btc?amount=${pendingDisplay}`;
                    document.getElementById("ovVaultSendLink").innerText = "Vault to CreditChain network →";
                    document.getElementById("ovVaultSendWrap").style.display = "block";
                }
            } catch (e) {
                console.error(e);
            }
        }

        document.getElementById("ovOtherNetworksList").innerHTML =
            `Deposits to your real BTC address get locked into your spendable BTC_CCC1 balance above; ` +
            `<a href="send-bridged-btc" style="color:var(--cc-accent-gold, #c9a227);">CCT network / direct-from-address send options</a> are also available.`;
        document.getElementById("ovOtherNetworks").style.display = "block";
        return;
    }

    if (EXTERNAL_CHAIN_SYMBOLS[symbol]) {
        if (symbol === "BTC") {
            // Real self-custodial testnet BTC balance - now spendable
            // (see send-bridged-btc.html's "BTC network" mode), so this
            // is no longer a receive-only placeholder.
            document.getElementById("assetSendBtn").style.display = "inline-block";
            const address = await assetAddress();
            const networkLabel = window.CC_NETWORK === "mainnet" ? "Mainnet" : "Testnet";
            document.getElementById("ovAddress").innerText = address
                ? `[${networkLabel}] ${address}`
                : "No wallet found for this network.";

            if (address && !btcAddressMatchesNetwork(address)) {
                document.getElementById("assetBalance").innerText = "Wrong network for this BTC address";
                document.getElementById("ovBalance").innerText = "-";
                document.getElementById("assetSendBtn").style.display = "none";
                document.getElementById("ovOtherNetworksList").innerHTML =
                    `<a href="btc-network-fix" style="color:var(--cc-accent-gold, #c9a227);">This address was derived for the other network - re-enter your recovery phrase to fix it &rarr;</a>`;
                document.getElementById("ovOtherNetworks").style.display = "block";
                return;
            }

            try {
                const utxos = await apiGet(`/btc-bridge/utxos?address=${address}`);
                const confirmedSats = (utxos || [])
                    .filter((u) => u.status && u.status.confirmed)
                    .reduce((sum, u) => sum + Number(u.value || 0), 0);
                const btcAmount = satsToBtcAsset(confirmedSats);
                const btcStr = `${btcAmount} BTC (${networkLabel})`;
                document.getElementById("assetBalance").innerText = btcStr;
                document.getElementById("ovBalance").innerText = btcStr;
                setFiat("BTC", btcAmount);
            } catch (e) {
                console.error(e);
                document.getElementById("assetBalance").innerText = "Couldn't load balance";
                document.getElementById("ovBalance").innerText = "-";
            }

            document.getElementById("ovOtherNetworksList").innerHTML =
                `<a href="asset?symbol=BTCB" style="color:var(--cc-accent-gold, #c9a227);">Spend inside CreditChain &rarr;</a>`;
            document.getElementById("ovOtherNetworks").style.display = "block";
            return;
        }

        document.getElementById("assetBalance").innerText = `Receive-only, balance display not yet available`;
        document.getElementById("ovBalance").innerText = "-";
        document.getElementById("assetSendBtn").style.display = "none";
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = address || "No wallet found for this network.";
        document.getElementById("ovOtherNetworks").style.display = "none";
        return;
    }

    try {
        const balance = await fetchTokenBalance(wallet, symbol);
        // CCT balances come back as an 18-decimal smallest-unit string
        // now (see website/js/api.js's cctFromSmallest) - every other
        // symbol reaching this generic path (REPT/ETHTEST/USDCTEST/
        // custom tokens) is still a plain whole-unit number, unchanged.
        const displayBalance = symbol === "CCT" ? cctFromSmallest(balance.balance) : balance.balance;
        document.getElementById("assetBalance").innerText = `${displayBalance} ${symbol}`;
        document.getElementById("ovBalance").innerText = `${displayBalance} ${symbol}`;
        setFiat(symbol, displayBalance);
    } catch (e) {
        console.error(e);
    }

    try {
        const address = await assetAddress();
        document.getElementById("ovAddress").innerText = formatAddress(address);
    } catch (e) {
        console.error(e);
    }

    // ETHTEST is CreditChain's own representation of "ETH" - the
    // group's second network (the real, self-custodial Ethereum
    // account) lives at a separate page, cross-linked here so opening
    // this one row surfaces both networks per the Multi-Chain HD
    // Wallet plan's §A.6 unified-asset UX.
    if (symbol === "ETHTEST" && externalChainAddress("ETH")) {
        document.getElementById("ovOtherNetworksList").innerHTML =
            `<a href="asset?symbol=ETH" style="color:var(--cc-accent-gold, #c9a227);">Ethereum (self-custodial, receive-only) &rarr;</a>`;
        document.getElementById("ovOtherNetworks").style.display = "block";
    }
}

// loadChart is a compact copy of price-chart.js's loadPriceChart -
// CCT itself never gets a chart (it's the hub currency every OTHER
// token prices against, not traded against itself).
async function loadChart() {
    const container = document.getElementById("assetChartArea");

    if (EXTERNAL_CHAIN_SYMBOLS[symbol]) {
        container.innerHTML = `<p style="opacity:0.6;font-size:13px;">${symbol} is a real external-chain asset, not a CreditChain-traded token - no price chart here.</p>`;
        return;
    }

    if (symbol === "CCT") {
        container.innerHTML = `<p style="opacity:0.6;font-size:13px;">Native network token - no price chart (every other token prices in CCT).</p>`;
        return;
    }

    try {
        const pools = await apiGet("/pools");
        const isTradeable = (pools || []).some((p) => p.symbol === symbol && p.tradeable);
        if (!isTradeable) {
            container.innerHTML = `<p style="opacity:0.6;font-size:13px;">No live pool yet for ${symbol}.</p>`;
            return;
        }

        const history = await apiGet(`/pool/price-history?symbol=${encodeURIComponent(symbol)}`);
        if (!history || history.length < 2) {
            container.innerHTML = `<p style="opacity:0.6;font-size:13px;">Not enough trading activity yet to chart ${symbol}.</p>`;
            return;
        }

        const prices = history.map((p) => p.price / 1e6);
        const min = Math.min(...prices);
        const max = Math.max(...prices);
        const range = max - min || max || 1;

        const width = 500;
        const height = 140;
        const pad = 8;

        const points = history
            .map((p, i) => {
                const x = pad + (i / (history.length - 1)) * (width - pad * 2);
                const y = height - pad - ((p.price / 1e6 - min) / range) * (height - pad * 2);
                return `${x.toFixed(1)},${y.toFixed(1)}`;
            })
            .join(" ");

        const first = prices[0];
        const last = prices[prices.length - 1];
        const changeColor = last >= first ? "#0f9d58" : "#ff6b6b";

        container.innerHTML = `
            <svg viewBox="0 0 ${width} ${height}" style="width:100%;height:100%;display:block;">
                <polyline points="${points}" fill="none" stroke="${changeColor}" stroke-width="2.5" />
            </svg>
        `;
        document.getElementById("assetBalance").innerText += ` · 1 ${symbol} = ${last.toFixed(6)} CCT`;
    } catch (e) {
        console.error(e);
        container.innerHTML = `<p style="opacity:0.6;font-size:13px;">Couldn't load price history.</p>`;
    }
}

// networkLabelForTx distinguishes a real external-chain bridge leg from
// an ordinary CreditChain-internal ("CCT network") transfer - so a
// mixed CCT-network + bridge history reads clearly per row. Purely a
// display label derived from the transaction's own recorded Type,
// same convention tx.js's networkLabelForTx (kept in sync, see that
// file) already uses on the detail page.
function networkLabelForTx(tx) {
    const bridgeTypes = ["BRIDGE_DEPOSIT", "BRIDGE_WITHDRAW", "BRIDGE_BTC_DEPOSIT", "BRIDGE_BTC_WITHDRAW"];
    return bridgeTypes.includes(tx.Type) ? "Bridge" : "CCT network";
}

// --- History section grouping ---------------------------------------------
//
// Three broad buckets, not a full one-category-per-feature scheme - easy
// to scan at a glance rather than fragmenting a 7-row list into a dozen
// single-item headers. "Money Movement" is anything that actually moved
// value (trades, loans, staking, vault/bridge, treasury); "Account &
// Settings" is identity/registration/configuration actions with no value
// movement of their own; everything else (governance, admin-only,
// reporting-only transactions) falls to "Other" - same safe-fallback
// philosophy api/explorer_summary.go's buildTransactionSummary uses for
// its own default case, so a type this list doesn't know about yet still
// gets a home instead of being silently dropped.
const HISTORY_SECTION_MONEY_MOVEMENT = new Set([
    "BORROW", "LEND", "EXCHANGE", "STAKE_ETH", "UNSTAKE_ETH", "MINT_OETH", "BURN_OETH",
    "CLAIM_REWARD", "REPAY", "LOCK_COLLATERAL", "REWARD",
    "BRIDGE_DEPOSIT", "BRIDGE_WITHDRAW", "BRIDGE_BTC_DEPOSIT", "BRIDGE_BTC_WITHDRAW",
    "VAULT_RELEASE_COMPLETE", "VAULT_LOCK_ATTEST", "VAULT_BURN", "VAULT_RELEASE_APPROVE",
    "POOL_ADD_LIQUIDITY", "POOL_REMOVE_LIQUIDITY", "POOL_SWAP", "SWAP_VIA_CCT", "FAIR_VALUE_SWAP",
    "P2P_OFFER_CREATE", "P2P_OFFER_ACCEPT", "P2P_OFFER_CANCEL",
    "P2P_EXTERNAL_ACCEPT", "P2P_SELLER_CONFIRM_PAID", "P2P_BUYER_MARK_PAID",
    "P2P_BUYER_CONFIRM_PAID", "P2P_MODERATOR_RESOLVE", "P2P_SELLER_DISPUTE", "P2P_BUYER_DISPUTE",
    "FAUCET_CLAIM", "YENE_CLAIM",
    "LOAN_OFFER_CREATE", "LOAN_OFFER_DRAW", "LOAN_OFFER_CLOSE",
    "LOAN_REQUEST_CREATE", "LOAN_REQUEST_CANCEL", "LOAN_REQUEST_FULFILL",
    "LOAN_ADD_COLLATERAL", "LOAN_REDEEM_SHORTFALL",
    "BUY_REQUEST_CREATE", "BUY_REQUEST_FULFILL", "BUY_REQUEST_CANCEL", "HUB_TRADE_REQUEST",
    "TOKEN_MINT", "ADMIN_TRANSFER", "ADMIN_NORMALIZE_CCC1_BALANCE",
    "TREASURY_PURCHASE_REQUEST", "TREASURY_PURCHASE_CONFIRM",
    "GENESIS_POOL_ALLOCATION", "GENESIS_POOL_WITHDRAW", "TOKENOMICS_DECIMAL_MIGRATION",
    "ORDER_PLACE", "ORDER_CANCEL", "RC_INFUSE",
    "VALIDATOR_DELEGATE", "VALIDATOR_UNDELEGATE", "PREDICTION_VOTE",
]);
const HISTORY_SECTION_ACCOUNT = new Set([
    "SET_RECOVERY_EMAIL", "RECOVER_WALLET", "HD_WALLET_MIGRATE", "KYC_SUBMIT",
    "VALIDATOR_REGISTER", "VALIDATOR_APPLICATION_CREATE", "VALIDATOR_APPLICATION_SUPPORT",
    "VALIDATOR_APPLICATION_CONFIRM", "VALIDATOR_SET_COMMISSION",
    "MODERATOR_REGISTER", "COMPANY_APPLY", "COMPANY_REVIEW", "COMPANY_UPDATE_CONTACT",
    "COMPANY_REASSIGN_MODERATOR", "COMPANY_UPLOAD_CERTIFICATE",
    "HUB_RATE_SET", "HUB_RULES_SET", "HUB_WORKER_ADD", "HUB_WORKER_REMOVE", "HUB_STOREFRONT_SET",
    "HUB_AUTOMATION_SUBSCRIBE", "HUB_WORKER_SET_AUTOMATION",
    "BRIDGE_VALIDATOR_REGISTER", "RATING_SUBMIT", "TOKEN_REGISTER",
]);

function historySectionFor(tx) {
    if (HISTORY_SECTION_MONEY_MOVEMENT.has(tx.Type)) return "Money Movement";
    if (HISTORY_SECTION_ACCOUNT.has(tx.Type)) return "Account & Settings";
    return "Other";
}
const HISTORY_SECTION_ORDER = ["Money Movement", "Account & Settings", "Other"];

async function loadHistory() {
    const div = document.getElementById("assetHistoryList");

    if (EXTERNAL_CHAIN_SYMBOLS[symbol]) {
        // CreditChain's explorer is CreditChain-internal only (see
        // api/explorer.go) - it must never claim to show history for
        // an independent blockchain, so this deliberately does not
        // link out to it or query it for ETH/BTC.
        const isMainnetNow = window.CC_NETWORK === "mainnet";
        const explorerName = (symbol === "ETH" || symbol === "USDC")
            ? (isMainnetNow ? "Etherscan" : "Sepolia Etherscan")
            : (isMainnetNow ? "Bitcoin" : "Bitcoin testnet");
        div.innerHTML = `<p style="opacity:0.6;">History for ${symbol} isn't shown here - use a ${explorerName} block explorer with your address above.</p>`;
        document.getElementById("assetExplorerLink").style.display = "none";
        return;
    }

    try {
        const address = await assetAddress();
        document.getElementById("assetExplorerLink").href = `explorer?q=${address}`;

        const result = await apiGet(`/explorer/search?q=${address}`);
        const txs = result && result.type === "address" ? (result.address.transactions || []).slice(0, 7) : [];

        if (txs.length === 0) {
            div.innerHTML = `<p style="opacity:0.6;">No transactions yet.</p>`;
            return;
        }

        const rowHtml = (tx) => {
            const time = tx.Timestamp ? new Date(tx.Timestamp * 1000).toLocaleString() : "-";
            const label = escapeHtml(tx.summary || tx.Type);
            const network = networkLabelForTx(tx);
            const externalLink = tx.externalTxLink
                ? ` &middot; <a href="${tx.externalTxLink}" target="_blank" rel="noopener" onclick="event.stopPropagation();" style="color:var(--cc-accent-gold, #c9a227);">view on external network &#8599;</a>`
                : "";
            return `
                <div class="asset-history-row" style="cursor:pointer;" onclick="location.href='tx?id=${tx.ID}'">
                    <span>${label}<br><span style="opacity:0.55;font-size:11px;">${network}${externalLink}</span></span>
                    <span style="opacity:0.6;">${time}</span>
                </div>
            `;
        };

        // Grouped into HISTORY_SECTION_ORDER's three buckets, each kept
        // in the same chronological order the backend already returned -
        // only sections that actually have a transaction get a header,
        // so a wallet with e.g. no Account & Settings activity for this
        // asset doesn't see an empty section.
        const bySection = {};
        for (const tx of txs) {
            const section = historySectionFor(tx);
            (bySection[section] = bySection[section] || []).push(tx);
        }

        div.innerHTML = HISTORY_SECTION_ORDER.filter((section) => bySection[section])
            .map((section) => `
                <div class="asset-history-section-header" style="font-size:11px;text-transform:uppercase;letter-spacing:0.5px;opacity:0.5;margin:14px 0 4px;">${section}</div>
                ${bySection[section].map(rowHtml).join("")}
            `)
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = `<p style="opacity:0.6;">Couldn't load history.</p>`;
    }
}

async function toggleReceivePanel() {
    const panel = document.getElementById("assetReceivePanel");
    if (panel.style.display === "block") {
        panel.style.display = "none";
        return;
    }

    // ETHB and BTCB both need a network choice before Receive can show
    // one address, since each has two genuinely different deposit
    // paths - CCT network is always this wallet's internal CreditChain
    // sub-address (for a transfer from another CreditChain user); the
    // real external network (ERC/BTC) is this wallet's own
    // self-custodial address (the key never leaves the browser).
    // Resolving the wrong one here as if it were the only option would
    // mean funds sent to it could be unrecoverable. On mainnet, the
    // unified "ETH" row (self-custodial + bridged ETHB wei summed -
    // see assets.js) needs the exact same picker: its balance already
    // includes the ETHB side, so Receive has to be able to hand out
    // either a CCT-network address (internal transfer from another
    // CreditChain user) or the real self-custodial deposit address
    // (ERC network) - CreditChain never holds a signing key for
    // either side of it.
    if (symbol === "ETHB" || symbol === "BTCB" || (symbol === "ETH" && window.CC_NETWORK === "mainnet")) {
        const netBtn2 = document.getElementById("assetReceiveNetBtn2");
        if (symbol === "BTCB") {
            netBtn2.innerText = "BTC network";
            netBtn2.setAttribute("onclick", "loadReceiveForNetwork('BTC')");
        } else {
            netBtn2.innerText = "ERC network";
            netBtn2.setAttribute("onclick", "loadReceiveForNetwork('ERC')");
        }
        document.getElementById("assetReceiveNetworkPicker").style.display = "block";
        document.getElementById("assetReceiveQrArea").style.display = "none";
        panel.style.display = "block";
        return;
    }

    const rawAddress = await assetAddress();
    const address = EXTERNAL_CHAIN_SYMBOLS[symbol] ? rawAddress : formatAddress(rawAddress);
    document.getElementById("assetReceiveAddress").innerText = address;
    QR.renderToCanvas(document.getElementById("assetQrCanvas"), address, 6);
    panel.style.display = "block";
}

// loadReceiveForNetwork resolves the actual deposit address once the
// user picks a network for ETHB's (or BTCB's, or mainnet's unified
// "ETH" row's) Receive flow. CCT network reuses assetAddress()'s
// normal CreditChain sub-address (instant, internal sends only). ERC
// (or BTC) network resolves to this wallet's own self-custodial
// address - the same one already shown elsewhere in the app - since
// CreditChain never holds a signing key for either side of the
// bridge. That address is registered for deposit-watching so a
// wallet that's never opened send-eth.html/send-bridged-btc.html yet
// still gets its deposits detected.
async function loadReceiveForNetwork(network) {
    document.getElementById("assetReceiveNetworkPicker").style.display = "none";
    document.getElementById("assetReceiveQrArea").style.display = "block";
    const statusEl = document.getElementById("assetCopyStatus");
    const addressEl = document.getElementById("assetReceiveAddress");

    if (network === "CCT") {
        // For the unified mainnet "ETH" row, assetAddress() resolves to
        // the self-custodial external-chain address (see
        // EXTERNAL_CHAIN_SYMBOLS above) - wrong here, since CCT network
        // means "another CreditChain user sending you ETHB internally,"
        // which resolves against this wallet's own plain address, same
        // as every other CCT-network recipient lookup in this app.
        const address = symbol === "ETH" ? wallet.address : await assetAddress();
        addressEl.innerText = address;
        QR.renderToCanvas(document.getElementById("assetQrCanvas"), address, 6);
        statusEl.innerText = "CCT network address - instant, for CreditChain-internal sends only.";
        return;
    }

    // BTC network: unlike ETHB's ERC path, BTCB has no separate
    // custodial signing key - deposits are detected by watching the
    // user's own self-custodial Bitcoin (testnet) HD address directly
    // (see core/btc_address.go's doc comment). Register it for
    // watching here (idempotent - btcAddressStore.Set is a no-op if
    // this address is already registered) so a fresh wallet that's
    // never opened send-bridged-btc.html yet still gets its deposits
    // detected.
    if (network === "BTC") {
        const btcAddress = externalChainAddress("BTC");
        if (!btcAddress) {
            addressEl.innerText = "";
            statusEl.innerText = "No Bitcoin account derived for this wallet.";
            return;
        }
        if (!btcAddressMatchesNetwork(btcAddress)) {
            addressEl.innerText = "";
            statusEl.innerHTML = `This address was derived for the other network - <a href="btc-network-fix" style="color:var(--cc-accent-gold, #c9a227);">re-enter your recovery phrase to fix it</a> before receiving on this network.`;
            return;
        }
        addressEl.innerText = btcAddress;
        QR.renderToCanvas(document.getElementById("assetQrCanvas"), btcAddress, 6);
        apiPostSigned("/btc-bridge/register-address", { btcAddress }).catch(() => {});
        // Two ways to use this address: a plain send still auto-credits
        // BTCB (legacy mirror-model, kept running - see api/server.go's
        // doc comment), or lock the same real BTC into the new vault
        // for a real 1:1-backed, withdrawable balance instead.
        statusEl.innerHTML = `Sending BTC (testnet) here auto-credits BTCB. For a real 1:1-backed, withdrawable balance instead, use ` +
            `<a href="vault-deposit-btc" style="color:var(--cc-accent-gold, #c9a227);">Vault BTC to CreditChain network</a>.`;
        return;
    }

    // For the unified mainnet "ETH" row, "ERC network" means the real
    // self-custodial address already shown on the Assets list (free,
    // browser-held key, chain ID confirmed live on the server) - NOT
    // the separate KMS-custodial address /eth-custodial/create
    // provisions below, which costs a real 100 CCT anti-spam fee (see
    // api/eth_custodial.go's EthCustodialAccountFeeCCT) and exists for
    // a different purpose (auto-detected deposit crediting into ETHB).
    // Routing this button through that paid path was a bug - a wallet
    // that already has a free real address shouldn't be asked to pay
    // for a second one just to see it on the Receive panel.
    if (symbol === "ETH") {
        const address = externalChainAddress("ETH");
        if (!address) {
            addressEl.innerText = "";
            statusEl.innerHTML = `No self-custodial Ethereum address found for this wallet yet - <a href="outernet" style="color:var(--cc-accent-gold, #c9a227);">go to OUTERNET</a>, which will prompt you to set one up (needed to receive real ETH).`;
            return;
        }
        addressEl.innerText = address;
        QR.renderToCanvas(document.getElementById("assetQrCanvas"), address, 6);
        registerEthWatchAddressOnce(wallet, address);
        statusEl.innerText = "Real Ethereum mainnet address - the key never leaves this browser. No fee to receive here.";
        return;
    }

    // Plain testnet ETHB row's "ERC network" step. Mirrors the BTC
    // branch above exactly (and the unified mainnet "ETH" branch just
    // above it) - no CreditChain-held custodial account is ever
    // provisioned here. The deposit address IS this wallet's own
    // self-custodial ETH address; register it for deposit-watching
    // (idempotent, same as the BTC call above) so a wallet that's never
    // opened send-eth.html yet still gets its deposits detected.
    const ethAddress = externalChainAddress("ETH");
    if (!ethAddress) {
        addressEl.innerText = "";
        statusEl.innerHTML = `No self-custodial Ethereum address found for this wallet yet - <a href="outernet" style="color:var(--cc-accent-gold, #c9a227);">go to OUTERNET</a>, which will prompt you to set one up (needed to receive real ETH).`;
        return;
    }
    addressEl.innerText = ethAddress;
    QR.renderToCanvas(document.getElementById("assetQrCanvas"), ethAddress, 6);
    registerEthWatchAddressOnce(wallet, ethAddress);
    // Same as the BTC branch above - a plain send still auto-credits
    // ETHB (legacy mirror-model, kept running), or the same address can
    // be locked into the new vault for a real 1:1-backed balance.
    statusEl.innerHTML = `Sending ETH here auto-credits ETHB. For a real 1:1-backed, withdrawable balance instead, use ` +
        `<a href="vault-deposit-eth" style="color:var(--cc-accent-gold, #c9a227);">Vault ETH or USDC to CreditChain network</a>.`;
}

function copyAssetAddress() {
    const address = document.getElementById("assetReceiveAddress").innerText;
    if (navigator.clipboard) {
        navigator.clipboard
            .writeText(address)
            .then(() => {
                document.getElementById("assetCopyStatus").innerText = "Copied!";
            })
            .catch(() => {});
    }
}

document.getElementById("assetSendBtn").addEventListener("click", () => {
    if (symbol === "ETH") {
        // The balance shown on this page is ETH_CCC1 (see loadHero
        // above), so "Send" opens vault-withdraw.html, which offers
        // both ways to spend it: instantly to another CreditChain
        // user's balance (default), or burn+release to a real address -
        // a direct-from-the-real-address send is still reachable via
        // the "Also available on" link below for anyone who wants to
        // bypass the vault entirely (e.g. gas-only amounts, or funds not
        // yet vaulted).
        location.href = "vault-withdraw?chain=ETH";
        return;
    }
    if (symbol === "USDC") {
        // Same reasoning as ETH above - this page now shows USDC_CCC1.
        location.href = "vault-withdraw?chain=USDC";
        return;
    }
    if (symbol === "ETHB") {
        location.href = "send-bridged-eth";
        return;
    }
    if (symbol === "BTCB") {
        // Same reasoning as ETH above - this page shows BTC_CCC1.
        location.href = "vault-withdraw?chain=BTC";
        return;
    }
    if (symbol === "BTC") {
        location.href = "send-bridged-btc";
        return;
    }
    location.href = `send?symbol=${symbol}`;
});
document.getElementById("assetReceiveBtn").addEventListener("click", toggleReceivePanel);

loadHero();
loadChart();
loadHistory();
