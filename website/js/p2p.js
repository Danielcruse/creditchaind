const p2pWallet = getWallet();

// Every symbol a hub can post a rate for - same list Pool/Hub Rate
// already traded natively before this redesign.
const p2pNativeSymbols = ["CCT", "ETHTEST", "USDCTEST", "REPT"];
const p2pSymbolLabels = { CCT: "CCT", ETHTEST: "ETH", USDCTEST: "USDC", REPT: "REPT" };

const ACTIVE_CHAT_STORAGE_KEY = "cc_active_p2p_chat";
// Settlement states that mean "nothing left to do here" - once a
// trade reaches one of these, the persistent top-right banner should
// stop pointing back at it.
const TERMINAL_SETTLEMENT_STATUSES = ["CONFIRMED", "RESOLVED_RELEASE", "RESOLVED_REFUND"];

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
    loadHubDirectory();
    restoreActiveChatBanner();
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
    } else {
        fiatInput.style.display = "";
        counterSelect.style.display = "none";
    }
}

// --- Hub directory: one bold rectangular card per hub -------------------

async function loadHubDirectory() {
    try {
        const companies = await apiGet("/companies");
        p2pCompaniesCache = (companies || []).filter((c) => c.Status === "VERIFIED");

        const body = document.getElementById("hubDirectoryBody");
        if (p2pCompaniesCache.length === 0) {
            body.innerHTML = `<p class="explorer-empty">No verified Trading Hubs yet.</p>`;
            return;
        }

        body.innerHTML = p2pCompaniesCache
            .map(
                (c) => `
            <div class="hub-card">
                <div>
                    <div class="hub-card-name">${c.Name}</div>
                    <div class="hub-card-badge">
                        <span title="Moderator-reviewed testnet badge - not real KYC/AML" style="color:#8ef;">&#9989; Verified</span>
                    </div>
                </div>
                <button class="hub-view-btn" onclick="viewHub('${c.Address}')">View</button>
            </div>`
            )
            .join("");
    } catch (e) {
        console.error(e);
    }
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
// fills both sessions' columns regardless of which one is visible),
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
    document.getElementById("rateManagerHeading").innerText = currentHubIsMine
        ? "Manage Your Rate"
        : `Manage Rates (as a worker for ${company ? company.Name : "this hub"})`;

    await loadHubRates();
    await loadHubRulesForDisplay(address);
    await loadHubStorefrontForDisplay(address);
    if (currentHubIsMine || currentHubIsWorker) {
        await loadHubNotifications();
    }
    if (currentHubIsMine) {
        document.getElementById("hubRulesText").value = hubRulesCache;
        await loadHubWorkers();
        document.getElementById("hubStorefrontHtml").value = hubStorefrontCache;
    }
}

// --- Off-app / on-chain rate posting -------------------------------------

async function setHubRate() {
    const symbol = document.getElementById("hubRateSymbol").value;
    const mode = document.getElementById("hubRateMode").value;
    const fiatSymbol = mode === "ONCHAIN"
        ? document.getElementById("hubRateCounterSymbol").value
        : document.getElementById("hubRateFiatSymbol").value.trim();
    const sellRate = parseInt(document.getElementById("hubRateSellRate").value, 10) || 0;
    const buyRate = parseInt(document.getElementById("hubRateBuyRate").value, 10) || 0;
    const active = document.getElementById("hubRateActive").checked;
    const statusEl = document.getElementById("hubRateSetStatus");

    if (!fiatSymbol) {
        statusEl.innerText = mode === "ONCHAIN"
            ? "Choose a counter token."
            : "Enter a label for your off-app currency (e.g. TESTFIAT).";
        return;
    }
    if (!sellRate && !buyRate) {
        statusEl.innerText = "Set at least one of sell rate / buy rate.";
        return;
    }

    try {
        const result = await apiPostSigned("/hub-rates/set", { hub: currentHubAddress, symbol, fiatSymbol, sellRate, buyRate, active, mode });
        statusEl.innerText = `Rate pending confirmation (tx ${result.txId}).`;
        setTimeout(loadHubRates, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

function tokenCardId(symbol, direction) {
    return `hubCard_${direction}_${symbol}`;
}

function renderTokenCard(rate, direction) {
    const symbol = rate.Symbol;
    const label = p2pSymbolLabels[symbol] || symbol;
    const isBuy = direction === "BUY_FROM_HUB";
    const rateValue = isBuy ? rate.SellRate : rate.BuyRate;
    const counterLabel = rate.Mode === "ONCHAIN" ? (p2pSymbolLabels[rate.FiatSymbol] || rate.FiatSymbol) : rate.FiatSymbol;

    const id = tokenCardId(symbol, direction);
    let capLine = "";
    if (isBuy) {
        capLine = `<p style="opacity:0.6;font-size:11px;">Available: ${rate.HubSymbolBalance} ${label}</p>`;
    } else if (rate.Mode === "ONCHAIN") {
        capLine = `<p style="opacity:0.6;font-size:11px;">Hub can pay up to: ${rate.HubCounterBalance || 0} ${counterLabel}</p>`;
    }

    return `
        <div class="hub-token-card">
            <h4>${label}</h4>
            <p style="opacity:0.75;font-size:12px;">Rate: ${rateValue} ${counterLabel} per ${label}</p>
            ${capLine}
            <div class="explorer-search" style="margin-bottom:6px;">
                <input id="${id}_amount" type="number" min="0" placeholder="Amount of ${label}" oninput="updateHubTradeQuote('${symbol}', '${direction}', '${rate.Mode || "OFFAPP"}')">
            </div>
            <div id="${id}_quote"></div>
            <button onclick="requestHubTrade('${symbol}', '${direction}', '${rate.Mode || "OFFAPP"}')">${isBuy ? "Buy" : "Sell"} ${label}</button>
        </div>
    `;
}

function findRate(symbol, mode) {
    return p2pHubRatesCache.find((r) => r.Symbol === symbol && (r.Mode || "OFFAPP") === mode);
}

async function loadHubRates() {
    if (!currentHubAddress) return;
    try {
        const rates = await apiGet(`/hub-rates?hub=${currentHubAddress}`);
        p2pHubRatesCache = rates || [];

        // Snapshot any amount the user is mid-typing (and which input had
        // focus) before replacing the cards below - this runs on a 15s
        // timer (see setInterval near the bottom of this file)
        // regardless of what the user is doing, so without this a typed
        // amount and its live quote would vanish out from under them.
        const preservedValues = {};
        document.querySelectorAll('[id$="_amount"]').forEach((el) => {
            if (el.value) preservedValues[el.id] = el.value;
        });
        const focusedId = document.activeElement && document.activeElement.id;
        const selectionStart = document.activeElement && document.activeElement.selectionStart;

        const offApp = p2pHubRatesCache.filter((r) => (r.Mode || "OFFAPP") === "OFFAPP");
        const onChain = p2pHubRatesCache.filter((r) => r.Mode === "ONCHAIN");

        const buyCol = document.getElementById("hubBuyColumn");
        const sellCol = document.getElementById("hubSellColumn");
        const onChainBuyCol = document.getElementById("hubOnChainBuyColumn");
        const onChainSellCol = document.getElementById("hubOnChainSellColumn");

        const buyable = offApp.filter((r) => r.Active && r.SellRate > 0);
        const sellable = offApp.filter((r) => r.Active && r.BuyRate > 0);
        const onChainBuyable = onChain.filter((r) => r.Active && r.SellRate > 0);
        const onChainSellable = onChain.filter((r) => r.Active && r.BuyRate > 0);

        buyCol.innerHTML = buyable.length
            ? buyable.map((r) => renderTokenCard(r, "BUY_FROM_HUB")).join("")
            : `<p class="explorer-empty">This hub isn't selling anything right now.</p>`;

        sellCol.innerHTML = sellable.length
            ? sellable.map((r) => renderTokenCard(r, "SELL_TO_HUB")).join("")
            : `<p class="explorer-empty">This hub isn't buying anything right now.</p>`;

        onChainBuyCol.innerHTML = onChainBuyable.length
            ? onChainBuyable.map((r) => renderTokenCard(r, "BUY_FROM_HUB")).join("")
            : `<p class="explorer-empty">This hub isn't swapping out anything right now.</p>`;

        onChainSellCol.innerHTML = onChainSellable.length
            ? onChainSellable.map((r) => renderTokenCard(r, "SELL_TO_HUB")).join("")
            : `<p class="explorer-empty">This hub isn't swapping in anything right now.</p>`;

        // Restore whatever the user had typed (and its live quote) into
        // the freshly-rendered cards, keyed by symbol+direction since the
        // DOM nodes themselves are brand new.
        Object.entries(preservedValues).forEach(([id, value]) => {
            const el = document.getElementById(id);
            if (!el) return;
            el.value = value;
            const match = id.match(/^hubCard_(BUY_FROM_HUB|SELL_TO_HUB)_(.+)_amount$/);
            if (!match) return;
            const [, direction, symbol] = match;
            const mode = onChainBuyCol.contains(el) || onChainSellCol.contains(el) ? "ONCHAIN" : "OFFAPP";
            updateHubTradeQuote(symbol, direction, mode);
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
    } catch (e) {
        console.error(e);
    }
}

// updateHubTradeQuote is the pre-purchase confirmation step: as soon
// as an amount is entered, this renders a distinct, clearly-labeled
// box showing exactly what the trader will owe/receive in total -
// deliberately not just small inline text - so nobody clicks Buy/Sell
// without having already seen the real total at this hub's rate.
function updateHubTradeQuote(symbol, direction, mode) {
    const rate = findRate(symbol, mode);
    if (!rate) return;

    const id = tokenCardId(symbol, direction);
    const amount = parseInt(document.getElementById(`${id}_amount`).value, 10) || 0;
    const quoteDiv = document.getElementById(`${id}_quote`);
    const label = p2pSymbolLabels[symbol] || symbol;
    const counterLabel = mode === "ONCHAIN" ? (p2pSymbolLabels[rate.FiatSymbol] || rate.FiatSymbol) : rate.FiatSymbol;
    const onAppSuffix = mode === "ONCHAIN" ? "" : " (off-app)";

    if (!amount) {
        quoteDiv.innerHTML = "";
        return;
    }

    // Platform fee: taken from whichever side is RECEIVING at
    // settlement, never from what a trader pays in - see
    // applyPlatformFee/skimPlatformFee in api/apply.go. Off-app trades
    // don't settle the fee until the chat concludes, but the same
    // 0.3%-off-the-receiving-side rule applies there too.
    const feeLine = (net, netSymbol) =>
        `<div style="font-size:11px;opacity:0.75;margin-top:4px;">${PLATFORM_FEE_PERCENT}% platform fee already reflected - you'll net ~${net} ${netSymbol}.</div>`;

    if (direction === "BUY_FROM_HUB") {
        if (amount > rate.HubSymbolBalance) {
            quoteDiv.innerHTML = `<div class="p2p-quote-box" style="border-color:#ff6b6b;background:rgba(255,107,107,0.1);">Only ${rate.HubSymbolBalance} ${label} available from this hub right now.</div>`;
            return;
        }
        const owe = amount * rate.SellRate;
        const net = Math.floor(amount * (1 - PLATFORM_FEE_PERCENT / 100));
        quoteDiv.innerHTML = `
            <div class="p2p-quote-box">
                <div class="p2p-quote-label">Your Total</div>
                <div class="p2p-quote-total">You will owe ${owe} ${counterLabel}${onAppSuffix} for ${amount} ${label}</div>
                ${feeLine(net, label)}
            </div>
        `;
    } else {
        if (mode === "ONCHAIN" && amount * rate.BuyRate > (rate.HubCounterBalance || 0)) {
            quoteDiv.innerHTML = `<div class="p2p-quote-box" style="border-color:#ff6b6b;background:rgba(255,107,107,0.1);">Hub only has ${rate.HubCounterBalance || 0} ${counterLabel} available to pay right now.</div>`;
            return;
        }
        const receive = amount * rate.BuyRate;
        const net = Math.floor(receive * (1 - PLATFORM_FEE_PERCENT / 100));
        quoteDiv.innerHTML = `
            <div class="p2p-quote-box">
                <div class="p2p-quote-label">Your Total</div>
                <div class="p2p-quote-total">You will pay ${amount} ${label} for ${receive} ${counterLabel}${onAppSuffix}</div>
                ${feeLine(net, counterLabel)}
            </div>
        `;
    }
}

async function requestHubTrade(symbol, direction, mode) {
    const rate = findRate(symbol, mode);
    if (!rate) return;

    const id = tokenCardId(symbol, direction);
    const amount = parseInt(document.getElementById(`${id}_amount`).value, 10);
    const label = p2pSymbolLabels[symbol] || symbol;

    if (!amount || amount <= 0) {
        alert("Enter an amount first.");
        return;
    }

    // Gate on the hub's preset rules, if it has any - the trader must
    // acknowledge them before the actual trade request fires. See
    // core.HubRules's doc comment: this is a disclosure step, not a
    // security boundary.
    if (hubRulesCache && hubRulesCache.trim()) {
        pendingTradeRequest = { symbol, direction, mode, amount, label };
        document.getElementById("rulesModalText").innerText = hubRulesCache;
        document.getElementById("rulesModalOverlay").style.display = "flex";
        return;
    }

    await executeHubTrade(symbol, direction, mode, amount, label);
}

function closeRulesModal() {
    document.getElementById("rulesModalOverlay").style.display = "none";
    pendingTradeRequest = null;
}

async function acknowledgeRulesAndTrade() {
    document.getElementById("rulesModalOverlay").style.display = "none";
    if (!pendingTradeRequest) return;
    const { symbol, direction, mode, amount, label } = pendingTradeRequest;
    pendingTradeRequest = null;
    await executeHubTrade(symbol, direction, mode, amount, label);
}

async function executeHubTrade(symbol, direction, mode, amount, label) {
    const isOnChain = mode === "ONCHAIN";
    const msg = isOnChain
        ? (direction === "BUY_FROM_HUB"
            ? `Swap for ${amount} ${label} from this hub? This settles instantly - both sides move on chain right away, nothing left to confirm.`
            : `Swap ${amount} ${label} to this hub? This settles instantly - both sides move on chain right away.`)
        : (direction === "BUY_FROM_HUB"
            ? `Buy ${amount} ${label} from this hub? This escrows their tokens now; you'll pay off-app once the chat opens.`
            : `Sell ${amount} ${label} to this hub? Your tokens go into escrow now, released once payment is confirmed.`);
    if (!confirm(msg)) return;

    try {
        const result = await apiPostSigned("/hub-rates/trade", {
            hub: currentHubAddress,
            symbol,
            amount,
            direction,
        });
        setTimeout(async () => {
            await loadOffers();
            await loadHubRates();
            if (isOnChain) {
                alert("Swap complete - balances updated.");
            } else {
                openP2PChatModal(result.offerId);
            }
        }, 9000);
    } catch (e) {
        alert(e.message);
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

    try {
        const result = await apiPostSigned("/hub-rates/rules/set", { text, paymentWindowMinutes });
        statusEl.innerText = `Rules pending confirmation (tx ${result.txId}).`;
        setTimeout(() => loadHubRulesForDisplay(currentHubAddress), 9000);
    } catch (e) {
        statusEl.innerText = e.message;
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
                        ${w.Name || "(unnamed)"} - ${w.WorkerAddress.slice(0, 8)}...${w.WorkerAddress.slice(-6)}
                        ${w.Active ? "" : " (removed)"}
                    </span>
                    ${w.Active ? `<button onclick="removeHubWorker('${w.WorkerAddress}')">Remove</button>` : ""}
                </div>`
                  )
                  .join("")
            : `<p class="explorer-empty">No workers added yet.</p>`;
    } catch (e) {
        console.error(e);
    }
}

async function addHubWorker() {
    const workerAddress = document.getElementById("hubWorkerAddress").value.trim();
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
                    <span style="opacity:${n.Read ? "0.5" : "1"};">${n.Message}</span>
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

// --- Persistent "return to active chat" banner ---------------------------

function saveActiveChatOfferId(offerId) {
    if (offerId) {
        localStorage.setItem(ACTIVE_CHAT_STORAGE_KEY, offerId);
    } else {
        localStorage.removeItem(ACTIVE_CHAT_STORAGE_KEY);
    }
}

function showActiveChatBanner(show) {
    document.getElementById("activeChatBanner").style.display = show ? "block" : "none";
}

// On page load, if a trade this wallet was chatting about is still
// active (not yet settled/resolved), show the persistent top-right
// link so leaving the site to make an off-app payment and coming back
// later doesn't strand the user with no way back into that chat.
async function restoreActiveChatBanner() {
    const storedId = localStorage.getItem(ACTIVE_CHAT_STORAGE_KEY);
    if (!storedId) return;

    try {
        const offers = await apiGet("/p2p/offers");
        p2pOffersCache = offers || [];
        const o = p2pOffersCache.find((x) => x.ID === storedId);
        if (!o || o.PaymentMode !== "EXTERNAL" || TERMINAL_SETTLEMENT_STATUSES.includes(o.SettlementStatus)) {
            saveActiveChatOfferId(null);
            showActiveChatBanner(false);
            return;
        }
        showActiveChatBanner(true);
    } catch (e) {
        console.error(e);
    }
}

function reopenActiveChat() {
    const storedId = localStorage.getItem(ACTIVE_CHAT_STORAGE_KEY);
    if (!storedId) return;
    openP2PChatModal(storedId);
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
    saveActiveChatOfferId(offerId);
    showActiveChatBanner(false);
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

    // Keep the persistent link alive if the trade is still unsettled -
    // that's exactly the case this banner exists for (the user is
    // about to go pay off-app and will need a way back in).
    restoreActiveChatBanner();
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

async function startP2PCountdownFor(o) {
    stopP2PCountdown();
    const countdownEl = document.getElementById("p2pChatCountdown");
    const noteEl = document.getElementById("p2pChatWindowNote");

    if (o.SettlementStatus !== "AWAITING_PAYMENT" || !o.AwaitingPaymentSince) {
        countdownEl.innerText = "-";
        countdownEl.classList.remove("overdue");
        noteEl.innerText = o.SettlementStatus ? `Settlement status: ${o.SettlementStatus}` : "";
        return;
    }

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

    document.getElementById("p2pChatSummary").innerText =
        `${o.OfferAmount} ${o.OfferSymbol} for ${o.WantAmount} ${o.WantSymbol} (off-app) - ` +
        `seller: ${o.Creator.slice(0, 8)}..., buyer: ${(o.AcceptedBy || "-").slice(0, 8)}... - ` +
        `settlement: ${o.SettlementStatus || "-"}`;

    document.getElementById("p2pChatModerator").innerText = o.ModeratorAddress
        ? `Moderator on standby for disputes: ${o.ModeratorAddress.slice(0, 8)}...${o.ModeratorAddress.slice(-6)}`
        : "No moderator assigned yet.";

    // Every payment-related action (and its anti-fraud warning) lives on
    // the second, timer panel - see slideToTimerPanel/slideToChatPanel.
    const paymentActions = document.getElementById("p2pChatPaymentActions");
    let payHtml = "";

    if (isBuyer && o.SettlementStatus === "AWAITING_PAYMENT") {
        if (o.BuyerMarkedPaidAt) {
            payHtml += `<p style="opacity:0.75;font-size:12px;">You marked payment sent at ${new Date(o.BuyerMarkedPaidAt * 1000).toLocaleTimeString()}. Waiting on the seller to confirm receipt.</p>`;
        } else {
            payHtml += `<div class="p2p-fraud-warning">Only click this once you have ACTUALLY sent the payment. Falsely marking payment as sent is fraud, will be flagged to the moderator, and can get you banned from this hub.</div>`;
            payHtml += `<button onclick="markBuyerPaid('${o.ID}')">I've Sent Payment</button>`;
        }
    }

    if (isSeller && o.SettlementStatus === "AWAITING_PAYMENT") {
        payHtml += `<div class="p2p-fraud-warning">Only confirm this once you have genuinely verified the money has arrived in your own account. Scammers may show fake screenshots or bank alerts to pressure you into confirming before you've actually been paid - check your real balance, not just a message.</div>`;
        payHtml += `<button onclick="confirmSellerPaid('${o.ID}')">I've Received Payment</button>`;
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

    const messagesDiv = document.getElementById("p2pChatMessages");
    messagesDiv.innerHTML = (o.Messages || [])
        .map(
            (m) => `
        <div class="chat-message">
            <div class="sender">${m.Sender.slice(0, 8)}...${m.Sender.slice(-6)}</div>
            <div>${m.Text}</div>
        </div>`
        )
        .join("");
    messagesDiv.scrollTop = messagesDiv.scrollHeight;

    startP2PCountdownFor(o);

    // The trade just reached a terminal state while the chat was open -
    // drop the persistent link since there's nothing left to return to.
    if (TERMINAL_SETTLEMENT_STATUSES.includes(o.SettlementStatus)) {
        saveActiveChatOfferId(null);
    }
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
    if (!confirm("Confirm you have received the off-app payment? Only confirm if you've genuinely verified the money in your own account - don't rely on a screenshot or message alone, that's a common scam tactic. This is recorded on chain and can't be undone.")) return;
    try {
        await apiPostSigned("/p2p/offers/seller-confirm", { offerId });
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
}, 15000);
