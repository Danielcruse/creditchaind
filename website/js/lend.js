const lendWallet = requireWallet();

function formatOfferDuration(durationSeconds) {
    if (!durationSeconds) return "no due date";
    const days = durationSeconds / 86400;
    return `${days % 1 === 0 ? days : days.toFixed(1)}d`;
}

// lendPostingApproved gates ONLY the "post a lend offer" form -
// browsing the marketplace and negotiating as a borrower needs no KYC
// at all (see api/loan_offers.go's HandleCreateLoanOffer, the one
// remaining gated handler).
let lendPostingApproved = false;

// checkKYCAccess re-derives posting access from the wallet's CURRENT
// standing on every call (see core.KYCStore.IsApproved's doc comment)
// - never assumes a previous approval still holds, since reputation
// can change after submission. Only swaps the Lend card's own
// notice/form between "need KYC" and "post an offer" - never hides
// Marketplace or Your Negotiations, which are open to everyone.
async function checkKYCAccess() {
    if (!lendWallet) return;

    try {
        const status = await apiGet(`/kyc/status?address=${lendWallet.address}`);
        lendPostingApproved = status.approved;

        const statusLine = document.getElementById("kycStatusLine");
        const notice = document.getElementById("lendKycNotice");
        const form = document.getElementById("kycForm");
        const postForm = document.getElementById("lendPostForm");

        if (status.approved) {
            notice.style.display = "none";
            postForm.style.display = "block";
        } else {
            notice.style.display = "block";
            postForm.style.display = "none";
            form.style.display = "block";
            if (!status.submitted) {
                statusLine.innerHTML = `Not submitted yet. Reputation: ${status.reputation}/${status.minReputation} required.`;
            } else {
                statusLine.innerHTML =
                    `KYC on file, but reputation ${status.reputation}/${status.minReputation} required is not yet met - ` +
                    `build reputation elsewhere (repayments, completed trades, etc.) and this unlocks automatically.`;
            }
        }
    } catch (e) {
        console.error(e);
    }
}

async function submitKYC() {
    const fullName = document.getElementById("kycFullName").value.trim();
    const dateOfBirth = document.getElementById("kycDob").value;
    const country = document.getElementById("kycCountry").value.trim();
    const agreedTerms = document.getElementById("kycAgree").checked;
    const statusEl = document.getElementById("kycSubmitStatus");

    if (!fullName) {
        statusEl.innerText = "Enter your full name.";
        return;
    }
    if (!dateOfBirth) {
        statusEl.innerText = "Enter your date of birth.";
        return;
    }
    if (!country) {
        statusEl.innerText = "Enter your country.";
        return;
    }
    if (!agreedTerms) {
        statusEl.innerText = "You must agree to the terms.";
        return;
    }

    try {
        const result = await apiPostSigned("/kyc/submit", { fullName, dateOfBirth, country, agreedTerms });
        statusEl.innerText = `Submitted - pending confirmation (tx ${result.txId}).`;
        setTimeout(checkKYCAccess, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function refreshLendBalance() {
    if (!lendWallet) return;

    try {
        const symbol = document.getElementById("offerSymbol").value;
        const balance = await fetchTokenBalance(lendWallet, symbol);
        document.getElementById("lendBalance").innerText = `${balance.balance} ${symbol}`;
    } catch (e) {
        console.error(e);
    }
}

async function createLoanOffer() {
    if (!lendWallet) return;

    const symbol = document.getElementById("offerSymbol").value;
    const ratePercent = parseInt(document.getElementById("offerRate").value, 10);
    const totalAmount = parseInt(document.getElementById("offerAmount").value, 10);
    const durationDaysRaw = document.getElementById("offerDurationDays").value;
    const statusEl = document.getElementById("lendStatus");

    if (isNaN(ratePercent) || ratePercent < 0) {
        statusEl.innerText = "Enter a valid rate.";
        return;
    }
    if (!totalAmount || totalAmount <= 0) {
        statusEl.innerText = "Enter a valid amount to lend.";
        return;
    }

    // Blank/0 means no due date - loans drawn against this offer can
    // never default (see core.LoanOffer.DurationSeconds's doc comment).
    const durationSeconds = durationDaysRaw ? Math.round(parseFloat(durationDaysRaw) * 86400) : 0;

    try {
        const result = await apiPostSigned("/loans/offers/create", { symbol, ratePercent, totalAmount, durationSeconds });
        statusEl.innerText = `Offer posted - pending confirmation (tx ${result.txId}).`;
        setTimeout(() => {
            loadMyOffers();
            loadMarketplace();
        }, 9000);
    } catch (e) {
        statusEl.innerText = e.message;
    }
}

async function closeLoanOffer(offerId) {
    try {
        await apiPostSigned("/loans/offers/close", { offerId });
        setTimeout(() => {
            loadMyOffers();
            loadMarketplace();
        }, 9000);
    } catch (e) {
        alert(e.message);
    }
}

async function loadMyOffers() {
    if (!lendWallet) return;

    const div = document.getElementById("myOfferList");

    try {
        const offers = await apiGet("/loans/offers");
        const mine = (offers || []).filter((o) => o.Lender === lendWallet.address);

        if (mine.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No offers yet.</div>`;
            return;
        }

        div.innerHTML = mine
            .map(
                (o) => `
            <div class="explorer-row">
                <span>${o.Symbol}</span>
                <span>${o.RatePercent}%</span>
                <span>${formatOfferDuration(o.DurationSeconds)}</span>
                <span>${o.RemainingAmount} / ${o.TotalAmount} remaining</span>
                <span>${o.Status}</span>
                <span>${o.Status === "OPEN" ? `<button onclick="closeLoanOffer('${o.ID}')">Close & Withdraw</button>` : ""}</span>
            </div>
        `
            )
            .join("");
    } catch (e) {
        console.error(e);
    }
}

// requestToBorrow opens a negotiation instead of drawing instantly -
// nothing is recorded on chain until the lender agrees inside the
// chat (see confirmChatNegotiation).
async function requestToBorrow(offerId) {
    if (!lendWallet) return;

    const input = document.getElementById(`draw-${offerId}`);
    const amount = parseInt(input.value, 10);

    if (!amount || amount <= 0) {
        alert("Enter a valid amount to request.");
        return;
    }

    try {
        const result = await apiPostSigned("/negotiations/start", { offerId, amount });
        await loadNegotiations();
        openChatModal(result.negotiationId);
    } catch (e) {
        alert(e.message);
    }
}

async function loadMarketplace() {
    const div = document.getElementById("marketplaceList");

    try {
        const offers = await apiGet("/loans/offers");
        const open = (offers || []).filter((o) => o.Status === "OPEN");

        if (open.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No open offers yet - be the first to post one above.</div>`;
            return;
        }

        div.innerHTML = open
            .map(
                (o) => `
            <div class="explorer-row">
                <span class="explorer-hash">${o.Lender.slice(0, 8)}...${o.Lender.slice(-6)}</span>
                <span>${o.Symbol}</span>
                <span>${o.RatePercent}%</span>
                <span>Repay within ${formatOfferDuration(o.DurationSeconds)}</span>
                <span>${o.RemainingAmount} available</span>
                <input class="draw-amount" type="number" id="draw-${o.ID}" placeholder="Amount" min="1" max="${o.RemainingAmount}">
                <button onclick="requestToBorrow('${o.ID}')">Request to Borrow</button>
            </div>
        `
            )
            .join("");
    } catch (e) {
        console.error(e);
    }
}

// --- Negotiation chat -------------------------------------------------

let currentChatNegotiationId = null;
let negotiationsCache = [];

async function loadNegotiations() {
    if (!lendWallet) return;

    const div = document.getElementById("negotiationList");

    try {
        const negotiations = await apiGet(`/negotiations?address=${lendWallet.address}`);
        negotiationsCache = negotiations || [];

        if (negotiationsCache.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No negotiations yet.</div>`;
        } else {
            div.innerHTML = negotiationsCache
                .slice()
                .sort((a, b) => b.CreatedAt - a.CreatedAt)
                .map((n) => {
                    const role = n.Lender === lendWallet.address ? "lender" : "borrower";
                    const counterparty = role === "lender" ? n.Borrower : n.Lender;
                    return `
                <div class="explorer-row">
                    <span>${n.Amount} ${n.Symbol}</span>
                    <span>as ${role}, with ${counterparty.slice(0, 8)}...${counterparty.slice(-6)}</span>
                    <span>${n.Status}</span>
                    <span><button onclick="openChatModal('${n.ID}')">Open Chat</button></span>
                </div>
            `;
                })
                .join("");
        }

        // If the chat modal is currently open, refresh its contents too.
        if (currentChatNegotiationId) {
            renderChatModal(currentChatNegotiationId);
        }
    } catch (e) {
        console.error(e);
    }
}

async function openChatModal(negotiationId) {
    currentChatNegotiationId = negotiationId;
    await loadNegotiations();
    renderChatModal(negotiationId);
    document.getElementById("chatModalOverlay").style.display = "flex";
}

function closeChatModal() {
    document.getElementById("chatModalOverlay").style.display = "none";
    currentChatNegotiationId = null;
}

async function renderChatModal(negotiationId) {
    const n = negotiationsCache.find((x) => x.ID === negotiationId);
    if (!n) return;

    const isLender = lendWallet && n.Lender === lendWallet.address;
    const receiverAddress = await balanceAddressForSymbol(lendWallet, n.Symbol);

    const moderatorLine = n.ModeratorAddress
        ? ` Moderator: ${n.ModeratorAddress.slice(0, 8)}...${n.ModeratorAddress.slice(-6)} (${n.ModerationStatus || "PENDING"}).`
        : " No moderator assigned yet (none registered).";

    document.getElementById("chatSummary").innerText =
        `${n.Amount} ${n.Symbol} @ ${n.Status} - lender: ${n.Lender.slice(0, 8)}...${n.Lender.slice(-6)}, ` +
        `borrower: ${n.Borrower.slice(0, 8)}...${n.Borrower.slice(-6)}. ` +
        (isLender
            ? `Funds will be sent to the borrower's ${n.Symbol} address.`
            : `Funds will be sent to your ${n.Symbol} address: ${receiverAddress}`) +
        moderatorLine;

    const messagesDiv = document.getElementById("chatMessages");
    messagesDiv.innerHTML = (n.Messages || [])
        .map(
            (m) => `
        <div class="chat-message">
            <div class="sender">${m.Sender.slice(0, 8)}...${m.Sender.slice(-6)}</div>
            <div>${m.Text}</div>
        </div>
    `
        )
        .join("") || `<div class="explorer-empty">No messages yet - say hello.</div>`;

    document.getElementById("chatConfirmBtn").style.display = isLender && n.Status === "OPEN" ? "inline-block" : "none";
}

async function sendChatMessage() {
    if (!currentChatNegotiationId) return;

    const input = document.getElementById("chatText");
    const text = input.value.trim();
    if (!text) return;

    try {
        await apiPostSigned("/negotiations/message", { negotiationId: currentChatNegotiationId, text });
        input.value = "";
        await loadNegotiations();
    } catch (e) {
        alert(e.message);
    }
}

async function confirmChatNegotiation() {
    if (!currentChatNegotiationId) return;

    try {
        const result = await apiPostSigned("/negotiations/confirm", { negotiationId: currentChatNegotiationId });
        alert(`Agreed - funding pending confirmation (tx ${result.txId}), takes effect in the next block.`);
        await loadNegotiations();
        setTimeout(() => {
            loadMarketplace();
            loadNegotiations();
        }, 9000);
    } catch (e) {
        alert(e.message);
    }
}

async function cancelChatNegotiation() {
    if (!currentChatNegotiationId) return;

    try {
        await apiPostSigned("/negotiations/cancel", { negotiationId: currentChatNegotiationId });
        closeChatModal();
        await loadNegotiations();
    } catch (e) {
        alert(e.message);
    }
}

document.getElementById("offerSymbol").addEventListener("change", refreshLendBalance);

checkKYCAccess();
refreshLendBalance();
loadMyOffers();
loadMarketplace();
loadNegotiations();

setInterval(() => {
    checkKYCAccess();
    refreshLendBalance();
    loadMyOffers();
    loadMarketplace();
    loadNegotiations();
}, 10000);
