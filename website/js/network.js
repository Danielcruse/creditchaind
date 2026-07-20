// network.html - the full-page version of outernet.html's Network
// Overview card, split out per the user's request to separate wallet/
// assets/profile/network into their own pages. Same /network endpoint
// outernet.js's loadNetwork already uses - no new backend surface.

async function loadNetwork() {
    try {
        const data = await apiGet("/network");

        document.getElementById("height").innerText = data.blockHeight;
        document.getElementById("hash").innerText = data.latestBlockHash;
        document.getElementById("peers").innerText = data.peerCount;
        document.getElementById("validators").innerText = data.activeValidators;
        document.getElementById("tps").innerText = data.tps;
        document.getElementById("gas").innerText = data.gasPrice;
        document.getElementById("supply").innerText = data.totalSupply;
        document.getElementById("circulating").innerText = data.circulatingSupply;
        document.querySelector(".network-live").innerText = `● ${data.status}`;
    } catch (e) {
        console.error(e);
    }
}

loadNetwork();
setInterval(loadNetwork, 5000);
