const API =
    "http://localhost:8080";

const walletAddress =
    localStorage.getItem(
        "walletAddress"
    ) ||
    localStorage.getItem(
        "address"
    ) ||
    "CCT123";

document.getElementById(
    "wallet"
).innerText =
    walletAddress;

async function getJSON(
    url
) {
    try {
        const res =
            await fetch(url);

        if (!res.ok)
            throw new Error(
                "Request failed"
            );

        return await res.json();
    }
    catch (e) {
        console.error(e);
        return null;
    }
}

async function loadNetwork() {

    const data =
        await getJSON(
            `${API}/network`
        );

    if (!data)
        return;

    document.getElementById(
        "height"
    ).innerText =
        data.blockHeight;

    document.getElementById(
        "hash"
    ).innerText =
        data.latestBlockHash;

    document.getElementById(
        "peers"
    ).innerText =
        data.peerCount;

    document.getElementById(
        "validators"
    ).innerText =
        data.activeValidators;

    document.getElementById(
        "tps"
    ).innerText =
        data.tps;

    document.getElementById(
        "gas"
    ).innerText =
        data.gasPrice;

    document.getElementById(
        "supply"
    ).innerText =
        data.totalSupply;

    document.getElementById(
        "circulating"
    ).innerText =
        data.circulatingSupply;

    document.querySelector(
        ".network-live"
    ).innerText =
        `● ${data.status}`;
}

async function loadWallet() {

    const data =
        await getJSON(
            `${API}/wallet?address=${walletAddress}`
        );

    if (!data)
        return;

    document.getElementById(
        "balance"
    ).innerText =
        `${data.cctBalance} CCT`;

    document.getElementById(
        "nodeId"
    ).innerText =
        data.nodeId ||
        "Not Connected";

    document.getElementById(
        "voting"
    ).innerText =
        data.votingPower;

    document.getElementById(
        "credit"
    ).innerText =
        data.creditScore;
}

async function loadReputation() {

    const data =
        await getJSON(
            `${API}/reputation?address=${walletAddress}`
        );

    if (!data)
        return;

    document.getElementById(
        "reputation"
    ).innerText =
        data.score;

    let level =
        "Genesis";

    if (
        data.score >= 80
    )
        level =
            "Legend";

    else if (
        data.score >= 60
    )
        level =
            "Elite";

    else if (
        data.score >= 40
    )
        level =
            "Builder";

    document.getElementById(
        "level"
    ).innerText =
        level;
}

async function loadStaking() {

    const data =
        await getJSON(
            `${API}/staking?address=${walletAddress}`
        );

    if (!data)
        return;

    document.getElementById(
        "balance"
    ).innerText =
        `${data.oethBalance} OETH`;
}

async function loadExplorer() {

    const data =
        await getJSON(
            `${API}/explorer`
        );

    if (!data)
        return;

    document.getElementById(
        "blocks"
    ).innerText =
        JSON.stringify(
            data.recentBlocks
                .slice(-5)
                .reverse(),
            null,
            2
        );

    document.getElementById(
        "transactions"
    ).innerText =
        JSON.stringify(
            data.recentTransactions,
            null,
            2
        );
}

async function loadMempool() {

    const data =
        await getJSON(
            `${API}/mempool`
        );

    if (!data)
        return;

    document.getElementById(
        "activity"
    ).innerText =
        `${data.length}
         pending transactions`;
}

async function refresh() {

    await loadNetwork();

    await loadWallet();

    await loadReputation();

    await loadStaking();

    await loadExplorer();

    await loadMempool();
}

refresh();

setInterval(
    refresh,
    5000
);