// developers.js powers website/developers.html - a real entry point
// for building against the CreditChain API, not a placeholder.

document.getElementById("devApiBase").innerText = API_BASE;
document.querySelectorAll(".dev-base-inline").forEach((el) => {
    el.innerText = API_BASE;
});

async function tryLiveNetworkCall() {
    const resultEl = document.getElementById("devTryResult");
    resultEl.innerText = "Loading...";
    try {
        const data = await apiGet("/network");
        resultEl.innerText = JSON.stringify(data, null, 2);
    } catch (e) {
        resultEl.innerText = "Request failed: " + e.message;
    }
}

// ENDPOINT_GROUPS is a curated index of this API - not every field of
// every request/response, just enough for a developer to know where
// to start reading the actual handler source (api/*.go) for exact
// shapes. GET unless marked otherwise.
const ENDPOINT_GROUPS = [
    {
        name: "Wallet & Identity",
        endpoints: [
            ["POST /wallet/create", "Register {address, publicKey} derived client-side from a BIP-39 mnemonic (see js/hdwallet.js) - server never generates or sees a private key"],
            ["/wallet?address=", "Balance, reputation, voting power, node ID for one address"],
            ["/reputation?address=", "Reputation score + credit tier"],
            ["POST /session/login-ping", "(signed) Record a login-activity notification"],
        ],
    },
    {
        name: "Explorer",
        endpoints: [
            ["/explorer?blocksLimit=&txLimit=", "Recent blocks + transactions, paginated"],
            ["/explorer/search?q=", "Search by block #, block hash, tx ID, or address"],
            ["/network", "Chain height, latest block hash, peer count, TPS"],
            ["/mempool", "Currently pending (unconfirmed) transactions"],
        ],
    },
    {
        name: "Tokens & Balances",
        endpoints: [
            ["/token/balance?address=", "CCT balance"],
            ["/rept/balance?address=", "REPT (reputation token) balance"],
            ["/ethtest/balance?address=", "ETHTEST balance (derived sub-address - see whitepaper)"],
            ["/usdctest/balance?address=", "USDCTEST balance (derived sub-address)"],
            ["/tokens", "List every developer-registered custom token"],
            ["POST /tokens/register", "(signed) Register a new custom token + seed its pool"],
            ["POST /token/transfer", "(signed) Send CCT/ETHTEST/USDCTEST/a custom token"],
            ["POST /faucet", "(signed) One-time testnet token claim"],
        ],
    },
    {
        name: "Pool & Swaps",
        endpoints: [
            ["/pools", "Every pool (native + custom) and whether it's tradeable"],
            ["/pool/price-history?symbol=", "Price time series for one token"],
            ["POST /pool/swap", "(signed) Swap through the CCT-hub AMM"],
            ["POST /pool/add-liquidity", "(signed) Add liquidity to a pool"],
            ["POST /pool/remove-liquidity", "(signed) Remove liquidity"],
        ],
    },
    {
        name: "Staking & Validators",
        endpoints: [
            ["POST /stake", "(signed) Stake CCT or ETHTEST"],
            ["POST /unstake", "(signed) Begin unbonding (cooldown period applies)"],
            ["/staking?address=", "Your current stakes + unbonding entries"],
            ["/validators", "List every registered validator"],
            ["POST /validators/register", "(signed) Register as a validator, delegated to this node"],
        ],
    },
    {
        name: "Lending & Borrowing",
        endpoints: [
            ["POST /borrow", "(signed) Self-funded borrow against locked collateral"],
            ["/loans?address=", "Your loans (self-funded and peer-funded)"],
            ["/loans/offers", "Browse open peer-funded lending offers"],
            ["POST /loans/offers/create", "(signed) Post a lending offer (requires KYC)"],
            ["POST /negotiations/start", "(signed) Propose drawing against an offer"],
            ["POST /repay", "(signed) Repay a loan"],
        ],
    },
    {
        name: "P2P Trading & Trading Hubs",
        endpoints: [
            ["/p2p/offers", "List every P2P offer"],
            ["POST /p2p/offers/create", "(signed) Post a fixed-price P2P offer"],
            ["/companies", "List verified Trading Hubs"],
            ["/hub-rates?hub=", "A hub's posted buy/sell rates"],
            ["POST /hub-rates/trade", "(signed) Trade against a hub's rate"],
            ["POST /hub-agents/apply", "(signed) Apply to become one of a hub's agents"],
        ],
    },
    {
        name: "Governance",
        endpoints: [
            ["/governance", "List every proposal + vote tally"],
            ["POST /governance/create", "(signed) Create a proposal"],
            ["POST /governance/vote", "(signed) Vote on a proposal"],
            ["POST /governance/activate", "(signed) Activate voting rights"],
            ["/governance/status/recent", "Recent community status posts (public, no auth)"],
        ],
    },
    {
        name: "Node / Peer (running your own node)",
        endpoints: [
            ["/node", "This node's own identity + public key"],
            ["/peers", "This node's known peers"],
            ["/health", "Liveness check"],
        ],
    },
];

document.getElementById("devEndpointGroups").innerHTML = ENDPOINT_GROUPS.map(
    (group) => `
        <div class="dev-endpoint-group">
            <h4>${group.name}</h4>
            ${group.endpoints
                .map(
                    ([path, desc]) => `
                <div class="dev-endpoint-row">
                    <span class="dev-endpoint-path">${path}</span>
                    <span class="dev-endpoint-desc">${desc}</span>
                </div>`
                )
                .join("")}
        </div>`
).join("");
