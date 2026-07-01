let wallet =
    JSON.parse(
        localStorage.getItem(
            "wallet"
        )
    );

if (!wallet) {

    wallet = {

        address:
            "CCT-DEMO",

        reputation:
            50,

        votingPower:
            50,

        creditScore:
            50,

        eth:
            0,

        oeth:
            0,

        cct:
            0,

        rewards:
            0,

        transactions:
            []
    };

    localStorage.setItem(
        "wallet",
        JSON.stringify(
            wallet
        )
    );
}

function loadWallet() {

    document.getElementById(
        "walletAddress"
    ).innerText =
        wallet.address;

    document.getElementById(
        "reputation"
    ).innerText =
        wallet.reputation;

    document.getElementById(
        "votingPower"
    ).innerText =
        wallet.votingPower;

    document.getElementById(
        "creditScore"
    ).innerText =
        wallet.creditScore;

    document.getElementById(
        "ethBalance"
    ).innerText =
        wallet.eth;

    document.getElementById(
        "oethBalance"
    ).innerText =
        wallet.oeth;

    document.getElementById(
        "cctBalance"
    ).innerText =
        wallet.cct;

    document.getElementById(
        "rewardBalance"
    ).innerText =
        wallet.rewards;

    let txDiv =
        document.getElementById(
            "transactions"
        );

    txDiv.innerHTML = "";

    wallet.transactions
        .slice()
        .reverse()
        .forEach(tx => {

            txDiv.innerHTML +=
                `
                <p>
                ${tx}
                </p>
                `;
        });
}

loadWallet();

setInterval(
    loadWallet,
    10000
);