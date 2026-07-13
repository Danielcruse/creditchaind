const wallet = requireWallet();

const NOTIFICATION_ICONS = {
    TRADE_STARTED: "\u{1F91D}",
    NEW_MESSAGE: "\u{1F4AC}",
    LOGIN: "\u{1F510}",
    REPT_MINTED: "\u{1F3C6}",
};

function notificationIcon(type) {
    return NOTIFICATION_ICONS[type] || "\u{1F514}";
}

function renderNotificationRow(n) {
    const time = n.CreatedAt ? new Date(n.CreatedAt * 1000).toLocaleString() : "-";
    const unreadStyle = n.Read ? "" : "font-weight:600;";
    return `
        <div style="display:flex;gap:10px;align-items:flex-start;cursor:pointer;padding:8px 0;border-bottom:1px solid rgba(255,255,255,0.08);${unreadStyle}" data-id="${n.ID}" onclick="markNotificationRead('${n.ID}')">
            <span>${notificationIcon(n.Type)}</span>
            <span style="flex:1;">${n.Message}</span>
            <span style="opacity:0.6;font-size:12px;white-space:nowrap;">${time}</span>
        </div>
    `;
}

async function loadNotifications() {
    if (!wallet) return;

    const div = document.getElementById("notifications");

    try {
        const result = await apiGet(`/notifications?address=${wallet.address}`);
        const list = result.notifications || [];

        if (list.length === 0) {
            div.innerHTML = "No notifications.";
            return;
        }

        div.innerHTML = list.map(renderNotificationRow).join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = "No notifications.";
    }
}

async function markNotificationRead(id) {
    try {
        await apiPostSigned("/notifications/read", { notificationId: id });
        loadNotifications();
    } catch (e) {
        console.error(e);
    }
}

loadNotifications();

setInterval(loadNotifications, 10000);
