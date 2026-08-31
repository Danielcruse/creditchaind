const p2pWallet = getWallet();

// Every symbol a hub can post a rate for - same list Pool/Hub Rate
// already traded natively before this redesign. ETHTEST was replaced
// by ETHB (the real bridged/imported ETH) as the user-facing "ETH"
// symbol here; USDCTEST stays the same token under the hood, just
// relabeled "USDC" for display.
//
// On mainnet, ETHB/USDCTEST/BTCB (testnet-only mirror-model/synthetic
// tokens) are replaced with ETH_CCC1/USDC_CCC1/BTC_CCC1 (real vault-
// locked balances) - without this, mainnet Marketplace offers could
// only ever be posted against balances mainnet users don't actually
// hold, since api/p2p_offers.go's resolveTokenStore already supports
// these CCC1 symbols (see api/token_transfer.go's ETH_CCC1/USDC_CCC1/
// BTC_CCC1 cases) but nothing in this UI ever offered them. Same
// "mainnet should use only mainnet features" treatment already applied
// to send.js/staking.js. p2pSymbolLabels keeps every symbol (both
// networks) so a historical offer/rate created under the old testnet
// symbols still displays with a real label instead of falling back to
// the raw symbol string.
const p2pNativeSymbols = window.CC_NETWORK === "mainnet"
    ? ["CCT", "ETH_CCC1", "USDC_CCC1", "BTC_CCC1", "REPT"]
    : ["CCT", "ETHB", "USDCTEST", "BTCB", "REPT"];
const p2pSymbolLabels = {
    CCT: "CCT", REPT: "REPT",
    ETHB: "ETH", USDCTEST: "USDC", BTCB: "BTC",
    ETH_CCC1: "ETH", USDC_CCC1: "USDC", BTC_CCC1: "BTC",
};

// Settlement states that mean "nothing left to do here" - once a
// trade reaches one of these, it drops out of the "My Active Trades"
// list.
const TERMINAL_SETTLEMENT_STATUSES = ["CONFIRMED", "RESOLVED_RELEASE", "RESOLVED_REFUND", "EXPIRED", "AUTO_RELEASED"];

// Settlement states that mean the trade closed WITH funds actually
// moving - the subset of TERMINAL_SETTLEMENT_STATUSES that earns the
// in-chat completion banner. EXPIRED is deliberately excluded: nothing
// was ever paid, so there's nothing to announce as "credited."
const FUNDS_MOVED_SETTLEMENT_STATUSES = ["CONFIRMED", "RESOLVED_RELEASE", "RESOLVED_REFUND", "AUTO_RELEASED"];

// Human-readable labels for core.SettlementStatus (core/p2p_offer.go) -
// used everywhere a trade's progress is surfaced (the Active Trades
// list, the Timer & Payment panel) so a user reads "awaiting seller
// verification" instead of the raw "SELLER_CONFIRMED" enum string.
// AWAITING_PAYMENT is deliberately absent - the countdown panel already
// covers that state with its own live timer, this is only consulted
// once a trade has moved past it (see startP2PCountdownFor).
const P2P_SETTLEMENT_STATUS_LABELS = {
    AWAITING_PAYMENT: "Awaiting payment",
    SELLER_CONFIRMED: "Payment marked sent - awaiting seller verification",
    CONFIRMED: "Transaction concluded - funds released",
    DISPUTED: "Disputed - awaiting moderator review",
    RESOLVED_RELEASE: "Transaction concluded - dispute resolved, funds released to buyer",
    RESOLVED_REFUND: "Transaction concluded - dispute resolved, refunded to seller",
    EXPIRED: "Transaction concluded - payment window expired, escrow returned",
    AUTO_RELEASED: "Transaction concluded - auto-released after no response",
};
function p2pFriendlySettlementStatus(status) {
    return P2P_SETTLEMENT_STATUS_LABELS[status] || status || "-";
}

// Short form of the same labels, for the compact uppercase badge on
// each Active Trades row (.active-trade-row-status - not enough room
// for a full sentence) - every terminal status collapses to
// "Concluded" there since a concluded trade already drops off this
// list entirely (see myActiveTrades/TERMINAL_SETTLEMENT_STATUSES), so
// in practice only AWAITING_PAYMENT/SELLER_CONFIRMED/DISPUTED ever
// actually show here; the terminal entries exist for completeness.
const P2P_SETTLEMENT_STATUS_SHORT_LABELS = {
    AWAITING_PAYMENT: "Awaiting Payment",
    SELLER_CONFIRMED: "Awaiting Verification",
    CONFIRMED: "Concluded",
    DISPUTED: "Disputed",
    RESOLVED_RELEASE: "Concluded",
    RESOLVED_REFUND: "Concluded",
    EXPIRED: "Concluded",
    AUTO_RELEASED: "Concluded",
};
function p2pShortSettlementStatus(status) {
    return P2P_SETTLEMENT_STATUS_SHORT_LABELS[status] || status || "-";
}

// Fixed, platform-wide baseline rules shown before ANY hub trade chat
// becomes usable - independent of whatever a specific hub's own free-
// text HubRules.Text says (that's shown right underneath, per-offer).
// UI copy only, not protocol state, so no backend field is needed.
const PLATFORM_P2P_RULES = [
    "Never mark payment as sent, or confirm receipt, unless you have personally verified it in your own account - not a screenshot, not a message from the other party.",
    "Everything in this chat and every action you take is recorded on-chain and reviewable by a moderator.",
    "Disputes are decided by the assigned moderator's judgment call, not an automatic refund - cooperate and provide evidence if asked.",
    "Falsely claiming payment was sent, or falsely denying a real payment was received, is fraud - if a moderator confirms this happened, you lose reputation and it is broadcast network-wide.",
    "Escrow releases automatically the moment the receiving side confirms - there is no undo once you click confirm.",
];

// Offer IDs whose rules-intro gate has already been dismissed this
// browser session - deliberately in-memory only (not persisted), a
// pure UI gate with no new backend state. Re-showing on a fresh page
// load for an already-familiar user is an acceptable minor friction.
const p2pChatRulesAcknowledged = new Set();

// Tracks the last SettlementStatus we rendered a completion banner
// for, per offer ID, so the banner only appears once, right when the
// status newly reaches a funds-moved terminal state - not on every
// re-render while the chat stays open afterward.
const p2pChatLastBannerStatus = {};

let p2pCompaniesCache = [];
let currentHubAddress = null;
let p2pHubRatesCache = [];
let p2pOffersCache = [];
let currentP2PChatOfferId = null;
let hubRulesCache = "";
// Payment window, in minutes, per trade direction ("BUY_FROM_HUB" /
// "SELL_TO_HUB") - drives the P2P chat's countdown timer (see
// getPaymentWindowMinutes below). Falls back to
// DEFAULT_PAYMENT_WINDOW_MINUTES for any direction the hub hasn't set.
let hubRulesWindowsCache = {};
const DEFAULT_PAYMENT_WINDOW_MINUTES = 30;
let pendingTradeRequest = null; // { symbol, direction } - held while the rules modal is open
let currentHubIsMine = false;
let currentHubIsWorker = false; // wallet is an active delegated worker for currentHubAddress, but not the hub itself

// PLATFORM_FEE_PERCENT must match core.PlatformFeeBps (api/hub_rate.go
// and friends) - duplicated here since the frontend has no way to read
// a Go constant, but it's a fixed, rarely-changing protocol rule, not
// user data, so this is a deliberate small duplication rather than
// adding a whole endpoint just to serve one number.
const PLATFORM_FEE_PERCENT = 0.3;

function p2pInit() {
    if (!p2pWallet) {
        document.getElementById("p2pLoggedOut").style.display = "block";
    }
    loadSymbolOptions();
    loadOffAppCurrencyOptions();
    loadHubDirectory();
    refreshActiveTradesWidget();
}

async function loadSymbolOptions() {
    const select = document.getElementById("hubRateSymbol");
    if (select) {
        select.innerHTML = p2pNativeSymbols
            .map((s) => `<option value="${s}">${p2pSymbolLabels[s] || s}</option>`)
            .join("");
    }
    onHubRateModeChange();
}

// loadOffAppCurrencyOptions populates the off-app currency dropdown
// (hubRateFiatSymbol) from GET /hub-rates/currencies - the server's own
// allowlist (core.AllowedOffAppCurrencies), fetched rather than
// hardcoded here so the frontend can never drift from what a rate
// submission will actually be accepted for. Deliberately a fixed
// dropdown, not a free-text field: a Trading Hub could previously type
// any string here, including USD, which this marketplace does not
// support settling in (see core.AllowedOffAppCurrencies' doc comment -
// facilitating USD peer-to-peer settlement is money transmission under
// US law, and this platform holds no license for that anywhere).
async function loadOffAppCurrencyOptions() {
    const select = document.getElementById("hubRateFiatSymbol");
    if (!select) return;
    try {
        const currencies = await apiGet("/hub-rates/currencies");
        select.innerHTML =
            `<option value="">Select a currency...</option>` +
            (currencies || []).map((c) => `<option value="${c}">${c}</option>`).join("");
    } catch (e) {
        console.error(e);
    }
}

// --- Owner tools: mode-aware rate posting ------------------------------

function onHubRateModeChange() {
    const mode = document.getElementById("hubRateMode").value;
    const fiatInput = document.getElementById("hubRateFiatSymbol");
    const counterSelect = document.getElementById("hubRateCounterSymbol");
    const chosenSymbol = document.getElementById("hubRateSymbol").value;

    if (mode === "ONCHAIN") {
        fiatInput.style.display = "none";
        counterSelect.style.display = "";
        counterSelect.innerHTML = p2pNativeSymbols
            .filter((s) => s !== chosenSymbol)
            .map((s) => `<option value="${s}">${p2pSymbolLabels[s] || s}</option>`)
            .join("");
        showHubRateExternalReference();
    } else {
        fiatInput.style.display = "";
        counterSelect.style.display = "none";
        document.getElementById("hubRateExternalRefPrice").innerText = "";
    }
}

// showHubRateExternalReference is purely informational (see
// api/price_oracle.go's GET /price/external) - the hub's actual
// SellRate/BuyRate stays their own free choice, exactly as before;
// this just shows a live market reference alongside the form so a hub
// can eyeball whether their own quote is in the right ballpark.
async function showHubRateExternalReference() {
    const el = document.getElementById("hubRateExternalRefPrice");
    const counter = document.getElementById("hubRateCounterSymbol").value;
    if (counter !== "ETHB" && counter !== "ETHTEST" && counter !== "ETH_CCC1") {
        el.innerText = "";
        return;
    }
    try {
        const prices = await apiGet("/price/external");
        if (!prices.ethUsd) {
            el.innerText = "";
            return;
        }
        el.innerText = `Reference: 1 ETH ≈ $${prices.ethUsd.toFixed(2)} (external market price)`;
    } catch (e) {
        el.innerText = "";
    }
}

// --- Hub directory: one bold rectangular card per hub -------------------

// p2pHubDirectoryFiltersCache is keyed by hub address, from
// GET /hub-directory-filters (api/hub_directory_filters.go) - each
// hub's active off-app currencies and its own hub-authored payment-
// provider list, populated alongside the directory itself so
// applyHubDirectoryFilters can filter purely client-side afterward
// (no re-fetch per filter change).
let p2pHubDirectoryFiltersCache = {};

// p2pConnectedHubCache holds {connectedHub, hubName} for the current
// wallet, fetched once per loadHubDirectory call - "" connectedHub
// means either no wallet is present in this browser, or this wallet
// has never followed a Trading Hub's own shareable link (see
// core.Wallet.ConnectedHub's doc comment).
let p2pConnectedHubCache = { connectedHub: "", hubName: "" };

async function loadHubDirectory() {
    try {
        const [companies, filters] = await Promise.all([
            apiGet("/companies"),
            apiGet("/hub-directory-filters").catch(() => []),
            loadConnectedHub(),
        ]);
        p2pCompaniesCache = (companies || []).filter((c) => c.Status === "VERIFIED");

        p2pHubDirectoryFiltersCache = {};
        (filters || []).forEach((f) => {
            p2pHubDirectoryFiltersCache[f.hub] = f;
        });
        populateHubDirectoryFilterOptions(filters || []);

        renderConnectedHubBanner();
        renderHubDirectoryCards(sortConnectedHubFirst(p2pCompaniesCache));
    } catch (e) {
        console.error(e);
    }
}

// loadConnectedHub fetches (and caches) the current wallet's connected
// hub - a no-op if there's no wallet in this browser at all, since the
// endpoint has nothing meaningful to look up for an anonymous visitor.
async function loadConnectedHub() {
    p2pConnectedHubCache = { connectedHub: "", hubName: "" };
    if (!p2pWallet || !p2pWallet.address) return;
    try {
        const r = await apiGet(`/wallet/connected-hub?address=${p2pWallet.address}`);
        p2pConnectedHubCache = { connectedHub: r.connectedHub || "", hubName: r.hubName || "" };
    } catch (e) {
        console.error(e);
    }
}

// sortConnectedHubFirst puts the wallet's connected hub's card first,
// leaving every other hub in whatever order they were already in -
// this is the "recommend the hub that connected them" behavior from
// the marketplace side, alongside the banner below.
function sortConnectedHubFirst(companies) {
    const hub = p2pConnectedHubCache.connectedHub;
    if (!hub) return companies;
    const idx = companies.findIndex((c) => c.Address === hub);
    if (idx <= 0) return companies;
    const copy = companies.slice();
    const [match] = copy.splice(idx, 1);
    copy.unshift(match);
    return copy;
}

function renderConnectedHubBanner() {
    const banner = document.getElementById("connectedHubBanner");
    if (!banner) return;
    const { connectedHub, hubName } = p2pConnectedHubCache;
    if (!connectedHub) {
        banner.style.display = "none";
        return;
    }
    banner.style.display = "block";
    banner.innerHTML = `
        <div class="card" style="border:2px solid var(--cc-accent-gold, #16B8A6);margin-bottom:10px;">
            <p style="margin:0;font-size:13px;">
                Continue trading with <strong>${escapeHtml(hubName || "your Trading Hub")}</strong>
                <button style="margin-left:10px;" onclick="viewHub('${connectedHub}')">Open &rarr;</button>
            </p>
        </div>`;
}

// populateHubDirectoryFilterOptions builds the two filter dropdowns'
// option lists from whatever's actually posted across every hub right
// now (a hub-authored payment-provider name only appears here once at
// least one verified hub has actually saved it) - preserves whichever
// value was already selected, if it's still a valid option, so
// changing hubs/reloading the directory doesn't silently reset an
// in-progress filter.
function populateHubDirectoryFilterOptions(filters) {
    const currencies = new Set();
    const providers = new Set();
    filters.forEach((f) => {
        (f.currencies || []).forEach((c) => currencies.add(c));
        (f.serviceProviders || []).forEach((p) => providers.add(p));
    });

    const fillSelect = (id, values, placeholder) => {
        const select = document.getElementById(id);
        if (!select) return;
        const previous = select.value;
        select.innerHTML =
            `<option value="">${placeholder}</option>` +
            Array.from(values)
                .sort((a, b) => a.localeCompare(b))
                .map((v) => `<option value="${v}">${v}</option>`)
                .join("");
        if (previous && values.has(previous)) select.value = previous;
    };

    fillSelect("hubDirectoryCurrencyFilter", currencies, "Any currency");
    fillSelect("hubDirectoryProviderFilter", providers, "Any payment method");
}

// applyHubDirectoryFilters re-renders the already-loaded hub list
// filtered to hubs matching BOTH the selected currency (if any) and
// selected payment provider (if any) - purely client-side over
// p2pHubDirectoryFiltersCache, no new request per change.
function applyHubDirectoryFilters() {
    const currency = document.getElementById("hubDirectoryCurrencyFilter").value;
    const provider = document.getElementById("hubDirectoryProviderFilter").value;

    if (!currency && !provider) {
        renderHubDirectoryCards(sortConnectedHubFirst(p2pCompaniesCache));
        return;
    }

    const filtered = p2pCompaniesCache.filter((c) => {
        const f = p2pHubDirectoryFiltersCache[c.Address];
        if (!f) return false;
        if (currency && !(f.currencies || []).includes(currency)) return false;
        if (provider && !(f.serviceProviders || []).includes(provider)) return false;
        return true;
    });
    renderHubDirectoryCards(sortConnectedHubFirst(filtered), true);
}

function renderHubDirectoryCards(companies, isFiltered) {
    const body = document.getElementById("hubDirectoryBody");
    if (companies.length === 0) {
        body.innerHTML = isFiltered
            ? `<p class="explorer-empty">No Trading Hubs match that filter right now.</p>`
            : `<p class="explorer-empty">No verified Trading Hubs yet.</p>`;
        return;
    }

    body.innerHTML = companies
        .map(
            (c) => `
        <div class="hub-card">
            <div class="hub-card-namerow">
                <span class="hub-card-name">${escapeHtml(c.Name)}</span>
                <span class="hub-card-badge" title="Moderator-reviewed testnet badge - not real KYC/AML" style="color:var(--cc-accent-gold, #16B8A6);">&#9989; Verified</span>
            </div>
            <button class="hub-view-btn" onclick="viewHub('${c.Address}')">View</button>
        </div>`
        )
        .join("");
}

function showHubDirectory() {
    currentHubAddress = null;
    document.getElementById("p2pHubDetailView").style.display = "none";
    document.getElementById("p2pNativeSwapView").style.display = "none";
    document.getElementById("p2pDirectoryView").style.display = "block";
    loadHubDirectory();
}

// showNativeSwapView switches to the Native Token Swap page for
// whichever hub is currently open - a separate page from the main
// hub detail/Off-App view, not just a lower section on the same
// scroll. Reuses the already-loaded p2pHubRatesCache (loadHubRates
// fills both sessions' sections regardless of which one is visible),
// so switching is instant with no extra network round-trip.
function showNativeSwapView() {
    const company = p2pCompaniesCache.find((c) => c.Address === currentHubAddress);
    document.getElementById("nativeSwapHubName").innerText = company ? company.Name : "Trading Hub";
    document.getElementById("nativeSwapHubAddress").innerText = currentHubAddress;

    document.getElementById("p2pHubDetailView").style.display = "none";
    document.getElementById("p2pNativeSwapView").style.display = "block";
}

function showHubDetailFromSwap() {
    document.getElementById("p2pNativeSwapView").style.display = "none";
    document.getElementById("p2pHubDetailView").style.display = "block";
}

async function viewHub(address) {
    currentHubAddress = address;
    document.getElementById("p2pDirectoryView").style.display = "none";
    document.getElementById("p2pHubDetailView").style.display = "block";

    const company = p2pCompaniesCache.find((c) => c.Address === address);
    document.getElementById("hubDetailName").innerText = company ? company.Name : "Trading Hub";
    document.getElementById("hubDetailAddress").innerText = address;

    currentHubIsMine = !!(p2pWallet && p2pWallet.address === address);
    currentHubIsWorker = false;
    if (p2pWallet && !currentHubIsMine) {
        try {
            const workers = await apiGet(`/hub-workers?hub=${address}`);
            currentHubIsWorker = (workers || []).some((w) => w.WorkerAddress === p2pWallet.address && w.Active);
        } catch (e) {
            console.error(e);
        }
    }

    document.getElementById("hubDetailOwnerTools").style.display = (currentHubIsMine || currentHubIsWorker) ? "block" : "none";
    document.getElementById("hubOwnerOnlyTools").style.display = currentHubIsMine ? "block" : "none";
    document.getElementById("hubAgentApplyBox").style.display =
        (p2pWallet && !currentHubIsMine && !currentHubIsWorker) ? "block" : "none";
    document.getElementById("hubAgentApplyStatus").innerText = "";
    document.getElementById("hubKYCBox").style.display = (p2pWallet && !currentHubIsMine) ? "block" : "none";
    if (p2pWallet && !currentHubIsMine) refreshHubKYCStatus();
    document.getElementById("rateManagerHeading").innerText = currentHubIsMine
        ? "Manage Your Listing"
        : `Manage Your Own Listing (operating under ${company ? company.Name : "this hub"})`;

    await loadHubRates();
    await loadHubRulesForDisplay(address);
    await loadHubStorefrontForDisplay(address);
    if (currentHubIsMine || currentHubIsWorker) {
        showHubPanelTab("trades");
        await loadHubOpenTrades();
        await loadHubNotifications();
    }
    if (currentHubIsMine) {
        document.getElementById("hubRulesText").value = hubRulesCache;
        await loadHubWorkers();
        await loadHubAgentApplications();
        await loadHubKYCRecords();
        await loadHubAutomationStatus();
        document.getElementById("hubStorefrontHtml").value = hubStorefrontCache;
    }
}

// --- Hub control panel: sidebar tab switching ---------------------------

function showHubPanelTab(tab) {
    const tradesTab = document.getElementById("hubPanelTrades");
    const settingsTab = document.getElementById("hubPanelSettings");
    const tradesBtn = document.getElementById("hubPanelNavTrades");
    const settingsBtn = document.getElementById("hubPanelNavSettings");

    tradesTab.style.display = tab === "trades" ? "block" : "none";
    settingsTab.style.display = tab === "settings" ? "block" : "none";
    tradesBtn.classList.toggle("active", tab === "trades");
    settingsBtn.classList.toggle("active", tab === "settings");

    if (tab === "trades") loadHubOpenTrades();
}

// Every trade this hub (the owner, seeing everything under its
// verified badge) or this operator (seeing only their own listing's
// trades - see core.HubRate.OperatorAddress) is a party to and hasn't
// been fully settled/cancelled yet. P2POffer has no server-side "list
// by hub" filter, so this pulls the full offer list (already cached
// elsewhere via loadOffers) and filters client-side.
const OPEN_TRADE_STATUSES_EXCLUDE = ["CANCELLED"];
async function loadHubOpenTrades() {
    const listEl = document.getElementById("hubOpenTradesList");
    if (!listEl || !currentHubAddress) return;

    try {
        const offers = await apiGet("/p2p/offers");
        p2pOffersCache = offers || [];

        // The owner sees every trade under this hub, including every
        // operator's - an operator only sees their own, since each
        // operator's listing is its own separate marketplace.
        const myIdentity = currentHubIsMine ? currentHubAddress : (p2pWallet && p2pWallet.address);

        const mine = p2pOffersCache.filter((o) => {
            const belongsToHub = currentHubIsMine && (o.Creator === currentHubAddress || o.AcceptedBy === currentHubAddress || o.HubAddress === currentHubAddress);
            const belongsToMe = myIdentity && (o.Creator === myIdentity || o.AcceptedBy === myIdentity);
            if (!belongsToHub && !belongsToMe) return false;
            if (OPEN_TRADE_STATUSES_EXCLUDE.includes(o.Status)) return false;
            if (o.PaymentMode === "EXTERNAL" && TERMINAL_SETTLEMENT_STATUSES.includes(o.SettlementStatus)) return false;
            return true;
        });

        if (mine.length === 0) {
            listEl.innerHTML = `<p class="explorer-empty">No open trades right now.</p>`;
            return;
        }

        listEl.innerHTML = mine
            .map((o) => {
                const self = o.Creator === currentHubAddress || o.Creator === myIdentity ? o.Creator : o.AcceptedBy;
                const counterparty = o.Creator === self ? o.AcceptedBy : o.Creator;
                const counterpartyLabel = counterparty ? `${counterparty.slice(0, 8)}...${counterparty.slice(-6)}` : "(awaiting counterparty)";
                const status = o.PaymentMode === "EXTERNAL" ? (o.SettlementStatus || o.Status) : o.Status;
                return `
                <div class="hub-trade-row">
                    <div>
                        <div>${o.OfferAmount} ${p2pSymbolLabels[o.OfferSymbol] || o.OfferSymbol} &harr; ${o.WantAmount} ${p2pSymbolLabels[o.WantSymbol] || o.WantSymbol}</div>
                        <div class="explorer-hash" style="font-size:11px;">with ${counterpartyLabel}</div>
                    </div>
                    <div style="text-align:right;">
                        <div class="hub-trade-status">${status}</div>
                        <button onclick="openP2PChatModal('${o.ID}')">Enter</button>
                    </div>
                </div>`;
            })
            .join("");
    } catch (e) {
        listEl.innerHTML = `<p class="explorer-empty">Failed to load trades.</p>`;
        console.error(e);
    }
}

// --- Off-app / on-chain rate posting -------------------------------------

// HUB_RATE_SCALE must match core.HubRateScale exactly - rates are
// stored server-side as integers scaled by this factor (e.g. a real
// price of 0.000975 is sent/stored as 975) so a fractional price like
// "1 CCT = 0.000975 ETH" doesn't get floored to 0 the way a bare
// parseInt of a decimal string would. Every place this file reads a
// trader-entered rate or displays a stored one must scale/unscale by
// this same constant.
const HUB_RATE_SCALE = 1000000;

// scaleHubRateInput turns a trader-entered decimal string (e.g.
// "0.000975") into the integer the backend stores, rounding to the
// nearest whole scaled unit rather than truncating - parseFloat handles
// the decimal correctly where the old parseInt silently floored any
// fractional price straight to 0.
function scaleHubRateInput(value) {
    const n = parseFloat(value);
    if (!n || n <= 0) return 0;
    return Math.round(n * HUB_RATE_SCALE);
}

// unscaleHubRate turns a stored scaled-integer rate back into the real
// decimal price for display - trims trailing zeros via parseFloat so
// whole-number rates (e.g. "1 CCT = 2 USDC") still display as "2", not
// "2.000000".
function unscaleHubRate(scaledValue) {
    return parseFloat((scaledValue / HUB_RATE_SCALE).toFixed(6));
}

// hubTradeSymbolDecimals/hubBalanceWholeUnit mirror
// api/hub_trade_amount_scale.go's hubTradeAmountDecimals exactly - a
// hub-trade Amount for ETH_CCC1/USDC_CCC1/BTC_CCC1 is that token's own
// smallest unit (wei/6-decimal/sats), not a whole-token count, but
// core.HubRate's SellRate/BuyRate is always quoted per 1 WHOLE unit.
// CCT/REPT/any other symbol stays whole-unit-only (0 decimals) - a hub
// trade in those still only accepts whole numbers, unchanged by this
// fix. decimalToSmallest/smallestToDecimal come from vault.js (see
// p2p.html's script includes) - the same BigInt-safe conversion the
// vault withdraw flow already uses for this exact class of number.
const HUB_TRADE_SYMBOL_DECIMALS = { ETH_CCC1: 18, USDC_CCC1: 6, BTC_CCC1: 8 };
const HUB_TRADE_CCC1_CHAIN = { ETH_CCC1: "ETH", USDC_CCC1: "USDC", BTC_CCC1: "BTC" };

function hubTradeSymbolDecimals(symbol) {
    return HUB_TRADE_SYMBOL_DECIMALS[symbol] || 0;
}

// hubBalanceWholeUnit converts a smallest-unit balance (as returned by
// hubRateView's HubSymbolBalance/HubCounterBalance, api/hub_rate.go)
// into the whole-unit decimal a trader's amount field is actually
// comparable against - a no-op for whole-unit-native symbols.
function hubBalanceWholeUnit(smallestValue, symbol) {
    const decimals = hubTradeSymbolDecimals(symbol);
    if (!decimals) return smallestValue;
    return parseFloat(smallestToDecimal(String(smallestValue), decimals));
}

// postHubSellOffer/postHubBuyOffer are two distinct actions (not one
// combined form) - posting one side never requires or implies the
// other. Since HubRateStore.Set replaces the whole listing record for
// this (hub, symbol, operator, fiatSymbol) key, each function looks up
// whatever the OTHER side is already set to (via the already-loaded
// p2pHubRatesCache) and carries it forward unchanged, so posting a
// sell offer can never silently wipe out an existing buy offer and
// vice versa.
function currentHubRateOperator() {
    return p2pWallet && p2pWallet.address === currentHubAddress ? "" : (p2pWallet && p2pWallet.address) || "";
}

async function submitHubRateSide(side) {
    const symbol = document.getElementById("hubRateSymbol").value;
    const mode = document.getElementById("hubRateMode").value;
    const fiatSymbol = mode === "ONCHAIN"
        ? document.getElementById("hubRateCounterSymbol").value
        : document.getElementById("hubRateFiatSymbol").value;
    const active = document.getElementById("hubRateActive").checked;
    const statusEl = document.getElementById("hubRateSetStatus");

    if (!fiatSymbol) {
        statusEl.innerText = mode === "ONCHAIN"
            ? "Choose a counter token."
            : "Choose an off-app currency from the list.";
        return;
    }

    const existing = findRate(symbol, mode, currentHubRateOperator(), fiatSymbol);
    let sellRate = existing ? existing.SellRate : 0;
    let buyRate = existing ? existing.BuyRate : 0;

    if (side === "sell") {
        sellRate = scaleHubRateInput(document.getElementById("hubRateSellRate").value);
        if (!sellRate) {
            statusEl.innerText = "Enter your sell price first.";
            return;
        }
    } else {
        buyRate = scaleHubRateInput(document.getElementById("hubRateBuyRate").value);
        if (!buyRate) {
            statusEl.innerText = "Enter your buy price first.";
            return;
        }
    }

    try {
        const result = await apiPostSigned("/hub-rates/set", { hub: currentHubAddress, symbol, fiatSymbol, sellRate, buyRate, active, mode });
        statusEl.innerText = `${side === "sell" ? "Sell" : "Buy"} offer pending confirmation (tx ${result.txId}).`;
        setTimeout(loadHubRates, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function postHubSellOffer() {
    await submitHubRateSide("sell");
}

async function postHubBuyOffer() {
    await submitHubRateSide("buy");
}

// hubCardId identifies a card by (direction, symbol, operator, session) -
// session ("OFFAPP" or "ONCHAIN") is which page/session the card lives
// on (see p2p.html's two separate pages), and is still needed even
// though one card now covers every currency *within* that session (see
// renderUnifiedTokenCard's "Trade in for" dropdown): the same symbol
// can be tradeable both off-app AND on-chain at once, and those render
// into two different pages/containers - without session in the id,
// those two cards would collide on the same DOM ids.
function hubCardId(direction, symbol, operator, session) {
    return `hubCard_${direction}_${symbol}_${operator || "hub"}_${session}`;
}

// currencyKey/parseCurrencyKey round-trip a rate's (Mode, FiatSymbol)
// through a single <select> option value - both are needed to find the
// exact rate back (see findRate), since Mode alone can't disambiguate
// if a hub ever reused a label across OFFAPP and ONCHAIN.
function currencyKey(mode, fiatSymbol) {
    return `${mode || "OFFAPP"}|${fiatSymbol}`;
}

function parseCurrencyKey(key) {
    const idx = key.indexOf("|");
    return { mode: key.slice(0, idx), fiatSymbol: key.slice(idx + 1) };
}

function findRate(symbol, mode, operator, fiatSymbol) {
    return p2pHubRatesCache.find(
        (r) =>
            r.Symbol === symbol &&
            (r.Mode || "OFFAPP") === mode &&
            (r.OperatorAddress || "") === (operator || "") &&
            r.FiatSymbol === fiatSymbol
    );
}

// groupHubRatesByAgent turns the flat list of posted rates into
// per-agent (operator address, "" for the hub's own flagship listing)
// Buy/Sell groups, each keyed by symbol - every currency option an
// agent posted for a given symbol+direction lands in the same group,
// which is what lets renderUnifiedTokenCard show them as one card with
// a currency dropdown instead of one card per currency.
function groupHubRatesByAgent(rates) {
    const agents = new Map();
    for (const r of rates) {
        const operator = r.OperatorAddress || "";
        if (!agents.has(operator)) {
            agents.set(operator, { buy: new Map(), sell: new Map() });
        }
        const agent = agents.get(operator);
        if (r.Active && r.SellRate > 0) {
            if (!agent.buy.has(r.Symbol)) agent.buy.set(r.Symbol, []);
            agent.buy.get(r.Symbol).push(r);
        }
        if (r.Active && r.BuyRate > 0) {
            if (!agent.sell.has(r.Symbol)) agent.sell.set(r.Symbol, []);
            agent.sell.get(r.Symbol).push(r);
        }
    }
    return agents;
}

// renderUnifiedTokenCard builds one card per (agent, direction, symbol):
// a "Trade in for" dropdown lets the trader pick which of this agent's
// posted currency options to use, a Rate line reflects whichever is
// currently selected, and two amount fields (CCT / trade currency) stay
// in sync with each other (see onHubCardAmountChange) so either one can
// be the one the trader actually types into.
// renderUnifiedTokenCard is a two-step offer: collapsed by default to
// one compact, selectable summary row (e.g. "Buy USDC - buy USDC with
// NGN from this hub"), matching a real P2P/OTC offer list instead of
// eagerly showing every symbol's full input panel at once. Clicking the
// row (toggleHubCardExpanded) reveals the SAME currency-select/rate/
// amount-inputs/quote/button panel this always had - none of that
// logic changed, only when it's visible.
function renderUnifiedTokenCard(direction, symbol, operator, rateOptions, session) {
    const label = p2pSymbolLabels[symbol] || symbol;
    const isBuy = direction === "BUY_FROM_HUB";
    const id = hubCardId(direction, symbol, operator, session);

    const optionsHtml = rateOptions
        .map((r) => {
            const key = currencyKey(r.Mode, r.FiatSymbol);
            const counterLabel = r.Mode === "ONCHAIN" ? (p2pSymbolLabels[r.FiatSymbol] || r.FiatSymbol) : r.FiatSymbol;
            const suffix = r.Mode === "ONCHAIN" ? "" : " (off-app)";
            return `<option value="${key}">${counterLabel}${suffix}</option>`;
        })
        .join("");

    // The summary line names the FIRST currency option up front (e.g.
    // "buy USDC with NGN from this hub") - a hub offering several
    // currencies for the same symbol still only needs the one row; the
    // currency dropdown inside the expanded panel covers the rest, same
    // as before.
    const firstCounterLabel = rateOptions[0]
        ? (rateOptions[0].Mode === "ONCHAIN" ? (p2pSymbolLabels[rateOptions[0].FiatSymbol] || rateOptions[0].FiatSymbol) : rateOptions[0].FiatSymbol)
        : "";
    const summaryVerb = isBuy ? "buy" : "sell";
    const summarySuffix = isBuy ? "from" : "to";

    return `
        <div class="hub-token-card">
            <div class="hub-token-card-summary" onclick="toggleHubCardExpanded('${direction}', '${symbol}', '${operator}', '${session}')">
                <div>
                    <span class="hub-token-card-summary-title">${isBuy ? "Buy" : "Sell"} ${label}</span>
                    <span class="hub-token-card-summary-sub">${summaryVerb} ${label}${firstCounterLabel ? ` with ${firstCounterLabel}` : ""} ${summarySuffix} this hub</span>
                </div>
                <span class="hub-token-card-chevron" id="${id}_chevron">&rsaquo;</span>
            </div>
            <div class="hub-token-card-detail" id="${id}_detail" style="display:none;">
                <div class="explorer-search" style="margin-bottom:6px;">
                    <select id="${id}_currency" onchange="onHubCardCurrencyChange('${direction}', '${symbol}', '${operator}', '${session}')">
                        ${optionsHtml}
                    </select>
                </div>
                <p style="opacity:0.75;font-size:12px;" id="${id}_rateLine"></p>
                <p style="opacity:0.6;font-size:11px;" id="${id}_capLine"></p>
                <div class="explorer-search" style="margin-bottom:6px;">
                    <input id="${id}_cctAmount" type="number" min="0" step="any" placeholder="Amount of ${label}" oninput="onHubCardAmountChange('${direction}', '${symbol}', '${operator}', '${session}', 'cct')">
                    <button type="button" onclick="fillMaxHubCardAmount('${direction}', '${symbol}', '${operator}', '${session}')" style="flex:0 0 auto;padding:8px 14px;">Max</button>
                    <input id="${id}_currencyAmount" type="number" min="0" step="any" placeholder="Amount in trade currency" oninput="onHubCardAmountChange('${direction}', '${symbol}', '${operator}', '${session}', 'currency')">
                </div>
                <div id="${id}_quote"></div>
                <button onclick="requestHubTradeFromCard('${direction}', '${symbol}', '${operator}', '${session}')">${isBuy ? "Buy" : "Sell"} ${label}</button>
            </div>
        </div>
    `;
}

// toggleHubCardExpanded reveals/hides one offer's detail panel - only
// one at a time per card (not global), so browsing several offers in
// sequence doesn't leave a trail of expanded panels behind.
function toggleHubCardExpanded(direction, symbol, operator, session) {
    const id = hubCardId(direction, symbol, operator, session);
    const detail = document.getElementById(`${id}_detail`);
    const chevron = document.getElementById(`${id}_chevron`);
    if (!detail) return;
    const nowOpen = detail.style.display === "none";
    detail.style.display = nowOpen ? "block" : "none";
    if (chevron) chevron.classList.toggle("open", nowOpen);
    if (nowOpen) {
        updateCardRateDisplay(direction, symbol, operator, session, getCardSelectedRate(direction, symbol, operator, session));
    }
}

function getCardSelectedRate(direction, symbol, operator, session) {
    const id = hubCardId(direction, symbol, operator, session);
    const select = document.getElementById(`${id}_currency`);
    if (!select || !select.value) return null;
    const { mode, fiatSymbol } = parseCurrencyKey(select.value);
    return findRate(symbol, mode, operator, fiatSymbol);
}

function updateCardRateDisplay(direction, symbol, operator, session, rate) {
    const id = hubCardId(direction, symbol, operator, session);
    const label = p2pSymbolLabels[symbol] || symbol;
    const rateLineEl = document.getElementById(`${id}_rateLine`);
    const capLineEl = document.getElementById(`${id}_capLine`);
    if (!rateLineEl || !capLineEl) return;
    if (!rate) {
        rateLineEl.innerText = "";
        capLineEl.innerText = "";
        return;
    }
    const isBuy = direction === "BUY_FROM_HUB";
    const rateValue = unscaleHubRate(isBuy ? rate.SellRate : rate.BuyRate);
    const counterLabel = rate.Mode === "ONCHAIN" ? (p2pSymbolLabels[rate.FiatSymbol] || rate.FiatSymbol) : rate.FiatSymbol;
    rateLineEl.innerText = `Rate: ${rateValue} ${counterLabel} per ${label}`;

    if (isBuy) {
        capLineEl.innerText = `Available: ${hubBalanceWholeUnit(rate.HubSymbolBalance, symbol)} ${label}`;
    } else if (rate.Mode === "ONCHAIN") {
        capLineEl.innerText = `Hub can pay up to: ${hubBalanceWholeUnit(rate.HubCounterBalance || 0, rate.FiatSymbol)} ${counterLabel}`;
    } else {
        capLineEl.innerText = "";
    }
}

// onHubCardCurrencyChange fires when the trader picks a different
// currency for this card - the rate differs per currency, so the Rate
// line and any in-progress quote need to be recomputed rather than
// left showing the previous currency's numbers.
function onHubCardCurrencyChange(direction, symbol, operator, session) {
    const id = hubCardId(direction, symbol, operator, session);
    const rate = getCardSelectedRate(direction, symbol, operator, session);
    updateCardRateDisplay(direction, symbol, operator, session, rate);

    const cctInput = document.getElementById(`${id}_cctAmount`);
    if (cctInput && cctInput.value) {
        onHubCardAmountChange(direction, symbol, operator, session, "cct");
    } else {
        document.getElementById(`${id}_currencyAmount`).value = "";
        document.getElementById(`${id}_quote`).innerHTML = "";
    }
}

// onHubCardAmountChange keeps "Amount of CCT" and "Amount in trade
// currency" in sync - typing into either one recomputes the other from
// the currently selected rate, so a trader can enter either side of
// the trade. Recomputing the just-typed side too (when driven by the
// currency field) snaps to the exact amount the floored CCT amount
// actually costs, rather than showing a currency amount that doesn't
// correspond to any whole-unit CCT amount.
function onHubCardAmountChange(direction, symbol, operator, session, source) {
    const id = hubCardId(direction, symbol, operator, session);
    const rate = getCardSelectedRate(direction, symbol, operator, session);
    const quoteDiv = document.getElementById(`${id}_quote`);
    const cctInput = document.getElementById(`${id}_cctAmount`);
    const currencyInput = document.getElementById(`${id}_currencyAmount`);

    if (!rate) {
        quoteDiv.innerHTML = "";
        return;
    }
    const rateValue = unscaleHubRate(direction === "BUY_FROM_HUB" ? rate.SellRate : rate.BuyRate);
    if (!rateValue) {
        quoteDiv.innerHTML = "";
        return;
    }

    // roundDisplay trims floating-point noise (e.g. 0.19499999999999998)
    // from a rate * amount multiplication down to a clean 6-decimal
    // figure - this is a client-side display estimate only (the
    // trade's real amounts are computed server-side, see
    // requestHubTradeFromCard, which only ever submits the whole-unit
    // Symbol amount).
    const roundDisplay = (n) => parseFloat(n.toFixed(6));

    // ETH_CCC1/USDC_CCC1/BTC_CCC1 trade in genuinely fractional amounts
    // (0.0000567 ETH is a real, tradeable size) - only whole-unit-native
    // symbols (CCT/REPT/etc, hubTradeSymbolDecimals === 0) still floor to
    // an integer, matching what the backend actually accepts for them
    // (see api/hub_trade_amount_scale.go's hubTradeAmountDecimals).
    const fractional = hubTradeSymbolDecimals(symbol) > 0;

    if (source === "cct") {
        const cctAmount = parseFloat(cctInput.value) || 0;
        if (!cctAmount) {
            currencyInput.value = "";
            quoteDiv.innerHTML = "";
            return;
        }
        const currencyAmount = roundDisplay(cctAmount * rateValue);
        currencyInput.value = currencyAmount;
        renderCardQuote(direction, symbol, operator, session, rate, cctAmount, currencyAmount);
    } else {
        const currencyAmount = parseFloat(currencyInput.value) || 0;
        if (!currencyAmount) {
            cctInput.value = "";
            quoteDiv.innerHTML = "";
            return;
        }
        const rawCctAmount = currencyAmount / rateValue;
        const cctAmount = fractional ? roundDisplay(rawCctAmount) : Math.floor(rawCctAmount);
        cctInput.value = cctAmount;
        const exactCurrencyAmount = roundDisplay(cctAmount * rateValue);
        currencyInput.value = exactCurrencyAmount;
        renderCardQuote(direction, symbol, operator, session, rate, cctAmount, exactCurrencyAmount);
    }
}

// renderCardQuote is the pre-purchase confirmation step: as soon as an
// amount is entered (in either field), this renders a distinct,
// clearly-labeled box showing exactly what the trader will owe/receive
// in total - deliberately not just small inline text - so nobody
// clicks Buy/Sell without having already seen the real total.
function renderCardQuote(direction, symbol, operator, session, rate, cctAmount, currencyAmount) {
    const id = hubCardId(direction, symbol, operator, session);
    const quoteDiv = document.getElementById(`${id}_quote`);
    const label = p2pSymbolLabels[symbol] || symbol;
    const counterLabel = rate.Mode === "ONCHAIN" ? (p2pSymbolLabels[rate.FiatSymbol] || rate.FiatSymbol) : rate.FiatSymbol;
    const onAppSuffix = rate.Mode === "ONCHAIN" ? "" : " (off-app)";

    if (!cctAmount || !currencyAmount) {
        quoteDiv.innerHTML = "";
        return;
    }

    // Platform fee: taken from whichever side is RECEIVING at
    // settlement, never from what a trader pays in - see
    // applyPlatformFee/skimPlatformFee in api/apply.go. Off-app trades
    // don't settle the fee until the chat concludes, but the same
    // 0.3%-off-the-receiving-side rule applies there too.
    // netAfterFee floors for whole-unit-native symbols (can't net a
    // fractional CCT) but keeps 6-decimal precision for ETH_CCC1/
    // USDC_CCC1/BTC_CCC1, where flooring a small fractional net would
    // just show "0" - not wrong, but useless as a display estimate.
    const netAfterFee = (amount, symbol) => {
        const net = amount * (1 - PLATFORM_FEE_PERCENT / 100);
        return hubTradeSymbolDecimals(symbol) > 0 ? parseFloat(net.toFixed(6)) : Math.floor(net);
    };
    const feeLine = (net, netSymbol) =>
        `<div style="font-size:11px;opacity:0.75;margin-top:4px;">${PLATFORM_FEE_PERCENT}% platform fee already reflected - you'll net ~${net} ${netSymbol}.</div>`;

    if (direction === "BUY_FROM_HUB") {
        const availableWhole = hubBalanceWholeUnit(rate.HubSymbolBalance, symbol);
        if (cctAmount > availableWhole) {
            quoteDiv.innerHTML = `<div class="p2p-quote-box" style="border-color:#ff6b6b;background:rgba(255,107,107,0.1);">Only ${availableWhole} ${label} available from this hub right now.</div>`;
            return;
        }
        const net = netAfterFee(cctAmount, symbol);
        quoteDiv.innerHTML = `
            <div class="p2p-quote-box">
                <div class="p2p-quote-label">Your Total</div>
                <div class="p2p-quote-total">You will owe ${currencyAmount} ${counterLabel}${onAppSuffix} for ${cctAmount} ${label}</div>
                ${feeLine(net, label)}
            </div>
        `;
    } else {
        const hubCounterAvailable = hubBalanceWholeUnit(rate.HubCounterBalance || 0, rate.FiatSymbol);
        if (rate.Mode === "ONCHAIN" && currencyAmount > hubCounterAvailable) {
            quoteDiv.innerHTML = `<div class="p2p-quote-box" style="border-color:#ff6b6b;background:rgba(255,107,107,0.1);">Hub only has ${hubCounterAvailable} ${counterLabel} available to pay right now.</div>`;
            return;
        }
        const net = netAfterFee(currencyAmount, rate.FiatSymbol);
        quoteDiv.innerHTML = `
            <div class="p2p-quote-box">
                <div class="p2p-quote-label">Your Total</div>
                <div class="p2p-quote-total">You will pay ${cctAmount} ${label} for ${currencyAmount} ${counterLabel}${onAppSuffix}</div>
                ${feeLine(net, counterLabel)}
            </div>
        `;
    }
}

// renderAgentSectionsInto builds the per-agent Buy/Sell section markup
// for one session's worth of rates (already filtered to just OFFAPP or
// just ONCHAIN by the caller) into the given container - shared by both
// pages since the section/card structure is identical, only which
// rates feed it differs.
function renderAgentSectionsInto(containerId, sessionRates, session) {
    const agents = groupHubRatesByAgent(sessionRates);
    const container = document.getElementById(containerId);
    if (!container) return;

    const sections = [];
    // Hub's own flagship listing (operator "") always first, if present.
    const operators = Array.from(agents.keys()).sort((a, b) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)));
    for (const operator of operators) {
        const agent = agents.get(operator);
        if (agent.buy.size === 0 && agent.sell.size === 0) continue;

        const agentLabel = operator === "" ? "Hub Direct" : `Agent ${operator.slice(0, 8)}...${operator.slice(-6)}`;

        const buyCards = Array.from(agent.buy.entries())
            .map(([symbol, opts]) => renderUnifiedTokenCard("BUY_FROM_HUB", symbol, operator, opts, session))
            .join("");
        const sellCards = Array.from(agent.sell.entries())
            .map(([symbol, opts]) => renderUnifiedTokenCard("SELL_TO_HUB", symbol, operator, opts, session))
            .join("");

        sections.push(`
            <div class="hub-agent-section">
                <h3 class="hub-agent-name">${agentLabel}</h3>
                <div class="hub-marketplace">
                    <div class="hub-marketplace-col" data-direction="buy">
                        <h4>Buy</h4>
                        ${buyCards || `<p class="explorer-empty">Nothing to buy here right now.</p>`}
                    </div>
                    <div class="hub-marketplace-col" data-direction="sell">
                        <h4>Sell</h4>
                        ${sellCards || `<p class="explorer-empty">Nothing to sell here right now.</p>`}
                    </div>
                </div>
            </div>
        `);
    }
    container.innerHTML = sections.join("") || `<p class="explorer-empty">This hub hasn't posted any active listings in this market yet.</p>`;
    applyHubMarketplaceMode(session);
}

// hubMarketplaceMode tracks which direction (buy/sell) is currently
// visible per session - each session has its own independent toggle
// (see setHubMarketplaceMode) since the two pages are viewed
// separately. Defaults to "buy" on first load.
const hubMarketplaceMode = { OFFAPP: "buy", ONCHAIN: "buy" };

// applyHubMarketplaceMode shows only the currently-selected direction's
// columns within a session's container - called after every re-render
// (loadHubRates runs on a timer) so the toggle choice survives a
// refresh instead of silently resetting back to showing both.
function applyHubMarketplaceMode(session) {
    const mode = hubMarketplaceMode[session];
    const containerId = session === "ONCHAIN" ? "hubOnChainAgentSections" : "hubAgentSections";
    const container = document.getElementById(containerId);
    if (!container) return;

    container.querySelectorAll(".hub-marketplace-col").forEach((col) => {
        col.style.display = col.dataset.direction === mode ? "block" : "none";
    });
}

// setHubMarketplaceMode is the Buy/Sell toggle button handler - purely
// a visibility switch over cards already in the DOM, no re-fetch
// needed, so it's instant.
function setHubMarketplaceMode(session, mode) {
    hubMarketplaceMode[session] = mode;
    applyHubMarketplaceMode(session);

    const buyBtn = document.getElementById(`hubModeBtn_${session}_buy`);
    const sellBtn = document.getElementById(`hubModeBtn_${session}_sell`);
    if (buyBtn) buyBtn.classList.toggle("active", mode === "buy");
    if (sellBtn) sellBtn.classList.toggle("active", mode === "sell");
}

async function loadHubRates() {
    if (!currentHubAddress) return;
    try {
        const rates = await apiGet(`/hub-rates?hub=${currentHubAddress}`);
        p2pHubRatesCache = rates || [];

        // Snapshot every card's typed CCT amount + selected currency
        // before replacing the DOM below - this runs on a timer (see
        // setInterval near the bottom of this file) regardless of what
        // the trader is doing, so without this a typed amount and its
        // live quote would vanish out from under them.
        const preserved = {};
        document.querySelectorAll('[id$="_cctAmount"]').forEach((el) => {
            if (el.value) {
                const cardId = el.id.replace(/_cctAmount$/, "");
                const currencySelect = document.getElementById(`${cardId}_currency`);
                preserved[cardId] = { amount: el.value, currency: currencySelect ? currencySelect.value : null };
            }
        });
        const focusedId = document.activeElement && document.activeElement.id;
        const selectionStart = document.activeElement && document.activeElement.selectionStart;

        // Every card's detail panel defaults to collapsed
        // (renderUnifiedTokenCard bakes in style="display:none") - without
        // this, a trader who expanded a card to fill in a trade would see
        // it silently collapse back on this timer's next tick, jerking the
        // whole list up as that card's height shrinks back to just its
        // summary row (and re-expanding on the next click only to collapse
        // again 15s later - the reported "jumping up and down").
        const expandedCardIds = Array.from(document.querySelectorAll('[id$="_detail"]'))
            .filter((el) => el.style.display !== "none")
            .map((el) => el.id.replace(/_detail$/, ""));
        const scrollY = window.scrollY;

        // Two entirely separate sessions/pages - off-app-currency rates
        // feed the main hub detail page, on-chain rates feed the
        // Native Token Swap page - both rendered every refresh
        // regardless of which one is currently visible, same as before
        // the unified-card redesign, so switching pages is instant.
        const offAppRates = p2pHubRatesCache.filter((r) => (r.Mode || "OFFAPP") === "OFFAPP");
        const onChainRates = p2pHubRatesCache.filter((r) => r.Mode === "ONCHAIN");
        renderAgentSectionsInto("hubAgentSections", offAppRates, "OFFAPP");
        renderAgentSectionsInto("hubOnChainAgentSections", onChainRates, "ONCHAIN");

        // Restore typed amounts + currency selection, keyed by cardId
        // (direction+symbol+operator+session survives the re-render
        // since it's derived purely from data, not DOM node identity),
        // then initialize the Rate line for every card that didn't have
        // a preserved amount too.
        document.querySelectorAll('[id$="_currency"]').forEach((select) => {
            const cardId = select.id.replace(/_currency$/, "");
            const match = cardId.match(/^hubCard_(BUY_FROM_HUB|SELL_TO_HUB)_(.+)_(hub|[0-9a-f]+)_(OFFAPP|ONCHAIN)$/);
            if (!match) return;
            const [, direction, symbol, operatorPart, session] = match;
            const operator = operatorPart === "hub" ? "" : operatorPart;

            const saved = preserved[cardId];
            if (saved) {
                if (saved.currency) select.value = saved.currency;
                document.getElementById(`${cardId}_cctAmount`).value = saved.amount;
            }
            updateCardRateDisplay(direction, symbol, operator, session, getCardSelectedRate(direction, symbol, operator, session));
            if (saved) onHubCardAmountChange(direction, symbol, operator, session, "cct");
        });

        // Re-expand whatever was open before this refresh rebuilt the DOM
        // (each card starts collapsed again by default - see the
        // expandedCardIds comment above).
        expandedCardIds.forEach((cardId) => {
            const detail = document.getElementById(`${cardId}_detail`);
            const chevron = document.getElementById(`${cardId}_chevron`);
            if (!detail) return;
            detail.style.display = "block";
            if (chevron) chevron.classList.add("open");
        });

        if (focusedId) {
            const el = document.getElementById(focusedId);
            if (el) {
                el.focus();
                if (typeof selectionStart === "number" && el.setSelectionRange) {
                    el.setSelectionRange(selectionStart, selectionStart);
                }
            }
        }
        window.scrollTo(0, scrollY);
    } catch (e) {
        console.error(e);
    }
}

// fillMaxHubCardAmount fills "Amount of <symbol>" with the largest
// amount actually tradeable right now - the hub's live inventory for
// BUY_FROM_HUB (already loaded in p2pHubRatesCache, no extra request),
// or the trader's own balance for SELL_TO_HUB (a fresh fetch, since
// this page never otherwise loads a trader's CCC1/CCT balance).
async function fillMaxHubCardAmount(direction, symbol, operator, session) {
    const id = hubCardId(direction, symbol, operator, session);
    const decimals = hubTradeSymbolDecimals(symbol);
    let maxWhole = 0;

    if (direction === "BUY_FROM_HUB") {
        const rate = getCardSelectedRate(direction, symbol, operator, session);
        if (!rate) return;
        maxWhole = hubBalanceWholeUnit(rate.HubSymbolBalance, symbol);
    } else {
        if (!p2pWallet) return;
        try {
            if (HUB_TRADE_CCC1_CHAIN[symbol]) {
                const { balance } = await fetchCcc1Balance(p2pWallet, HUB_TRADE_CCC1_CHAIN[symbol]);
                maxWhole = parseFloat(smallestToDecimal(balance, decimals));
            } else if (symbol === "CCT") {
                const result = await fetchTokenBalance(p2pWallet, "CCT");
                maxWhole = Math.floor(parseFloat(smallestToDecimal(result.balance, 18)));
            } else {
                const result = await fetchTokenBalance(p2pWallet, symbol);
                maxWhole = Math.floor(parseFloat(result.balance) || 0);
            }
        } catch (e) {
            console.error(e);
            return;
        }
    }

    if (!maxWhole || maxWhole <= 0) return;
    document.getElementById(`${id}_cctAmount`).value = maxWhole;
    onHubCardAmountChange(direction, symbol, operator, session, "cct");
}

async function requestHubTradeFromCard(direction, symbol, operator, session) {
    const rate = getCardSelectedRate(direction, symbol, operator, session);
    if (!rate) return;

    const id = hubCardId(direction, symbol, operator, session);
    const amount = parseFloat(document.getElementById(`${id}_cctAmount`).value);
    const label = p2pSymbolLabels[symbol] || symbol;
    const mode = rate.Mode || "OFFAPP";

    if (!amount || amount <= 0) {
        alert("Enter an amount first.");
        return;
    }

    // Gate on the hub's preset rules, if it has any - the trader must
    // acknowledge them before the actual trade request fires. See
    // core.HubRules's doc comment: this is a disclosure step, not a
    // security boundary.
    if (hubRulesCache && hubRulesCache.trim()) {
        pendingTradeRequest = { symbol, direction, mode, operator, fiatSymbol: rate.FiatSymbol, amount, label };
        document.getElementById("rulesModalText").innerText = hubRulesCache;
        document.getElementById("rulesModalOverlay").style.display = "flex";
        return;
    }

    await executeHubTrade(symbol, direction, mode, operator, rate.FiatSymbol, amount, label);
}

function closeRulesModal() {
    document.getElementById("rulesModalOverlay").style.display = "none";
    pendingTradeRequest = null;
}

async function acknowledgeRulesAndTrade() {
    document.getElementById("rulesModalOverlay").style.display = "none";
    if (!pendingTradeRequest) return;
    const { symbol, direction, mode, operator, fiatSymbol, amount, label } = pendingTradeRequest;
    pendingTradeRequest = null;
    await executeHubTrade(symbol, direction, mode, operator, fiatSymbol, amount, label);
}

function showP2PTradeStatusToast(text) {
    const el = document.getElementById("p2pTradeStatusToast");
    if (!text) {
        el.style.display = "none";
        return;
    }
    el.innerText = text;
    el.style.display = "block";
}

async function executeHubTrade(symbol, direction, mode, operator, fiatSymbol, amount, label) {
    const isOnChain = mode === "ONCHAIN";
    const msg = isOnChain
        ? (direction === "BUY_FROM_HUB"
            ? `Swap for ${amount} ${label} from this hub? This settles instantly - both sides move on chain right away, nothing left to confirm.`
            : `Swap ${amount} ${label} to this hub? This settles instantly - both sides move on chain right away.`)
        : (direction === "BUY_FROM_HUB"
            ? `Buy ${amount} ${label} from this hub? This escrows their tokens now; you'll pay off-app once the chat opens.`
            : `Sell ${amount} ${label} to this hub? Your tokens go into escrow now, released once payment is confirmed.`);
    if (!confirm(msg)) return;

    showP2PTradeStatusToast("Submitting trade...");

    try {
        // amount here is the whole-unit decimal the trader sees/typed
        // (e.g. 0.0000567 for ETH_CCC1) - the backend expects that
        // token's own smallest unit (wei/6-decimal/sats), which can
        // exceed JS's 2^53 safe-integer range even for modest trade
        // sizes. Converted via vault.js's decimalToSmallest and built as
        // a raw JSON literal (not JSON.stringify on a Number) so the
        // exact digit string survives to the Go uint64 field untouched -
        // same convention apiPostSignedRaw's doc comment (api.js)
        // establishes for every other wei-scale amount in this app.
        const decimals = hubTradeSymbolDecimals(symbol);
        const submitAmount = decimals > 0 ? decimalToSmallest(amount, decimals) : String(Math.floor(amount));
        const payloadJSON = `{"hub":${JSON.stringify(currentHubAddress)},"symbol":${JSON.stringify(symbol)},"amount":${submitAmount},"direction":${JSON.stringify(direction)},"operator":${JSON.stringify(operator)},"fiatSymbol":${JSON.stringify(fiatSymbol)}}`;
        const result = await apiPostSignedRaw("/hub-rates/trade", payloadJSON);

        showP2PTradeStatusToast("Waiting for block confirmation...");

        // The trade only actually exists (escrow moved, chat/offer
        // created) once the transaction above confirms in a block -
        // poll for it instead of a blind fixed wait, so the user sees
        // real progress instead of an apparently-frozen button.
        const deadline = Date.now() + 15000;
        let confirmed = false;
        while (Date.now() < deadline) {
            await new Promise((resolve) => setTimeout(resolve, 1500));
            await loadOffers();
            if (isOnChain || p2pOffersCache.some((o) => o.ID === result.offerId)) {
                confirmed = true;
                break;
            }
        }

        await loadHubRates();
        showP2PTradeStatusToast(null);

        if (!confirmed) {
            alert("Still processing - check Active Trades shortly if the chat doesn't open automatically.");
            return;
        }

        if (isOnChain) {
            alert("Swap complete - balances updated.");
        } else {
            openP2PChatModal(result.offerId);
        }
    } catch (e) {
        showP2PTradeStatusToast(null);
        alert(e.message);
        // "requires ID verification" is the exact wording
        // HandleRequestHubTrade's RequireKYC gate returns (api/hub_rate.go)
        // - scroll the trader straight to the form that unblocks them
        // instead of leaving them to hunt for it after the alert closes.
        if (e.message && e.message.indexOf("requires ID verification") !== -1) {
            const kycBox = document.getElementById("hubKYCBox");
            if (kycBox) kycBox.scrollIntoView({ behavior: "smooth", block: "center" });
        }
    }
}

// --- Hub preset trading rules --------------------------------------------

async function loadHubRulesForDisplay(hub) {
    try {
        const rules = await apiGet(`/hub-rates/rules?hub=${hub}`);
        hubRulesCache = (rules && rules.Text) || (rules && rules.text) || "";
        hubRulesWindowsCache = (rules && rules.PaymentWindowMinutes) || (rules && rules.paymentWindowMinutes) || {};

        const buyInput = document.getElementById("hubRulesBuyWindow");
        const sellInput = document.getElementById("hubRulesSellWindow");
        if (buyInput) buyInput.value = hubRulesWindowsCache.BUY_FROM_HUB || "";
        if (sellInput) sellInput.value = hubRulesWindowsCache.SELL_TO_HUB || "";

        const minRepInput = document.getElementById("hubRulesMinRepToApply");
        if (minRepInput) minRepInput.value = (rules && (rules.MinReputationToApply || rules.minReputationToApply)) || "";

        const requireKYCInput = document.getElementById("hubRulesRequireKYC");
        if (requireKYCInput) requireKYCInput.checked = !!(rules && (rules.RequireKYC || rules.requireKYC));

        const cannedReplies = (rules && (rules.CannedReplies || rules.cannedReplies)) || [];
        const cannedTextarea = document.getElementById("hubCannedReplies");
        if (cannedTextarea) cannedTextarea.value = cannedReplies.map((c) => c.Text || c.text || "").join("\n");

        const serviceProviders = (rules && (rules.ServiceProviders || rules.serviceProviders)) || [];
        const serviceProvidersTextarea = document.getElementById("hubServiceProviders");
        if (serviceProvidersTextarea) serviceProvidersTextarea.value = serviceProviders.join("\n");
    } catch (e) {
        hubRulesCache = "";
        hubRulesWindowsCache = {};
    }
}

// getPaymentWindowMinutes is what the P2P chat countdown timer reads
// from - falls back to DEFAULT_PAYMENT_WINDOW_MINUTES if the hub
// never set an explicit window for this direction.
function getPaymentWindowMinutes(direction) {
    const minutes = hubRulesWindowsCache && hubRulesWindowsCache[direction];
    return minutes > 0 ? minutes : DEFAULT_PAYMENT_WINDOW_MINUTES;
}

async function setHubRules() {
    const text = document.getElementById("hubRulesText").value;
    const statusEl = document.getElementById("hubRulesSetStatus");

    const paymentWindowMinutes = {};
    const buyMinutes = parseInt(document.getElementById("hubRulesBuyWindow").value, 10);
    const sellMinutes = parseInt(document.getElementById("hubRulesSellWindow").value, 10);
    if (buyMinutes > 0) paymentWindowMinutes.BUY_FROM_HUB = buyMinutes;
    if (sellMinutes > 0) paymentWindowMinutes.SELL_TO_HUB = sellMinutes;

    const minReputationToApply = parseInt(document.getElementById("hubRulesMinRepToApply").value, 10) || 0;
    const requireKYC = document.getElementById("hubRulesRequireKYC").checked;

    const cannedReplies = document
        .getElementById("hubCannedReplies")
        .value.split("\n")
        .map((line) => line.trim())
        .filter((line) => line.length > 0)
        .map((line) => ({ trigger: "", text: line }));

    const serviceProviders = document
        .getElementById("hubServiceProviders")
        .value.split("\n")
        .map((line) => line.trim())
        .filter((line) => line.length > 0);

    try {
        const result = await apiPostSigned("/hub-rates/rules/set", { text, paymentWindowMinutes, minReputationToApply, cannedReplies, requireKYC, serviceProviders });
        statusEl.innerText = `Rules pending confirmation (tx ${result.txId}).`;
        setTimeout(() => loadHubRulesForDisplay(currentHubAddress), 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

// --- Hub agent applications -----------------------------------------------
//
// The self-serve half core.HubWorker never had: any outernet wallet
// can apply here; the hub owner still has to explicitly approve (see
// HandleApproveHubAgent) before the applicant actually becomes a
// worker able to post rates or get zoned incoming trade requests.

async function applyHubAgent() {
    if (!p2pWallet) return;

    const statusEl = document.getElementById("hubAgentApplyStatus");
    try {
        await apiPostSigned("/hub-agents/apply", { hub: currentHubAddress, message: "" });
        statusEl.innerText = "Application submitted - the hub will review it.";
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function loadHubAgentApplications() {
    if (!currentHubAddress) return;
    try {
        const apps = await apiGet(`/hub-agents?hub=${currentHubAddress}`);
        const pending = (apps || []).filter((a) => a.Status === "PENDING");
        const list = document.getElementById("hubAgentApplicationList");

        list.innerHTML = pending.length
            ? pending
                  .map(
                      (a) => `
                <div class="explorer-row">
                    <span>${a.ApplicantAddress.slice(0, 8)}...${a.ApplicantAddress.slice(-6)}</span>
                    <button onclick="decideHubAgent('${a.ID}', true)">Approve</button>
                    <button onclick="decideHubAgent('${a.ID}', false)">Reject</button>
                </div>`
                  )
                  .join("")
            : `<p class="explorer-empty">No pending applications.</p>`;
    } catch (e) {
        console.error(e);
    }
}

async function decideHubAgent(applicationId, approve) {
    try {
        if (approve) {
            await apiPostSigned("/hub-agents/approve", { applicationId, name: "" });
        } else {
            await apiPostSigned("/hub-agents/reject", { applicationId });
        }
        await loadHubAgentApplications();
        setTimeout(loadHubWorkers, 9000);
    } catch (e) {
        alert(e.message);
    }
}

// --- Hub workers / customer care -----------------------------------------

async function loadHubWorkers() {
    if (!currentHubAddress) return;
    try {
        const workers = await apiGet(`/hub-workers?hub=${currentHubAddress}`);
        const list = document.getElementById("hubWorkerList");
        list.innerHTML = (workers || []).length
            ? workers
                  .map(
                      (w) => `
                <div class="explorer-row">
                    <span style="opacity:${w.Active ? "1" : "0.5"};">
                        ${escapeHtml(w.Name || "(unnamed)")} - ${w.WorkerAddress.slice(0, 8)}...${w.WorkerAddress.slice(-6)}
                        ${w.Active ? "" : " (removed)"}
                        ${w.IsAutomationAgent ? " &#9889; automation on" : ""}
                    </span>
                    ${w.Active ? `<button onclick="removeHubWorker('${w.WorkerAddress}')">Remove</button>` : ""}
                </div>`
                  )
                  .join("")
            : `<p class="explorer-empty">No workers added yet.</p>`;

        const activeWorkers = (workers || []).filter((w) => w.Active);
        const select = document.getElementById("hubAutomationWorker");
        if (select) {
            select.innerHTML = activeWorkers.length
                ? activeWorkers
                      .map((w) => `<option value="${w.WorkerAddress}">${escapeHtml(w.Name || w.WorkerAddress.slice(0, 8))}</option>`)
                      .join("")
                : `<option value="">No active operators yet</option>`;
        }
    } catch (e) {
        console.error(e);
    }
}

// --- Hub trader ID verification (phone + government ID) --------------------
// See api/hub_kyc.go: submitted per-hub, encrypted at rest, readable
// only by that exact hub - never written to the public chain.

const HUB_KYC_ID_TYPE_LABELS = {
    national_id: "National ID",
    passport: "Passport",
    drivers_license: "Driver's License",
    voters_card: "Voter's Card",
};

// refreshHubKYCStatus is shown to a trader (never the hub owner viewing
// their own hub) - hides the submission form once they've already sent
// their info to this hub, rather than re-showing an empty form every
// visit.
async function refreshHubKYCStatus() {
    if (!currentHubAddress || !p2pWallet) return;
    const statusEl = document.getElementById("hubKYCStatus");
    try {
        const result = await apiPostSigned("/hub-kyc/status", { hub: currentHubAddress });
        const formRow1 = document.getElementById("hubKYCPhone").closest(".explorer-search");
        const formRow2 = document.getElementById("hubKYCIdNumber").closest(".explorer-search");
        if (result.submitted) {
            formRow1.style.display = "none";
            formRow2.style.display = "none";
            statusEl.innerText = "Submitted - this hub has your phone number and ID on file.";
        } else {
            formRow1.style.display = "";
            formRow2.style.display = "";
            statusEl.innerText = "";
        }
    } catch (e) {
        console.error(e);
    }
}

async function submitHubKYC() {
    const statusEl = document.getElementById("hubKYCStatus");
    const phone = document.getElementById("hubKYCPhone").value.trim();
    const idType = document.getElementById("hubKYCIdType").value;
    const idNumber = document.getElementById("hubKYCIdNumber").value.trim();

    if (!phone || !idNumber) {
        statusEl.innerText = "Phone number and ID number are both required.";
        return;
    }

    statusEl.innerText = "Submitting...";
    try {
        await apiPostSigned("/hub-kyc/submit", { hub: currentHubAddress, phone, idType, idNumber });
        document.getElementById("hubKYCPhone").value = "";
        document.getElementById("hubKYCIdNumber").value = "";
        await refreshHubKYCStatus();
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

// loadHubKYCRecords is owner-only (see HandleListHubKYC's own address
// check) - called only from the currentHubIsMine branch of viewHub, so
// a worker/operator viewing the same hub never even attempts this call.
async function loadHubKYCRecords() {
    const listEl = document.getElementById("hubKYCRecordsList");
    if (!listEl || !currentHubAddress) return;
    try {
        const records = await apiPostSigned("/hub-kyc/list", { hub: currentHubAddress });
        listEl.innerHTML = (records || []).length
            ? records
                  .map(
                      (rec) => `
                <div class="explorer-row">
                    <span>
                        <span class="explorer-hash">${rec.traderAddress.slice(0, 8)}...${rec.traderAddress.slice(-6)}</span>
                        - ${rec.phone} - ${HUB_KYC_ID_TYPE_LABELS[rec.idType] || rec.idType}: ${rec.idNumber}
                        <span style="opacity:0.6;font-size:11px;"> (${new Date(rec.submittedAt * 1000).toLocaleDateString()})</span>
                    </span>
                </div>`
                  )
                  .join("")
            : `<p class="explorer-empty">No traders have submitted ID verification yet.</p>`;
    } catch (e) {
        listEl.innerHTML = `<p class="explorer-empty">Failed to load: ${e.message}</p>`;
        console.error(e);
    }
}

// --- Hub automation --------------------------------------------------------

async function loadHubAutomationStatus() {
    if (!currentHubAddress) return;
    try {
        const status = await apiGet(`/hub-automation/status?hub=${currentHubAddress}`);
        const el = document.getElementById("hubAutomationStatus");
        el.innerText = status.active
            ? `Active until ${new Date(status.activeUntil * 1000).toLocaleString()}.`
            : "Not subscribed - operators can't auto-reply or auto-confirm until you subscribe.";
    } catch (e) {
        console.error(e);
    }
}

async function subscribeHubAutomation() {
    const statusEl = document.getElementById("hubAutomationSubStatus");
    try {
        const result = await apiPostSigned("/hub-automation/subscribe", {});
        statusEl.innerText = `Subscription pending confirmation (tx ${result.txId}).`;
        setTimeout(loadHubAutomationStatus, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function saveWorkerAutomation() {
    const workerAddress = document.getElementById("hubAutomationWorker").value;
    const isAutomationAgent = document.getElementById("hubAutomationEnabled").checked;
    const autoConfirmCapAmount = parseInt(document.getElementById("hubAutomationCap").value, 10) || 0;
    const statusEl = document.getElementById("hubAutomationWorkerStatus");

    if (!workerAddress) {
        statusEl.innerText = "Add an operator first.";
        return;
    }

    try {
        const result = await apiPostSigned("/hub-workers/set-automation", {
            workerAddress,
            isAutomationAgent,
            autoConfirmCapAmount,
        });
        statusEl.innerText = `Saved (tx ${result.txId}).`;
        setTimeout(loadHubWorkers, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function addHubWorker() {
    const workerAddress = normalizeAddress(document.getElementById("hubWorkerAddress").value.trim());
    const workerPublicKey = document.getElementById("hubWorkerPublicKey").value.trim();
    const name = document.getElementById("hubWorkerName").value.trim();
    const statusEl = document.getElementById("hubWorkerSetStatus");

    if (!workerAddress || !workerPublicKey) {
        statusEl.innerText = "Enter the worker's address and public key.";
        return;
    }

    try {
        const result = await apiPostSigned("/hub-workers/add", { workerAddress, workerPublicKey, name });
        statusEl.innerText = `Worker pending confirmation (tx ${result.txId}).`;
        document.getElementById("hubWorkerAddress").value = "";
        document.getElementById("hubWorkerPublicKey").value = "";
        document.getElementById("hubWorkerName").value = "";
        setTimeout(loadHubWorkers, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function removeHubWorker(workerAddress) {
    if (!confirm("Remove this worker? They will no longer be able to post rates on your behalf.")) return;
    try {
        await apiPostSigned("/hub-workers/remove", { workerAddress });
        setTimeout(loadHubWorkers, 9000);
    } catch (e) {
        alert(e.message);
    }
}

// --- Hub storefront design -------------------------------------------------

let hubStorefrontCache = "";

function renderStorefrontFrame(html) {
    const wrap = document.getElementById("hubStorefrontDisplay");
    const frame = document.getElementById("hubStorefrontFrame");
    if (!html || !html.trim()) {
        wrap.style.display = "none";
        return;
    }
    wrap.style.display = "block";
    // srcdoc + sandbox="allow-scripts" ONLY (no allow-same-origin) is
    // what makes this safe - the hub's design runs in a unique opaque
    // origin with no access to this site's cookies, localStorage (so
    // no way to ever read a visitor's wallet keys), or DOM. See
    // core.HubStorefront's doc comment.
    frame.srcdoc = html;
}

async function loadHubStorefrontForDisplay(hub) {
    try {
        const sf = await apiGet(`/hub-storefront?hub=${hub}`);
        hubStorefrontCache = (sf && sf.HTML) || (sf && sf.html) || "";
    } catch (e) {
        hubStorefrontCache = "";
    }
    renderStorefrontFrame(hubStorefrontCache);
}

function previewHubStorefront() {
    renderStorefrontFrame(document.getElementById("hubStorefrontHtml").value);
}

async function saveHubStorefront() {
    const html = document.getElementById("hubStorefrontHtml").value;
    const statusEl = document.getElementById("hubStorefrontSetStatus");
    try {
        const result = await apiPostSigned("/hub-storefront/set", { html });
        statusEl.innerText = `Design pending confirmation (tx ${result.txId}).`;
        setTimeout(() => loadHubStorefrontForDisplay(currentHubAddress), 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

// --- Hub notifications ---------------------------------------------------

async function loadHubNotifications() {
    if (!currentHubAddress) return;
    try {
        const result = await apiGet(`/hub/notifications?hub=${currentHubAddress}`);
        const notifications = (result && result.notifications) || [];
        const unread = (result && result.unread) || 0;

        const badge = document.getElementById("hubNotifUnreadBadge");
        if (unread > 0) {
            badge.style.display = "inline-block";
            badge.innerText = unread;
        } else {
            badge.style.display = "none";
        }

        const list = document.getElementById("hubNotificationList");
        list.innerHTML = notifications.length
            ? notifications
                  .slice(0, 20)
                  .map(
                      (n) => `
                <div class="explorer-row">
                    <span style="opacity:${n.Read ? "0.5" : "1"};">${escapeHtml(n.Message)}</span>
                    ${n.Read ? "" : `<button onclick="markNotificationRead('${n.ID}')">Mark read</button>`}
                </div>`
                  )
                  .join("")
            : `<p class="explorer-empty">No notifications yet.</p>`;
    } catch (e) {
        console.error(e);
    }
}

async function markNotificationRead(id) {
    try {
        await apiPostSigned("/hub/notifications/read", { notificationId: id });
        await loadHubNotifications();
    } catch (e) {
        alert(e.message);
    }
}

// --- "My Active Trades" list ----------------------------------------------
//
// A wallet can have more than one EXTERNAL trade open at once (buying
// from one hub while selling to another, say) - this lists ALL of
// them, not just whichever chat was most recently opened, so leaving
// the site to make an off-app payment and coming back later never
// strands the user with no way back into a trade they forgot about.

// myActiveTrades filters the already-fetched offers cache down to
// this wallet's own EXTERNAL trades still in an active (non-terminal)
// settlement state - both sides (Creator and AcceptedBy) count, since
// either party might need to come back and act.
function myActiveTrades() {
    if (!p2pWallet) return [];
    return p2pOffersCache.filter(
        (o) =>
            o.PaymentMode === "EXTERNAL" &&
            (o.Creator === p2pWallet.address || o.AcceptedBy === p2pWallet.address) &&
            o.SettlementStatus &&
            !TERMINAL_SETTLEMENT_STATUSES.includes(o.SettlementStatus)
    );
}

function activeTradeRowHTML(o) {
    const isSeller = o.Creator === p2pWallet.address;
    const counterparty = isSeller ? o.AcceptedBy : o.Creator;
    const role = isSeller ? "Selling" : "Buying";
    const counterpartyLabel = counterparty ? `${counterparty.slice(0, 8)}...` : "-";
    return `
        <div class="active-trade-row" onclick="openP2PChatModal('${o.ID}')">
            <span class="active-trade-row-summary">${role} ${o.OfferAmount} ${o.OfferSymbol} - ${counterpartyLabel}</span>
            <span class="active-trade-row-status">${p2pShortSettlementStatus(o.SettlementStatus)}</span>
        </div>
    `;
}

// refreshActiveTradesWidget re-fetches every offer and re-renders the
// list from scratch - called on page load, after closing a chat, and
// on the same 15s poll everything else in this file already uses, so
// the count/list stay current even if a counterparty acts while this
// wallet has the widget closed.
async function refreshActiveTradesWidget() {
    if (!p2pWallet) return;

    try {
        const offers = await apiGet("/p2p/offers");
        p2pOffersCache = offers || [];
    } catch (e) {
        console.error(e);
        return;
    }

    const trades = myActiveTrades();
    document.getElementById("activeTradesWidget").style.display = trades.length > 0 ? "block" : "none";
    document.getElementById("activeTradesCount").innerText = trades.length;
    document.getElementById("activeTradesList").innerHTML = trades.length === 0
        ? `<p class="explorer-empty" style="padding:8px 10px;">No active trades.</p>`
        : trades.map(activeTradeRowHTML).join("");
}

function toggleActiveTradesList() {
    const list = document.getElementById("activeTradesList");
    list.style.display = list.style.display === "block" ? "none" : "block";
}

// --- Full-screen trade settlement chat -----------------------------------

async function loadOffers() {
    try {
        const offers = await apiGet("/p2p/offers");
        p2pOffersCache = offers || [];
        if (currentP2PChatOfferId) {
            renderP2PChatModal(currentP2PChatOfferId);
        }
    } catch (e) {
        console.error(e);
    }
}

async function openP2PChatModal(offerId) {
    currentP2PChatOfferId = offerId;
    document.getElementById("activeTradesList").style.display = "none";
    await loadOffers();
    renderP2PChatModal(offerId);
    slideToChatPanel();
    document.getElementById("p2pChatOverlay").style.display = "flex";
}

function closeP2PChatModal() {
    document.getElementById("p2pChatOverlay").style.display = "none";
    currentP2PChatOfferId = null;
    stopP2PCountdown();
    if (currentHubAddress) loadHubRates();

    // Refresh the active-trades list - it'll keep including this trade
    // if it's still unsettled (the user may be about to go pay off-app
    // and will need a way back in), same as every other open trade.
    refreshActiveTradesWidget();
}

// --- Two sliding chat panels: conversation, then timer & payment ---------

function slideToTimerPanel() {
    document.getElementById("p2pChatPanels").classList.add("panel-2");
}

function slideToChatPanel() {
    document.getElementById("p2pChatPanels").classList.remove("panel-2");
}

(function setupP2PChatSwipe() {
    const panels = document.getElementById("p2pChatPanels");
    if (!panels) return;
    let touchStartX = null;
    panels.addEventListener("touchstart", (e) => {
        touchStartX = e.touches[0].clientX;
    });
    panels.addEventListener("touchend", (e) => {
        if (touchStartX === null) return;
        const deltaX = e.changedTouches[0].clientX - touchStartX;
        if (deltaX < -40) slideToTimerPanel();
        else if (deltaX > 40) slideToChatPanel();
        touchStartX = null;
    });
})();

// --- Payment countdown timer ----------------------------------------------

let p2pChatCountdownTimer = null;

function stopP2PCountdown() {
    if (p2pChatCountdownTimer) {
        clearInterval(p2pChatCountdownTimer);
        p2pChatCountdownTimer = null;
    }
}

// offerHubAndDirection figures out which side of a trade (if either)
// is the verified Trading Hub, so the countdown can look up THAT
// hub's own payment window for the matching direction - necessary
// since a reopened chat (e.g. via the persistent top-right banner)
// may not have the hub's own detail page (and its rules cache)
// loaded at all.
function offerHubAndDirection(o) {
    if (p2pCompaniesCache.some((c) => c.Address === o.Creator)) {
        return { hub: o.Creator, direction: "BUY_FROM_HUB" };
    }
    if (p2pCompaniesCache.some((c) => c.Address === o.AcceptedBy)) {
        return { hub: o.AcceptedBy, direction: "SELL_TO_HUB" };
    }
    return { hub: null, direction: null };
}

async function loadPaymentWindowMinutesForOffer(o) {
    const { hub, direction } = offerHubAndDirection(o);
    if (!hub || !direction) return DEFAULT_PAYMENT_WINDOW_MINUTES;
    try {
        const rules = await apiGet(`/hub-rates/rules?hub=${hub}`);
        const windows = (rules && rules.PaymentWindowMinutes) || (rules && rules.paymentWindowMinutes) || {};
        const minutes = windows[direction];
        return minutes > 0 ? minutes : DEFAULT_PAYMENT_WINDOW_MINUTES;
    } catch (e) {
        return DEFAULT_PAYMENT_WINDOW_MINUTES;
    }
}

// loadPaymentInstructionsForOffer surfaces the hub's own trade-terms
// text (the same text shown once, before the trade, in the rules
// acknowledgment modal) inside the chat itself while payment is
// actually due - previously this was only ever shown pre-trade, with
// nothing to reference once the user actually needed to know how/
// where to pay. Hidden once the trade is past AWAITING_PAYMENT, since
// there's nothing left to pay by then.
async function loadPaymentInstructionsForOffer(o) {
    const box = document.getElementById("p2pChatPaymentInstructions");
    if (!box) return;

    if (o.SettlementStatus !== "AWAITING_PAYMENT") {
        box.innerHTML = "";
        return;
    }

    const { hub } = offerHubAndDirection(o);
    let text = "";
    if (hub) {
        try {
            const rules = await apiGet(`/hub-rates/rules?hub=${hub}`);
            text = (rules && rules.Text) || (rules && rules.text) || "";
        } catch (e) {
            text = "";
        }
    }

    box.innerHTML = text
        ? `<div class="p2p-payment-instructions"><div class="p2p-payment-instructions-label">Payment Instructions From The Hub</div>${escapeHtmlMultiline(text)}</div>`
        : `<div class="p2p-payment-instructions"><div class="p2p-payment-instructions-label">Payment Instructions</div>This hub hasn't posted specific payment instructions - ask in chat where/how to send payment.</div>`;
}

// acknowledgeChatRules is the "Continue to Chat" button's handler -
// remembers this offer as read for the rest of the browser session
// and re-renders, which reveals the actual thread/input.
function acknowledgeChatRules() {
    if (currentP2PChatOfferId) p2pChatRulesAcknowledged.add(currentP2PChatOfferId);
    renderP2PChatModal(currentP2PChatOfferId);
}

// loadChatRulesIntroText fills the rules-first gate with the fixed
// platform rules plus this specific hub's own free-text terms (same
// source loadPaymentInstructionsForOffer already reads) - shown once,
// before a first-time chat participant can see or send any message.
async function loadChatRulesIntroText(o) {
    const el = document.getElementById("p2pChatRulesIntroText");
    if (!el) return;

    const { hub } = offerHubAndDirection(o);
    let hubText = "";
    if (hub) {
        try {
            const rules = await apiGet(`/hub-rates/rules?hub=${hub}`);
            hubText = (rules && rules.Text) || (rules && rules.text) || "";
        } catch (e) {
            hubText = "";
        }
    }

    el.innerHTML =
        `<div class="p2p-chat-rules-section"><h4>Platform Rules</h4>${PLATFORM_P2P_RULES.map((r) => `&bull; ${r}`).join("<br>")}</div>` +
        `<div class="p2p-chat-rules-section"><h4>This Hub's Trade Terms</h4>${hubText ? escapeHtmlMultiline(hubText) : "This hub hasn't posted specific trade terms yet - ask in chat if you're unsure."}</div>`;
}

async function startP2PCountdownFor(o) {
    stopP2PCountdown();
    const countdownEl = document.getElementById("p2pChatCountdown");
    const noteEl = document.getElementById("p2pChatWindowNote");

    if (o.SettlementStatus !== "AWAITING_PAYMENT" || !o.AwaitingPaymentSince) {
        // No countdown to show once payment's been marked/the trade has
        // moved past the payment window - replace the timer itself with
        // a clear progress readout instead of leaving it at a bare "-",
        // so it's obvious at a glance (without reading the chat) whether
        // this trade is still moving or is fully done.
        const concluded = TERMINAL_SETTLEMENT_STATUSES.includes(o.SettlementStatus);
        countdownEl.innerText = o.SettlementStatus ? (concluded ? "✓ Concluded" : "⏳ In Progress") : "-";
        countdownEl.classList.remove("overdue");
        countdownEl.classList.toggle("status-text", !!o.SettlementStatus);
        countdownEl.classList.toggle("concluded", concluded);
        noteEl.innerText = o.SettlementStatus ? p2pFriendlySettlementStatus(o.SettlementStatus) : "";
        return;
    }

    countdownEl.classList.remove("status-text", "concluded");
    const minutes = await loadPaymentWindowMinutesForOffer(o);
    const deadlineMs = (o.AwaitingPaymentSince + minutes * 60) * 1000;
    noteEl.innerText = `This hub allows ${minutes} minute${minutes === 1 ? "" : "s"} to complete off-app payment.`;

    const tick = () => {
        const remainingMs = deadlineMs - Date.now();
        if (remainingMs <= 0) {
            countdownEl.innerText = "Time's up - overdue";
            countdownEl.classList.add("overdue");
            return;
        }
        const totalSeconds = Math.floor(remainingMs / 1000);
        const mm = Math.floor(totalSeconds / 60);
        const ss = totalSeconds % 60;
        countdownEl.innerText = `${mm}:${ss.toString().padStart(2, "0")}`;
        countdownEl.classList.remove("overdue");
    };
    tick();
    p2pChatCountdownTimer = setInterval(tick, 1000);
}

function renderP2PChatModal(offerId) {
    const o = p2pOffersCache.find((x) => x.ID === offerId);
    if (!o) return;

    const isSeller = p2pWallet && o.Creator === p2pWallet.address;
    const isBuyer = p2pWallet && o.AcceptedBy === p2pWallet.address;
    const isModerator = p2pWallet && o.ModeratorAddress === p2pWallet.address;

    // Moderator status first, then a plain-language reminder of the
    // trade's actual terms - the raw seller/buyer address + settlement-
    // status dump this used to show is redundant with the countdown
    // panel's own friendly status readout (see startP2PCountdownFor's
    // noteEl) and reads as backend debug output, not something either
    // trading party actually needs while chatting.
    document.getElementById("p2pChatModerator").innerText = o.ModeratorAddress
        ? `\u{1F6E1} Moderator active for this trade: ${o.ModeratorAddress.slice(0, 8)}...${o.ModeratorAddress.slice(-6)}`
        : "No moderator assigned yet.";

    document.getElementById("p2pChatSummary").innerText =
        `This trade is for ${o.OfferAmount} ${o.OfferSymbol} for ${Number(o.WantAmount).toLocaleString()} ${o.WantSymbol}, to be paid off-app.`;

    // Buyer-marked-paid is otherwise only visible after swiping to the
    // Timer & Payment panel (see p2pChatPaymentActions below) - shown
    // here too, front and center on the panel a seller actually lands
    // on first, so it's never missed while just reading the
    // conversation. Clicking it jumps straight to the panel with the
    // real verify/dispute buttons.
    const markedBanner = document.getElementById("p2pChatPaymentMarkedBanner");
    if (markedBanner) {
        if (isSeller && o.SettlementStatus === "AWAITING_PAYMENT" && o.BuyerMarkedPaidAt) {
            markedBanner.style.display = "block";
            markedBanner.className = "p2p-payment-marked-banner";
            markedBanner.onclick = slideToTimerPanel;
            markedBanner.innerText = "Buyer has made payment - swipe to verify →";
        } else if (isBuyer && o.SettlementStatus === "AWAITING_PAYMENT" && o.BuyerMarkedPaidAt) {
            markedBanner.style.display = "block";
            markedBanner.className = "p2p-payment-marked-banner";
            markedBanner.onclick = slideToTimerPanel;
            markedBanner.innerText = "You marked payment sent - waiting on the seller to verify →";
        } else {
            markedBanner.style.display = "none";
            markedBanner.onclick = null;
        }
    }

    renderTradeReviewPrompt(o, isSeller, isBuyer);
    renderDisputeFaultChoicePrompt(o);

    // Every payment-related action (and its anti-fraud warning) lives on
    // the second, timer panel - see slideToTimerPanel/slideToChatPanel.
    const paymentActions = document.getElementById("p2pChatPaymentActions");
    let payHtml = "";

    // DISPUTE_ELIGIBLE_AFTER_SECONDS mirrors core.DisputeEligibleAfterSeconds
    // (core/p2p_offer.go) exactly - purely for deciding when to SHOW the
    // buyer's own dispute button; the real gate is enforced server-side
    // in HandleP2PBuyerDispute/DisputeAwaitingPaymentByBuyer, same
    // "client estimate, server authoritative" split as every other
    // countdown in this app.
    const DISPUTE_ELIGIBLE_AFTER_SECONDS = 20 * 60;

    if (isBuyer && o.SettlementStatus === "AWAITING_PAYMENT") {
        if (o.BuyerMarkedPaidAt) {
            const secondsSinceMarked = Math.floor(Date.now() / 1000) - o.BuyerMarkedPaidAt;
            payHtml += `<p style="opacity:0.75;font-size:12px;">You marked payment sent at ${new Date(o.BuyerMarkedPaidAt * 1000).toLocaleTimeString()}. Waiting on the seller to confirm receipt.</p>`;
            if (secondsSinceMarked >= DISPUTE_ELIGIBLE_AFTER_SECONDS) {
                payHtml += `<div class="p2p-fraud-warning">The seller hasn't confirmed or disputed your payment yet. If you're confident you really sent it, you can summon a moderator now instead of waiting indefinitely.</div>`;
                payHtml += `<button onclick="disputeAwaitingPaymentAsBuyer('${o.ID}')">Seller Isn't Responding - Dispute</button>`;
            } else {
                const minutesLeft = Math.ceil((DISPUTE_ELIGIBLE_AFTER_SECONDS - secondsSinceMarked) / 60);
                payHtml += `<p style="opacity:0.6;font-size:11px;">You can escalate to a moderator in ${minutesLeft}m if the seller still hasn't responded.</p>`;
            }
        } else {
            payHtml += `<div class="p2p-fraud-warning">Only click this once you have ACTUALLY sent the payment. Falsely marking payment as sent is fraud, will be flagged to the moderator, and can get you banned from this hub.</div>`;
            payHtml += `<button onclick="markBuyerPaid('${o.ID}')">I've Sent Payment</button>`;
        }
    }

    if (isSeller && o.SettlementStatus === "AWAITING_PAYMENT") {
        payHtml += `<div class="p2p-fraud-warning">Only confirm this once you have genuinely verified the money has arrived in your own account. Scammers may show fake screenshots or bank alerts to pressure you into confirming before you've actually been paid - check your real balance, not just a message. Confirming closes the trade and releases escrow immediately - this can't be undone.</div>`;
        payHtml += `<button onclick="confirmSellerPaid('${o.ID}')">I've Received Payment</button>`;
        if (o.BuyerMarkedPaidAt) {
            payHtml += ` <button onclick="disputeAwaitingPayment('${o.ID}')">I did NOT Receive This - Dispute</button>`;
        }
    }

    if (isBuyer && o.SettlementStatus === "SELLER_CONFIRMED") {
        payHtml += `<button onclick="confirmBuyer('${o.ID}', true)">Confirm - Release Escrow</button> `;
        payHtml += `<button onclick="confirmBuyer('${o.ID}', false)">Dispute</button>`;
    }
    if (isModerator && o.SettlementStatus === "DISPUTED") {
        payHtml += `<button onclick="resolveDispute('${o.ID}', true)">Resolve: Release to Buyer</button> `;
        payHtml += `<button onclick="resolveDispute('${o.ID}', false)">Resolve: Refund Seller</button>`;
    }
    if (!payHtml) {
        payHtml = `<p class="explorer-empty">Nothing to do here right now.</p>`;
    }
    paymentActions.innerHTML = payHtml;

    // Rules-first gate: the thread/input stay hidden until this offer's
    // chat has been explicitly acknowledged once this session.
    const rulesIntro = document.getElementById("p2pChatRulesIntro");
    const threadBody = document.getElementById("p2pChatThreadBody");
    if (!p2pChatRulesAcknowledged.has(offerId)) {
        rulesIntro.style.display = "flex";
        threadBody.style.display = "none";
        loadChatRulesIntroText(o);
    } else {
        rulesIntro.style.display = "none";
        threadBody.style.display = "flex";
    }

    const { hub: hubAddress } = offerHubAndDirection(o);
    const messagesDiv = document.getElementById("p2pChatMessages");
    messagesDiv.innerHTML = (o.Messages || [])
        .map((m) => {
            const fromHub = hubAddress && m.Sender === hubAddress;
            const automationBadge = m.FromAutomation
                ? ` <span style="opacity:0.6;font-size:10px;">&#9889; Automated response</span>`
                : "";
            return `
        <div class="chat-message ${fromHub ? "mine" : "theirs"}">
            <div class="sender">${escapeHtml(m.Sender.slice(0, 8))}...${escapeHtml(m.Sender.slice(-6))}${automationBadge}</div>
            <div>${escapeHtmlMultiline(m.Text)}</div>
        </div>`;
        })
        .join("");
    messagesDiv.scrollTop = messagesDiv.scrollHeight;

    // Trade-completion banner - fires once, the first render after the
    // status newly lands on a funds-moved terminal state, computed
    // entirely from data already in p2pOffersCache.
    const bannerBox = document.getElementById("p2pChatCompletionBanner");
    if (
        bannerBox &&
        FUNDS_MOVED_SETTLEMENT_STATUSES.includes(o.SettlementStatus) &&
        p2pChatLastBannerStatus[offerId] !== o.SettlementStatus
    ) {
        p2pChatLastBannerStatus[offerId] = o.SettlementStatus;
        const releasedToBuyer = o.SettlementStatus !== "RESOLVED_REFUND";
        let bannerText;
        if (releasedToBuyer && isBuyer) {
            bannerText = `Trade complete - ${o.OfferAmount} ${o.OfferSymbol} credited to your wallet.`;
        } else if (releasedToBuyer && isSeller) {
            bannerText = `Trade complete - ${o.OfferAmount} ${o.OfferSymbol} was released to the buyer.`;
        } else if (!releasedToBuyer && isSeller) {
            bannerText = `Trade complete - ${o.OfferAmount} ${o.OfferSymbol} returned to your wallet.`;
        } else if (!releasedToBuyer && isBuyer) {
            bannerText = `Trade closed - the moderator refunded the seller, no funds were sent to you.`;
        } else {
            bannerText = `Trade complete - settlement finished (${o.SettlementStatus}).`;
        }
        bannerBox.innerHTML = `<div class="p2p-trade-complete-banner">${bannerText}</div>`;
    } else if (bannerBox && !FUNDS_MOVED_SETTLEMENT_STATUSES.includes(o.SettlementStatus)) {
        bannerBox.innerHTML = "";
    }

    startP2PCountdownFor(o);
    renderChatStarters(o, isBuyer, isSeller, isModerator);
    loadPaymentInstructionsForOffer(o);

    // The trade just reached a terminal state while the chat was open -
    // refresh the list so it drops out immediately rather than waiting
    // for the next 15s poll.
    if (TERMINAL_SETTLEMENT_STATUSES.includes(o.SettlementStatus)) {
        refreshActiveTradesWidget();
    }
}

// TRADE_REVIEW_ELIGIBLE_STATUSES mirrors api/trade_review.go's own
// tradeReviewEligibleStatuses exactly - a trade must have genuinely
// concluded with both sides getting what they agreed to, not just
// reached ANY terminal state (RESOLVED_REFUND and EXPIRED are
// terminal per TERMINAL_SETTLEMENT_STATUSES above but mean the trade
// fell through, not that it succeeded).
const TRADE_REVIEW_ELIGIBLE_STATUSES = ["CONFIRMED", "RESOLVED_RELEASE", "AUTO_RELEASED"];
let p2pSelectedReviewStars = 0;

// renderTradeReviewPrompt shows the star-rating prompt once this trade
// has settled successfully and the current wallet (as whichever side
// it is) hasn't reviewed the other party yet - hidden entirely for a
// moderator viewing the trade (not a party to it) or before/without a
// successful settlement.
async function renderTradeReviewPrompt(o, isSeller, isBuyer) {
    const promptEl = document.getElementById("p2pChatReviewPrompt");
    if (!promptEl) return;

    if (!(isSeller || isBuyer) || !TRADE_REVIEW_ELIGIBLE_STATUSES.includes(o.SettlementStatus)) {
        promptEl.style.display = "none";
        return;
    }

    try {
        const status = await apiGet(`/trade-review/status?tradeId=${o.ID}`);
        const alreadyReviewed = isSeller ? status.creatorReviewed : status.buyerReviewed;
        if (!status.eligible || alreadyReviewed) {
            promptEl.style.display = "none";
            return;
        }
    } catch (e) {
        promptEl.style.display = "none";
        return;
    }

    promptEl.style.display = "block";
    p2pSelectedReviewStars = 0;
    document.getElementById("p2pChatReviewComment").value = "";
    document.getElementById("p2pChatReviewStatus").innerText = "";
    renderTradeReviewStars();
}

function renderTradeReviewStars() {
    const starsEl = document.getElementById("p2pChatReviewStars");
    if (!starsEl) return;
    starsEl.innerHTML = "";
    for (let i = 1; i <= 5; i++) {
        const s = document.createElement("span");
        s.innerText = "★";
        s.className = i <= p2pSelectedReviewStars ? "selected" : "";
        s.onclick = () => {
            p2pSelectedReviewStars = i;
            renderTradeReviewStars();
        };
        starsEl.appendChild(s);
    }
}

// renderDisputeFaultChoicePrompt shows the pay-or-decline choice ONLY
// to the at-fault party of a refund-resolved dispute on this exact
// trade, only while it's still PENDING - see
// core.DisputeFaultChoice's own doc comment for the full mechanic
// (72-hour window; paying avoids the reputation penalty, declining
// accepts it immediately instead of waiting it out; doing neither in
// time applies BOTH automatically). A public read, same "everyone
// already knows the outcome" reasoning as the rest of this dispute
// machinery, but only ever rendered for the wallet it actually applies
// to - anyone else viewing this trade never sees the section at all.
async function renderDisputeFaultChoicePrompt(o) {
    const promptEl = document.getElementById("p2pChatFaultChoicePrompt");
    if (!promptEl) return;

    if (!p2pWallet || o.SettlementStatus !== "RESOLVED_REFUND") {
        promptEl.style.display = "none";
        return;
    }

    let choice;
    try {
        const result = await apiGet(`/dispute-fault-choice/status?offerId=${o.ID}`);
        if (!result.exists) {
            promptEl.style.display = "none";
            return;
        }
        choice = result.choice;
    } catch (e) {
        promptEl.style.display = "none";
        return;
    }

    if (choice.status !== "PENDING" || choice.atFaultAddress !== p2pWallet.address) {
        promptEl.style.display = "none";
        return;
    }

    const remainingSeconds = choice.deadline - Math.floor(Date.now() / 1000);
    if (remainingSeconds <= 0) {
        // Already past deadline client-side - the block-timestamp-driven
        // sweep (api/apply.go's resolveExpiredDisputeFaultChoices) will
        // apply both penalties automatically once the next block lands,
        // same as always; no point offering a choice that's about to be
        // rejected as expired anyway.
        promptEl.style.display = "none";
        return;
    }

    promptEl.style.display = "block";
    document.getElementById("p2pChatFaultChoiceDetail").innerText =
        `As the party found at fault, you can pay the moderator's fee directly (${choice.feeAmount} ${choice.symbol}) to avoid a reputation penalty, or decline and accept the penalty now instead of waiting.`;

    const hours = Math.floor(remainingSeconds / 3600);
    const minutes = Math.floor((remainingSeconds % 3600) / 60);
    document.getElementById("p2pChatFaultChoiceCountdown").innerText =
        `Time remaining to choose: ${hours}h ${minutes}m - if you do neither in time, BOTH the fee and the reputation penalty apply automatically.`;
    document.getElementById("p2pChatFaultChoiceStatus").innerText = "";
}

async function payDisputeFaultChoice() {
    const statusEl = document.getElementById("p2pChatFaultChoiceStatus");
    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/dispute-fault-choice/pay", { offerId: currentP2PChatOfferId });
        statusEl.innerText = `Payment submitted (tx ${result.txId}) - takes effect once the next block confirms it.`;
    } catch (e) {
        statusEl.innerText = "Could not submit payment: " + e.message;
    }
}

async function declineDisputeFaultChoice() {
    if (!confirm("Decline and accept the reputation penalty now? This cannot be undone.")) return;
    const statusEl = document.getElementById("p2pChatFaultChoiceStatus");
    statusEl.innerText = "Submitting...";
    try {
        const result = await apiPostSigned("/dispute-fault-choice/decline", { offerId: currentChatOfferId });
        statusEl.innerText = `Decline submitted (tx ${result.txId}) - takes effect once the next block confirms it.`;
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

async function submitTradeReview() {
    const statusEl = document.getElementById("p2pChatReviewStatus");
    if (p2pSelectedReviewStars < 1) {
        statusEl.innerText = "Tap a star rating first.";
        return;
    }
    const comment = document.getElementById("p2pChatReviewComment").value.trim();

    try {
        await apiPostSigned("/trade-review/submit", {
            tradeId: currentP2PChatOfferId,
            stars: p2pSelectedReviewStars,
            comment,
        });
        document.getElementById("p2pChatReviewPrompt").style.display = "none";
        statusEl.innerText = "";
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

// renderChatStarters gives the chat a few one-click, role- and
// status-aware phrases instead of an intimidating blank input - "make
// it more lively, write some logical things on it." Clicking one
// sends it immediately, same as typing it and hitting Send.
function renderChatStarters(o, isBuyer, isSeller, isModerator) {
    const box = document.getElementById("p2pChatStarters");
    if (!box) return;

    let starters = [];
    if (o.SettlementStatus === "AWAITING_PAYMENT" && isBuyer) {
        starters = ["On my way to send payment now", "What payment method do you prefer?", "One moment please"];
    } else if (o.SettlementStatus === "AWAITING_PAYMENT" && isSeller) {
        starters = ["Please send payment to the details provided", "Let me know once you've sent it", "Thank you!"];
    } else if (o.SettlementStatus === "SELLER_CONFIRMED" && isBuyer) {
        starters = ["Thanks, releasing now", "Give me a moment to verify", "Confirmed on my end"];
    } else if (o.SettlementStatus === "DISPUTED" && isModerator) {
        starters = ["Reviewing the evidence now", "Please provide proof of payment", "One moment while I check"];
    } else {
        starters = ["Hi, ready to proceed?", "Thank you!", "One moment please"];
    }

    box.innerHTML = starters
        .map((s) => `<span class="p2p-chat-starter-chip" onclick="sendStarterMessage('${s.replace(/'/g, "\\'")}')">${s}</span>`)
        .join("");
}

function sendStarterMessage(text) {
    document.getElementById("p2pChatInput").value = text;
    sendP2PMessage();
}

async function sendP2PMessage() {
    if (!currentP2PChatOfferId) return;
    const input = document.getElementById("p2pChatInput");
    const text = input.value.trim();
    if (!text) return;

    try {
        await apiPostSigned("/p2p/offers/message", { offerId: currentP2PChatOfferId, text });
        input.value = "";
        await loadOffers();
        renderP2PChatModal(currentP2PChatOfferId);
    } catch (e) {
        alert(e.message);
    }
}

async function markBuyerPaid(offerId) {
    if (!confirm("Confirm you have ACTUALLY sent the off-app payment? Only do this if the money has genuinely left your account - falsely marking payment as sent is fraud.")) return;
    try {
        await apiPostSigned("/p2p/offers/buyer-mark-paid", { offerId });
        await loadOffers();
        renderP2PChatModal(offerId);
    } catch (e) {
        alert(e.message);
    }
}

async function confirmSellerPaid(offerId) {
    if (!confirm("Confirm you have received the off-app payment? Only confirm if you've genuinely verified the money in your own account - don't rely on a screenshot or message alone, that's a common scam tactic. This closes the trade and releases escrow immediately, and can't be undone.")) return;
    try {
        await apiPostSigned("/p2p/offers/seller-confirm", { offerId });
        await loadOffers();
        renderP2PChatModal(offerId);
    } catch (e) {
        alert(e.message);
    }
}

// disputeAwaitingPayment lets the receiving side reject a payment
// claim BEFORE ever confirming it, instead of only being able to
// disagree after already claiming receipt - the counterpart to
// confirmSellerPaid for "no, I did not receive this."
async function disputeAwaitingPayment(offerId) {
    if (!confirm("Dispute this payment claim? This means you're saying you did NOT actually receive the payment. The assigned moderator will review and make the final call - only do this if you're genuinely certain.")) return;
    try {
        await apiPostSigned("/p2p/offers/seller-dispute", { offerId });
        await loadOffers();
        renderP2PChatModal(offerId);
    } catch (e) {
        alert(e.message);
    }
}

// disputeAwaitingPaymentAsBuyer is the buyer's own escalation path -
// see HandleP2PBuyerDispute's doc comment (api/p2p_external.go): only
// reachable once DisputeEligibleAfterSeconds has passed since the
// buyer's own markBuyerPaid claim with no response from the seller.
async function disputeAwaitingPaymentAsBuyer(offerId) {
    if (!confirm("Summon a moderator? This means the seller hasn't confirmed or disputed your payment within the wait window. The assigned moderator will review and make the final call.")) return;
    try {
        await apiPostSigned("/p2p/offers/buyer-dispute", { offerId });
        await loadOffers();
        renderP2PChatModal(offerId);
    } catch (e) {
        alert(e.message);
    }
}

async function confirmBuyer(offerId, agree) {
    const msg = agree
        ? "Confirm the seller's claim? Escrow releases to you immediately."
        : "Dispute the seller's claim? The assigned moderator will make the final call.";
    if (!confirm(msg)) return;
    try {
        await apiPostSigned("/p2p/offers/buyer-confirm", { offerId, agree });
        await loadOffers();
        renderP2PChatModal(offerId);
    } catch (e) {
        alert(e.message);
    }
}

async function resolveDispute(offerId, release) {
    const msg = release
        ? "Release escrow to the buyer? This is final."
        : "Refund escrow to the seller? This is final.";
    if (!confirm(msg)) return;
    try {
        await apiPostSigned("/p2p/offers/resolve-dispute", { offerId, release });
        await loadOffers();
        renderP2PChatModal(offerId);
    } catch (e) {
        alert(e.message);
    }
}

p2pInit();
setInterval(() => {
    if (currentHubAddress) {
        loadHubRates();
        if (p2pWallet && p2pWallet.address === currentHubAddress) {
            loadHubNotifications();
        }
    }
    if (currentP2PChatOfferId) loadOffers();
    refreshActiveTradesWidget();
}, 15000);
