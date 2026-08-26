// negotiation-chat.js - the negotiation chat modal, shared between
// borrow.html (borrower accepting a lender-posted offer) and
// lend.html (lender funding a borrower-posted request, or a lender
// funding an offer-draw via the older path) - both pages now host
// both marketplace directions, so both need this. Extracted from what
// used to be lend.js-only negotiation code so it isn't duplicated
// across two pages. `apiPostSigned` already reads the one browser
// wallet itself (see api.js) - this file only needs read access to
// its address for rendering (whose message is whose, am I the
// lender), via requireWallet() directly, same as api.js does.
// Requires the including page to include the #chatModalOverlay
// markup (see lend.html) and to have already loaded api.js.

let negotiationsCache = [];

async function loadNegotiations() {
    const wallet = requireWallet();
    if (!wallet) return;
    try {
        const list = await apiGet(`/negotiations?address=${wallet.address}`);
        negotiationsCache = (list || []).slice().sort((a, b) => b.CreatedAt - a.CreatedAt);
    } catch (e) {
        console.error(e);
        negotiationsCache = [];
    }
    renderNegotiationList();
}

function renderNegotiationList() {
    const el = document.getElementById("negotiationList");
    if (!el) return;

    const wallet = requireWallet();
    if (!wallet) return;

    if (negotiationsCache.length === 0) {
        el.innerHTML = `<p class="explorer-empty">No negotiations yet.</p>`;
        return;
    }

    el.innerHTML = negotiationsCache
        .map((n) => {
            const role = n.Lender === wallet.address ? "lender" : "borrower";
            const other = role === "lender" ? n.Borrower : n.Lender;
            return `
                <div class="explorer-row">
                    <span>${n.Amount} ${n.Symbol} <span style="opacity:.6;">(${role}, with ${formatAddress ? formatAddress(other) : other.slice(0, 10)})</span></span>
                    <span>${n.Status}</span>
                    <button onclick="openChatModal('${n.ID}')">Open Chat</button>
                </div>
            `;
        })
        .join("");
}

async function openChatModal(id) {
    document.getElementById("chatModalOverlay").style.display = "flex";
    await renderChatModal(id);
}

function closeChatModal() {
    document.getElementById("chatModalOverlay").style.display = "none";
}

async function renderChatModal(id) {
    const wallet = requireWallet();
    if (!wallet) return;
    const n = negotiationsCache.find((x) => x.ID === id) || (await apiGet(`/negotiations?address=${wallet.address}`)).find((x) => x.ID === id);
    if (!n) return;

    document.getElementById("chatModalOverlay").dataset.negotiationId = id;

    const isLender = n.Lender === wallet.address;
    const kind = n.RequestID ? "borrower request" : "lender offer";
    const rateNote = n.RequestID ? ` at ${n.RatePercent}%` : "";
    document.getElementById("chatSummary").innerText =
        `${n.Amount} ${n.Symbol}${rateNote} - ${kind} - status: ${n.Status}` +
        (n.ModeratorAddress ? ` - moderator assigned (${n.ModerationStatus})` : " - no moderator assigned yet");

    document.getElementById("chatMessages").innerHTML = (n.Messages || [])
        .map((m) => {
            const mine = m.Sender === wallet.address;
            return `<div class="chat-message ${mine ? "mine" : "theirs"}"><span class="sender">${mine ? "You" : escapeHtml(m.Sender.slice(0, 10))}</span>${escapeHtmlMultiline(m.Text)}</div>`;
        })
        .join("");

    const confirmBtn = document.getElementById("chatConfirmBtn");
    if (confirmBtn) {
        confirmBtn.style.display = isLender && n.Status === "OPEN" ? "inline-block" : "none";
    }
}

async function sendChatMessage() {
    const id = document.getElementById("chatModalOverlay").dataset.negotiationId;
    const text = document.getElementById("chatText").value.trim();
    if (!text) return;

    try {
        await apiPostSigned("/negotiations/message", { negotiationId: id, text });
        document.getElementById("chatText").value = "";
        await renderChatModal(id);
    } catch (e) {
        alert("Couldn't send message: " + e.message);
    }
}

async function confirmChatNegotiation() {
    const id = document.getElementById("chatModalOverlay").dataset.negotiationId;
    if (!confirm("Confirm and fund this loan now? This submits a real transaction.")) return;

    try {
        const result = await apiPostSigned("/negotiations/confirm", { negotiationId: id });
        alert("Funding submitted (tx " + result.txId + "). It will confirm in the next block.");
        closeChatModal();
        setTimeout(() => {
            if (typeof loadMarketplace === "function") loadMarketplace();
            if (typeof loadRequestMarketplace === "function") loadRequestMarketplace();
            loadNegotiations();
        }, 9000);
    } catch (e) {
        alert("Couldn't confirm: " + e.message);
    }
}

async function cancelChatNegotiation() {
    const id = document.getElementById("chatModalOverlay").dataset.negotiationId;
    if (!confirm("Cancel this negotiation?")) return;

    try {
        await apiPostSigned("/negotiations/cancel", { negotiationId: id });
        closeChatModal();
        loadNegotiations();
    } catch (e) {
        alert("Couldn't cancel: " + e.message);
    }
}
