const wallet = requireWallet();

async function refreshValidator() {
    if (!wallet) return;

    try {
        const validators = await apiGet("/validators");
        const mine = validators.find((v) => v.address === wallet.address);

        document.getElementById("validatorStatus").innerText = mine && mine.active ? "Active" : "Inactive";
        document.getElementById("blocksProduced").innerText = mine ? mine.blocksProduced : 0;
        document.getElementById("validatorRewards").innerText = mine ? mine.rewards : 0;
        document.getElementById("missedBlocks").innerText = mine ? mine.missedBlocks : 0;
        document.getElementById("slashCount").innerText = mine ? mine.slashCount : 0;
        document.getElementById("uptime").innerText = mine ? Math.round(mine.uptime * 100) : 100;

        const authNode = document.getElementById("authorizedNode");
        if (authNode) {
            authNode.innerText = mine ? mine.authorizedNodeId : "-";
        }

        const w = await apiGet(`/wallet?address=${wallet.address}`);
        document.getElementById("validatorReputation").innerText = w.reputation;
        document.getElementById("validatorVotingPower").innerText = w.votingPower;
    } catch (e) {
        console.error(e);
    }
}

async function refreshConsensusStatus() {
    try {
        const status = await apiGet("/validators/status");

        document.getElementById("blockHeight").innerText = status.blockHeight;
        document.getElementById("tickInterval").innerText = status.tickIntervalSeconds;
        document.getElementById("currentLeader").innerText = status.currentLeader || "none eligible yet";

        const fallbackNotice = document.getElementById("fallbackNotice");
        if (fallbackNotice) {
            fallbackNotice.innerText = status.strictEligibilityMet
                ? ""
                : "No validator currently meets the full reputation requirement - block production is running on the relaxed fallback tier (stake/uptime still required) to keep the network from stalling.";
        }

        const yourTurn = document.getElementById("yourTurn");
        if (status.currentLeader && wallet && status.currentLeader === wallet.address) {
            yourTurn.innerText = "It's your turn - you will produce the next block automatically.";
        } else {
            yourTurn.innerText = "";
        }
    } catch (e) {
        console.error(e);
    }
}

async function becomeValidator() {
    if (!wallet) return;

    try {
        // You must explicitly sign WHICH node you're delegating
        // block-production to, rather than just trusting whichever
        // node happens to receive the request to fill that in itself -
        // otherwise a malicious relay could rewrite that delegation in
        // transit. Fetch this node's own identity first so it's part
        // of what actually gets signed.
        const node = await apiGet("/node");

        const result = await apiPostSigned("/validators/register", {
            nodeId: node.nodeId,
            nodePublicKey: node.nodePublicKey,
        });

        alert(`Registration pending (tx ${result.txId}) - you become an active validator once the next block confirms it.`);
    } catch (e) {
        alert("Could not become a validator: " + e.message);
    }
}

async function produceBlock() {
    if (!wallet) return;

    try {
        const v = await apiPostSigned("/validators/produce-block", {});
        await refreshValidator();
        alert("Produced block #" + v.blocksProduced);
    } catch (e) {
        alert("Could not produce a block: " + e.message);
    }
}

refreshValidator();
refreshConsensusStatus();

setInterval(() => {
    refreshValidator();
    refreshConsensusStatus();
}, 10000);
