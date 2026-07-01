let wallet =
    JSON.parse(
        localStorage.getItem(
            "wallet"
        )
    );

if (!wallet) {

    wallet = {

        reputation: 50,
        votingPower: 50,
        rewards: 0,
        blocksProduced: 0,
        validator: false,
        stakedEth: 0,
        cct: 0,
        transactions: []
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

function refreshValidator() {

    document.getElementById(
        "validatorStatus"
    ).innerText =
        wallet.validator
            ? "Active"
            : "Inactive";

    document.getElementById(
        "validatorReputation"
    ).innerText =
        wallet.reputation;

    document.getElementById(
        "validatorVotingPower"
    ).innerText =
        wallet.votingPower;

    document.getElementById(
        "blocksProduced"
    ).innerText =
        wallet.blocksProduced || 0;

    document.getElementById(
        "validatorRewards"
    ).innerText =
        wallet.rewards || 0;
}

function becomeValidator() {

    if (
        wallet.reputation < 50
    ) {

        alert(
            "Reputation too low."
        );

        return;
    }

    if (
        wallet.stakedEth <= 0 &&
        wallet.cct <= 0
    ) {

        alert(
            "Stake ETH or CCT first."
        );

        return;
    }

    wallet.validator = true;

    wallet.transactions.push(
        "Validator Activated"
    );

    saveWallet();

    refreshValidator();

    alert(
        "Validator Activated"
    );
}

function produceBlock() {

    if (
        !wallet.validator
    ) {

        alert(
            "Not a validator."
        );

        return;
    }

    wallet.blocksProduced =
        (wallet.blocksProduced || 0)
        + 1;

    wallet.rewards =
        (wallet.rewards || 0)
        + 10;

    wallet.cct =
        (wallet.cct || 0)
        + 10;

    wallet.transactions.push(
        "Produced Block #" +
        wallet.blocksProduced
    );

    saveWallet();

    refreshValidator();

    alert(
        "Block Produced"
    );
}

refreshValidator();

setInterval(
    refreshValidator,
    10000
);