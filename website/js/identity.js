let wallet =
    JSON.parse(
        localStorage.getItem(
            "wallet"
        )
    );

if (wallet) {

    document.getElementById(
        "address"
    ).innerText =
        wallet.address;

    document.getElementById(
        "rep"
    ).innerText =
        wallet.reputation;

    document.getElementById(
        "credit"
    ).innerText =
        wallet.creditScore;

    document.getElementById(
        "vote"
    ).innerText =
        wallet.votingPower;
}