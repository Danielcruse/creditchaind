// network.html - the full-page version of outernet.html's Network
// Overview card, split out per the user's request to separate wallet/
// assets/profile/network into their own pages. Same /network endpoint
// outernet.js's loadNetwork already uses - no new backend surface.

async function loadNetwork() {
    try {
        document.getElementById("networkNameLabel").innerText =
            window.CC_NETWORK === "mainnet" ? "CreditChain Mainnet" : "CreditChain Testnet";

        const data = await apiGet("/network");

        document.getElementById("height").innerText = data.blockHeight;
        document.getElementById("hash").innerText = data.latestBlockHash;
        document.getElementById("peers").innerText = data.peerCount;
        document.getElementById("validators").innerText = data.activeValidators;
        document.getElementById("tps").innerText = data.tps;
        document.getElementById("gas").innerText = data.gasPrice;
        // TotalSupply/CirculatingSupply are 18-decimal smallest-unit
        // decimal strings now (see api/network.go).
        document.getElementById("supply").innerText = cctFromSmallest(data.totalSupply);
        document.getElementById("circulating").innerText = cctFromSmallest(data.circulatingSupply);
        document.querySelector(".network-live").innerText = `● ${data.status}`;
    } catch (e) {
        console.error(e);
    }
}

loadNetwork();
setInterval(loadNetwork, 5000);
