let proposals =
    JSON.parse(
        localStorage.getItem(
            "proposals"
        )
    ) || [];

let wallet =
    JSON.parse(
        localStorage.getItem(
            "wallet"
        )
    );

function saveProposals() {

    localStorage.setItem(
        "proposals",
        JSON.stringify(
            proposals
        )
    );
}

function createProposal() {

    let title =
        document.getElementById(
            "proposalTitle"
        ).value;

    let description =
        document.getElementById(
            "proposalDescription"
        ).value;

    if (
        title === ""
    ) {
        alert(
            "Title required."
        );
        return;
    }

    proposals.push({

        id:
            Date.now(),

        title:
            title,

        description:
            description,

        yes:
            0,

        no:
            0
    });

    saveProposals();

    render();

    alert(
        "Proposal Created"
    );
}

function voteYes(id) {

    let p =
        proposals.find(
            x =>
                x.id === id
        );

    p.yes +=
        wallet.votingPower;

    saveProposals();

    render();
}

function voteNo(id) {

    let p =
        proposals.find(
            x =>
                x.id === id
        );

    p.no +=
        wallet.votingPower;

    saveProposals();

    render();
}

function render() {

    let div =
        document.getElementById(
            "proposalList"
        );

    div.innerHTML = "";

    if (
        proposals.length === 0
    ) {

        div.innerHTML =
            "No proposals yet.";

        return;
    }

    proposals.forEach(
        p => {

            div.innerHTML +=
                `
                <div class="card">

                <h3>${p.title}</h3>

                <p>
                ${p.description}
                </p>

                <p>
                YES:
                ${p.yes}
                </p>

                <p>
                NO:
                ${p.no}
                </p>

                <button
                onclick="
                voteYes(${p.id})
                ">
                Vote Yes
                </button>

                <button
                onclick="
                voteNo(${p.id})
                ">
                Vote No
                </button>

                </div>

                <br>
                `;
        }
    );
}

render();