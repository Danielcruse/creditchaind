function logout() {

    localStorage.removeItem(
        "loggedIn"
    );

    location.href =
        "login.html";
}

function resetWallet() {

    if (
        confirm(
            "Reset wallet?"
        )
    ) {

        localStorage.removeItem(
            "wallet"
        );

        localStorage.removeItem(
            "loans"
        );

        localStorage.removeItem(
            "proposals"
        );

        alert(
            "Wallet reset."
        );

        location.reload();
    }
}