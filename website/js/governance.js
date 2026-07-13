const wallet = requireWallet();

async function checkGovernanceActivation() {
    if (!wallet) return;

    try {
        const w = await apiGet(`/wallet?address=${wallet.address}`);
        const activated = w.votingPower > 0;

        document.getElementById("govNotActivated").style.display = activated ? "none" : "block";
        document.getElementById("govActivated").style.display = activated ? "block" : "none";
    } catch (e) {
        console.error(e);
    }
}

async function activateGovernance() {
    if (!wallet) return;

    try {
        const result = await apiPostSigned("/governance/activate", {});
        document.getElementById("activateStatus").innerText =
            `Activating (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(checkGovernanceActivation, 9000);
    } catch (e) {
        document.getElementById("activateStatus").innerText = "Could not activate: " + e.message;
    }
}

async function postGovernanceStatus() {
    if (!wallet) return;

    const text = document.getElementById("statusText").value.trim();
    if (!text) {
        alert("Enter a status update first.");
        return;
    }

    try {
        await apiPostSigned("/governance/status/post", { text });
        document.getElementById("statusText").value = "";
        document.getElementById("statusPostResult").innerText = "Posted - now live on the landing page.";
        loadStatusFeed();
    } catch (e) {
        document.getElementById("statusPostResult").innerText = "Could not post: " + e.message;
    }
}

async function loadStatusFeed() {
    const div = document.getElementById("statusFeed");

    try {
        const statuses = await apiGet("/governance/status/recent");

        if (!statuses || statuses.length === 0) {
            div.innerHTML = "No community updates yet.";
            return;
        }

        div.innerHTML = statuses
            .map((s) => {
                const time = s.Timestamp ? new Date(s.Timestamp * 1000).toLocaleString() : "-";
                return `
                    <div style="padding:8px 0;border-bottom:1px solid rgba(255,255,255,0.08);">
                        <p style="margin:0 0 4px;">${s.Text}</p>
                        <p style="margin:0;opacity:0.55;font-size:11px;">${formatAddress(s.Address)} &middot; ${time}</p>
                    </div>
                `;
            })
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = "No community updates yet.";
    }
}

async function createProposal() {
    if (!wallet) return;

    const title = document.getElementById("proposalTitle").value;
    const description = document.getElementById("proposalDescription").value;

    if (title === "") {
        alert("Title required.");
        return;
    }

    try {
        const result = await apiPostSigned("/governance/create", {
            title: title,
            description: description,
        });

        document.getElementById("proposalTitle").value = "";
        document.getElementById("proposalDescription").value = "";

        document.getElementById("pendingStatus").innerText =
            `Pending (tx ${result.txId}) - appears below once the next block confirms it.`;
    } catch (e) {
        alert("Could not create proposal: " + e.message);
    }
}

async function vote(id, support) {
    if (!wallet) return;

    try {
        const result = await apiPostSigned("/governance/vote", {
            proposalId: id,
            support: support,
        });

        document.getElementById("pendingStatus").innerText =
            `Vote pending (tx ${result.txId}) - tally updates once confirmed.`;
    } catch (e) {
        alert("Vote failed: " + e.message);
    }
}

async function render() {
    const div = document.getElementById("proposalList");

    try {
        const proposals = await apiGet("/governance");

        div.innerHTML = "";

        if (!proposals || proposals.length === 0) {
            div.innerHTML = "No proposals yet.";
            return;
        }

        proposals.forEach((p) => {
            div.innerHTML += `
                <div class="card">
                    <h3>${p.Title}</h3>
                    <p>${p.Description}</p>
                    <p>YES: ${p.YesVotes}</p>
                    <p>NO: ${p.NoVotes}</p>
                    <button onclick="vote('${p.ID}', true)">Vote Yes</button>
                    <button onclick="vote('${p.ID}', false)">Vote No</button>
                </div>
                <br>
            `;
        });
    } catch (e) {
        console.error(e);
        div.innerHTML = "No proposals yet.";
    }
}

render();
checkGovernanceActivation();
loadStatusFeed();

setInterval(render, 10000);
setInterval(loadStatusFeed, 10000);
