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
            100,

        stakedEth:
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

    saveWallet();
}

function saveWallet() {

    localStorage.setItem(
        "wallet",
        JSON.stringify(
            wallet
        )
    );
}

function refresh() {

    document.getElementById(
        "stakedEth"
    ).innerText =
        wallet.stakedEth || 0;

    document.getElementById(
        "oethBalance"
    ).innerText =
        wallet.oeth || 0;

    document.getElementById(
        "rewardBalance"
    ).innerText =
        wallet.rewards || 0;
}

function stakeETH() {

    let amount =
        parseFloat(
            document
                .getElementById(
                    "stakeAmount"
                )
                .value
        );

    if (
        isNaN(amount) ||
        amount <= 0
    ) {
        alert(
            "Invalid amount."
        );
        return;
    }

    if (
        amount >
        wallet.eth
    ) {
        alert(
            "Insufficient ETH."
        );
        return;
    }

    wallet.eth -= amount;

    wallet.stakedEth =
        (wallet.stakedEth || 0)
        + amount;

    wallet.oeth =
        (wallet.oeth || 0)
        + amount;

    wallet.reputation += 5;

    wallet.votingPower += 5;

    wallet.transactions.push(
        "Staked " +
        amount +
        " ETH"
    );

    saveWallet();

    refresh();

    alert(
        "ETH Staked Successfully"
    );
}

function unstakeETH() {

    let amount =
        parseFloat(
            document
                .getElementById(
                    "unstakeAmount"
                )
                .value
        );

    if (
        isNaN(amount) ||
        amount <= 0
    ) {
        alert(
            "Invalid amount."
        );
        return;
    }

    if (
        amount >
        wallet.stakedEth
    ) {
        alert(
            "Not enough staked ETH."
        );
        return;
    }

    wallet.stakedEth -= amount;

    wallet.oeth -= amount;

    wallet.eth += amount;

    wallet.transactions.push(
        "Unstaked " +
        amount +
        " ETH"
    );

    saveWallet();

    refresh();

    alert(
        "Unstake Completed"
    );
}

function claimRewards() {

    let reward =
        (wallet.stakedEth || 0)
        * 0.05;

    reward =
        Math.floor(
            reward
        );

    if (
        reward <= 0
    ) {
        alert(
            "No rewards yet."
        );
        return;
    }

    wallet.cct += reward;

    wallet.rewards += reward;

    wallet.transactions.push(
        "Claimed " +
        reward +
        " CCT"
    );

    saveWallet();

    refresh();

    alert(
        reward +
        " CCT claimed."
    );
}

refresh();

setInterval(
    refresh,
    10000
);