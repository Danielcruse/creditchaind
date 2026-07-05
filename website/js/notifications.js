const wallet = requireWallet();

async function loadNotifications() {
    if (!wallet) return;

    const div = document.getElementById("notifications");

    try {
        const history = await apiGet(`/credit/history?address=${wallet.address}`);

        div.innerHTML = "";

        if (!history || history.length === 0) {
            div.innerHTML = "No notifications.";
            return;
        }

        history
            .slice()
            .reverse()
            .forEach((event) => {
                div.innerHTML += `<p>${event.Type}: ${event.Amount} CCT</p>`;
            });
    } catch (e) {
        console.error(e);
        div.innerHTML = "No notifications.";
    }
}

loadNotifications();

setInterval(loadNotifications, 10000);
