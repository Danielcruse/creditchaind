// coin-icons.js - small, original inline SVG icon set replacing the
// plain text-in-circle placeholders assetRowHTML/asset.html's hero
// icon used to show. These are NOT reproductions of the real Bitcoin/
// Ethereum/USDC trademarked logos (this environment can't fetch or
// verify pixel-accurate brand assets, and hand-copying a real
// trademark from memory isn't appropriate) - each is an original,
// simple gradient badge using that network's conventional color as
// the recognizable signal, plus a monogram. CCT gets its own original
// circular mark (a stylized double-loop "cc", not borrowed from
// anywhere) matching the site's existing green brand accent.

const COIN_ICON_DEFS = {
    BTC: { colors: ["#f7931a", "#c9740a"], mark: "B" },
    BTCB: { colors: ["#f7931a", "#c9740a"], mark: "B" },
    ETH: { colors: ["#7b6cf6", "#4b3fb0"], mark: "E" },
    ETHTEST: { colors: ["#7b6cf6", "#4b3fb0"], mark: "E" },
    ETHB: { colors: ["#7b6cf6", "#4b3fb0"], mark: "E" },
    USDC: { colors: ["#2775ca", "#1a4f8f"], mark: "$" },
    USDCTEST: { colors: ["#2775ca", "#1a4f8f"], mark: "$" },
    YENE: { colors: ["#c9a227", "#8a6d1f"], mark: "¥" },
    CCT: { colors: ["#2F6BFF", "#1D4ED8"], mark: null },
};

// coinIconSVG returns a self-contained inline SVG string for a known
// symbol, or a generic gray monogram badge (first 1-2 letters) for
// anything else (custom/developer-registered tokens) - never a blank
// icon, matching this project's "no fabricated but never empty either"
// convention used elsewhere (e.g. assets.js's priceLine "No trades
// yet" fallback).
function coinIconSVG(symbol, size) {
    size = size || 36;
    const def = COIN_ICON_DEFS[symbol];
    const gradId = `coinIconGrad_${symbol}_${size}`;

    if (symbol === "CCT") {
        // Original mark: two offset interlocking rings (a simple,
        // literal "chain" reference distinct from any real token logo)
        // rather than a letter monogram.
        const [c1, c2] = def.colors;
        return `
            <svg width="${size}" height="${size}" viewBox="0 0 36 36" xmlns="http://www.w3.org/2000/svg">
                <defs>
                    <linearGradient id="${gradId}" x1="0" y1="0" x2="1" y2="1">
                        <stop offset="0%" stop-color="${c1}"/>
                        <stop offset="100%" stop-color="${c2}"/>
                    </linearGradient>
                </defs>
                <circle cx="18" cy="18" r="18" fill="url(#${gradId})"/>
                <circle cx="14" cy="18" r="6" fill="none" stroke="#04101f" stroke-width="2.4"/>
                <circle cx="22" cy="18" r="6" fill="none" stroke="#04101f" stroke-width="2.4" opacity="0.85"/>
            </svg>
        `;
    }

    const [c1, c2] = def ? def.colors : ["#9ca3af", "#6b7280"];
    const mark = def ? def.mark : (symbol || "?").slice(0, 2).toUpperCase();

    return `
        <svg width="${size}" height="${size}" viewBox="0 0 36 36" xmlns="http://www.w3.org/2000/svg">
            <defs>
                <linearGradient id="${gradId}" x1="0" y1="0" x2="1" y2="1">
                    <stop offset="0%" stop-color="${c1}"/>
                    <stop offset="100%" stop-color="${c2}"/>
                </linearGradient>
            </defs>
            <circle cx="18" cy="18" r="18" fill="url(#${gradId})"/>
            <text x="18" y="24" text-anchor="middle" font-family="Arial, sans-serif" font-weight="800"
                  font-size="${mark.length > 1 ? 13 : 16}" fill="#ffffff">${mark}</text>
        </svg>
    `;
}
