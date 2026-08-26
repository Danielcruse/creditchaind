// currency.js - shared "display currency" preference + fiat-conversion
// helper, used by settings.js (the selector) and assets.js/asset.js
// (rendering a fiat-equivalent line next to every balance). Purely a
// client-side convenience layer over the real backend price data
// (GET /price/external, GET /price/cct) - never authoritative over any
// actual on-chain rate.

const DISPLAY_CURRENCY_KEY = "cc_display_currency";
const DEFAULT_DISPLAY_CURRENCY = "USD";

function getDisplayCurrency() {
    return localStorage.getItem(DISPLAY_CURRENCY_KEY) || DEFAULT_DISPLAY_CURRENCY;
}

function setDisplayCurrency(code) {
    localStorage.setItem(DISPLAY_CURRENCY_KEY, code);
}

const CURRENCY_SYMBOLS = { USD: "$", EUR: "€", GBP: "£", NGN: "₦", CAD: "$", JPY: "¥", INR: "₹" };

function currencySymbolFor(code) {
    return CURRENCY_SYMBOLS[code] || code + " ";
}

// _ratesCache/_cctRateCache are brief in-memory caches (not persisted)
// so re-rendering several rows on the same page load doesn't fire a
// fresh request per row - cleared every RATES_CACHE_MS.
let _ratesCache = null;
let _ratesCacheAt = 0;
let _cctRateCache = null;
let _cctRateCacheAt = 0;
const RATES_CACHE_MS = 20000;

async function fetchRates(currency) {
    const now = Date.now();
    if (_ratesCache && _ratesCache.currency === currency && now - _ratesCacheAt < RATES_CACHE_MS) {
        return _ratesCache;
    }
    const data = await apiGet(`/price/external?currency=${encodeURIComponent(currency)}`);
    _ratesCache = { ...data, currency };
    _ratesCacheAt = now;
    return _ratesCache;
}

async function fetchCctRate() {
    const now = Date.now();
    if (_cctRateCache && now - _cctRateCacheAt < RATES_CACHE_MS) {
        return _cctRateCache;
    }
    _cctRateCache = await apiGet("/price/cct");
    _cctRateCacheAt = now;
    return _cctRateCache;
}

// fiatLine returns a formatted "≈ $1,234.56" string for the given
// symbol/balance, or "" if no conversion path exists yet (e.g. a
// freshly-registered custom token with no trades) - honest "no price"
// rather than a fabricated one, same convention assets.js's existing
// priceLine() already uses for "No trades yet".
async function fiatLine(symbol, balance) {
    try {
        const currency = getDisplayCurrency();
        const rates = await fetchRates(currency);
        const amount = Number(balance);
        if (!isFinite(amount) || amount < 0) return "";

        let valueInCurrency = null;

        if (symbol === "ETH" || symbol === "ETHB" || symbol === "ETHTEST") {
            valueInCurrency = amount * rates.eth;
        } else if (symbol === "BTC" || symbol === "BTCB") {
            valueInCurrency = amount * rates.btc;
        } else if (symbol === "USDCTEST" || symbol === "YENE") {
            valueInCurrency = amount * rates.usdc;
        } else if (symbol === "CCT") {
            const cct = await fetchCctRate();
            valueInCurrency = amount * cct.cctPerEth * rates.eth;
        } else {
            // Custom/independent token - chain through its own CCT-per-
            // unit trade price (if any) -> CCT -> ETH -> display currency.
            const history = await apiGet(`/pool/price-history?symbol=${encodeURIComponent(symbol)}`);
            if (!history || history.length < 1) return "";
            const cctPerToken = history[history.length - 1].price / 1e6;
            if (!(cctPerToken > 0)) return "";
            const cct = await fetchCctRate();
            valueInCurrency = amount * cctPerToken * cct.cctPerEth * rates.eth;
        }

        if (valueInCurrency === null || !isFinite(valueInCurrency)) return "";
        return `≈ ${currencySymbolFor(currency)}${valueInCurrency.toLocaleString(undefined, { maximumFractionDigits: 2, minimumFractionDigits: 2 })}`;
    } catch (e) {
        return "";
    }
}
