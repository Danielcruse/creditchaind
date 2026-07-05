const wallet = requireWallet();

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

setInterval(render, 10000);
