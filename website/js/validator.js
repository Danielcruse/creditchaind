const wallet = requireWallet();

let currentSort = "stake";
let selectedValidatorAddress = null;
let leaderboardCache = [];

function fmtPct(bps) {
    return (bps / 100).toFixed(1);
}

function shortAddr(a) {
    return a ? `${a.slice(0, 8)}...${a.slice(-6)}` : "-";
}

async function loadLeaderboard() {
    currentSort = document.getElementById("sortSelect").value;
    try {
        const list = await apiGet(`/validators?sort=${currentSort}`);
        leaderboardCache = list;

        const body = document.getElementById("leaderboardBody");
        if (list.length === 0) {
            body.innerHTML = `<tr><td colspan="9" class="explorer-empty">No validators registered yet.</td></tr>`;
        } else {
            body.innerHTML = list
                .map(
                    (v, i) => `
                <tr onclick="openValidatorDetail('${v.address}')">
                    <td data-label="#">${i + 1}</td>
                    <td data-label="Validator" class="explorer-hash">${shortAddr(v.address)}${
                        v.isCurrentLeader ? '<span class="validator-badge leader">Leader</span>' : ""
                    }</td>
                    <td data-label="Status"><span class="validator-badge ${v.active ? "active" : "inactive"}">${
                        v.active ? "Active" : "Inactive"
                    }</span></td>
                    <td data-label="Commission">${fmtPct(v.commissionBps)}%</td>
                    <td data-label="Effective Stake">${v.effectiveStake}</td>
                    <td data-label="Delegators">${v.delegatorCount}</td>
                    <td data-label="Uptime">${Math.round(v.uptime * 100)}%</td>
                    <td data-label="Blocks">${v.blocksProduced}</td>
                    <td data-label="Est. APY">${v.estimatedApy > 0 ? v.estimatedApy.toFixed(2) + "%" : "-"}</td>
                </tr>
            `
                )
                .join("");
        }

        if (selectedValidatorAddress) {
            renderValidatorDetail(selectedValidatorAddress);
        }

        const mine = list.find((v) => v.address === wallet.address);
        renderMyValidatorCard(mine);
    } catch (e) {
        console.error(e);
    }
}

function renderMyValidatorCard(mine) {
    const card = document.getElementById("myValidatorCard");
    if (!mine) {
        card.style.display = "none";
        return;
    }
    card.style.display = "block";
    document.getElementById("myValidatorStatus").innerText = mine.active ? "Active" : "Inactive";
    document.getElementById("myAuthorizedNode").innerText = mine.authorizedNodeId || "-";
    document.getElementById("myReputation").innerText = mine.reputation;
    document.getElementById("myVotingPower").innerText = mine.votingPower;
    document.getElementById("myBlocksProduced").innerText = mine.blocksProduced;
    document.getElementById("myRewards").innerText = mine.rewards;
    document.getElementById("myMissedBlocks").innerText = mine.missedBlocks;
    document.getElementById("mySlashCount").innerText = mine.slashCount;
    document.getElementById("myUptime").innerText = Math.round(mine.uptime * 100);
    document.getElementById("myCommission").innerText = fmtPct(mine.commissionBps);
    document.getElementById("myTotalDelegated").innerText = mine.totalDelegated;
    document.getElementById("myDelegatorCount").innerText = mine.delegatorCount;
}

async function openValidatorDetail(address) {
    selectedValidatorAddress = address;
    document.getElementById("leaderboardCard").style.display = "none";
    document.getElementById("myDelegationsCard").style.display = "none";
    document.getElementById("validatorDetailCard").style.display = "block";
    document.getElementById("delegateAmount").value = "";
    document.getElementById("delegateEstimate").innerText = "";
    document.getElementById("delegateStatus").innerText = "";
    await renderValidatorDetail(address);
}

function closeValidatorDetail() {
    selectedValidatorAddress = null;
    document.getElementById("validatorDetailCard").style.display = "none";
    document.getElementById("leaderboardCard").style.display = "block";
    document.getElementById("myDelegationsCard").style.display = "block";
}

async function renderValidatorDetail(address) {
    let v = leaderboardCache.find((x) => x.address === address);
    if (!v) {
        const fresh = await apiGet(`/validators?sort=stake`);
        leaderboardCache = fresh;
        v = fresh.find((x) => x.address === address);
    }
    if (!v) return;

    document.getElementById("detailAddress").innerText = address;
    document.getElementById("detailStats").innerHTML = `
        <div class="explorer-row"><span>Status</span><span>${v.active ? "Active" : "Inactive"}${
        v.isCurrentLeader ? " (current leader)" : ""
    }</span></div>
        <div class="explorer-row"><span>Commission</span><span>${fmtPct(v.commissionBps)}%</span></div>
        <div class="explorer-row"><span>Own Stake (ETHTEST)</span><span>${v.stake}</span></div>
        <div class="explorer-row"><span>Total Delegated (CCT)</span><span>${v.totalDelegated}</span></div>
        <div class="explorer-row"><span>Effective Stake</span><span>${v.effectiveStake}</span></div>
        <div class="explorer-row"><span>Delegators</span><span>${v.delegatorCount}</span></div>
        <div class="explorer-row"><span>Reputation</span><span>${v.reputation}</span></div>
        <div class="explorer-row"><span>Uptime</span><span>${Math.round(v.uptime * 100)}%</span></div>
        <div class="explorer-row"><span>Blocks Produced</span><span>${v.blocksProduced}</span></div>
        <div class="explorer-row"><span>Missed Blocks</span><span>${v.missedBlocks}</span></div>
        <div class="explorer-row"><span>Slash Count</span><span>${v.slashCount}</span></div>
        <div class="explorer-row"><span>Est. APY for delegators</span><span>${
            v.estimatedApy > 0 ? v.estimatedApy.toFixed(2) + "%" : "n/a (no delegators yet, or inactive)"
        }</span></div>
    `;

    const commissionEditor = document.getElementById("detailCommissionEditor");
    if (address === wallet.address) {
        commissionEditor.style.display = "block";
        document.getElementById("commissionInput").placeholder = `Current: ${fmtPct(v.commissionBps)}%`;
    } else {
        commissionEditor.style.display = "none";
    }

    await loadDelegatorsList(address);
}

// updateDelegateEstimate is a plain-language preview, not a numeric
// promise - see api/validators.go's estimateDelegatorAPY doc comment
// for why a precise post-delegation APY isn't something this frontend
// tries to compute (PoSR selection also weighs reputation and uptime,
// not just stake).
function updateDelegateEstimate() {
    const amount = parseInt(document.getElementById("delegateAmount").value, 10) || 0;
    const v = leaderboardCache.find((x) => x.address === selectedValidatorAddress);
    const el = document.getElementById("delegateEstimate");
    if (!v || !amount) {
        el.innerText = "";
        return;
    }
    const newTotal = v.totalDelegated + amount;
    const shareOfPool = ((amount / newTotal) * 100).toFixed(1);
    el.innerText = `This would be ~${shareOfPool}% of this validator's delegated pool, sharing in its block rewards net of its ${fmtPct(
        v.commissionBps
    )}% commission.`;
}

async function delegateToValidator() {
    if (!wallet) return;
    const amount = parseInt(document.getElementById("delegateAmount").value, 10);
    const statusEl = document.getElementById("delegateStatus");
    if (!amount || amount <= 0) {
        statusEl.innerText = "Enter an amount first.";
        return;
    }
    try {
        const result = await apiPostSigned("/validators/delegate", {
            validator: selectedValidatorAddress,
            amount,
        });
        statusEl.innerText = `Delegation pending (tx ${result.txId}) - confirms with the next block.`;
        document.getElementById("delegateAmount").value = "";
        document.getElementById("delegateEstimate").innerText = "";
        setTimeout(() => {
            loadLeaderboard();
            loadMyDelegations();
        }, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function undelegateFromValidator(validatorAddress, amount) {
    if (!wallet || !amount || amount <= 0) return;
    try {
        const result = await apiPostSigned("/validators/undelegate", {
            validator: validatorAddress,
            amount,
        });
        alert(
            `Undelegate pending (tx ${result.txId}) - your CCT stays bonded for ${result.unbondingPeriodSeconds}s before it's spendable again.`
        );
        setTimeout(() => {
            loadLeaderboard();
            loadMyDelegations();
            if (selectedValidatorAddress) renderValidatorDetail(selectedValidatorAddress);
        }, 9000);
    } catch (e) {
        alert(e.message);
    }
}

async function loadDelegatorsList(validatorAddress) {
    const el = document.getElementById("delegatorsList");
    try {
        const delegations = await apiGet(`/validators/delegations?validator=${validatorAddress}`);
        if (delegations.length === 0) {
            el.innerHTML = `<p class="explorer-empty">Nobody has delegated to this validator yet.</p>`;
            return;
        }
        el.innerHTML = delegations
            .map((d) => {
                const isMine = wallet && d.delegator === wallet.address;
                return `
                    <div class="explorer-row">
                        <span class="explorer-hash">${shortAddr(d.delegator)}${isMine ? " (you)" : ""}</span>
                        <span>${d.amount} CCT</span>
                        ${
                            isMine
                                ? `<button onclick="undelegateFromValidator('${validatorAddress}', ${d.amount})">Undelegate</button>`
                                : ""
                        }
                    </div>
                `;
            })
            .join("");
    } catch (e) {
        console.error(e);
    }
}

async function loadMyDelegations() {
    if (!wallet) return;
    const card = document.getElementById("myDelegationsCard");
    const el = document.getElementById("myDelegationsList");
    try {
        const delegations = await apiGet(`/validators/delegations?delegator=${wallet.address}`);
        if (delegations.length === 0) {
            card.style.display = "none";
            return;
        }
        card.style.display = "block";
        el.innerHTML = delegations
            .map(
                (d) => `
                <div class="explorer-row">
                    <span class="explorer-hash">${shortAddr(d.validator)}</span>
                    <span>${d.amount} CCT</span>
                    <button onclick="openValidatorDetail('${d.validator}')">View</button>
                    <button onclick="undelegateFromValidator('${d.validator}', ${d.amount})">Undelegate</button>
                </div>
            `
            )
            .join("");
    } catch (e) {
        console.error(e);
    }
}

async function setCommission() {
    if (!wallet) return;
    const pct = parseFloat(document.getElementById("commissionInput").value);
    const statusEl = document.getElementById("commissionStatus");
    if (isNaN(pct) || pct < 0 || pct > 50) {
        statusEl.innerText = "Enter a commission between 0 and 50%.";
        return;
    }
    try {
        const result = await apiPostSigned("/validators/commission", {
            commissionBps: Math.round(pct * 100),
        });
        statusEl.innerText = `Commission update pending (tx ${result.txId}).`;
        setTimeout(() => {
            loadLeaderboard();
            if (selectedValidatorAddress) renderValidatorDetail(selectedValidatorAddress);
        }, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
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
        setTimeout(loadLeaderboard, 9000);
    } catch (e) {
        alert("Could not become a validator: " + e.message);
    }
}

async function produceBlock() {
    if (!wallet) return;

    try {
        const v = await apiPostSigned("/validators/produce-block", {});
        await loadLeaderboard();
        alert("Produced block #" + v.blocksProduced);
    } catch (e) {
        alert("Could not produce a block: " + e.message);
    }
}

loadLeaderboard();
loadMyDelegations();
refreshConsensusStatus();

setInterval(() => {
    loadLeaderboard();
    if (!selectedValidatorAddress) loadMyDelegations();
    refreshConsensusStatus();
}, 10000);
