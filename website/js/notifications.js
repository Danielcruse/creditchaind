let wallet =
    JSON.parse(
        localStorage.getItem(
            "wallet"
        )
    );

function loadNotifications() {

    let div =
        document.getElementById(
            "notifications"
        );

    div.innerHTML = "";

    if (
        !wallet ||
        !wallet.transactions
    ) {

        div.innerHTML =
            "No notifications.";

        return;
    }

    wallet.transactions
        .slice()
        .reverse()
        .forEach(tx => {

            div.innerHTML +=
                `<p>${tx}</p>`;
        });
}

loadNotifications();

setInterval(
    loadNotifications,
    10000
);