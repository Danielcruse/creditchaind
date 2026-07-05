const wallet = requireWallet();

async function refreshCollateral() {
    if (!wallet) return;

    try {
        const [collateral, staking, usdcTestBalance] = await Promise.all([
            apiGet(`/collateral?address=${wallet.address}`),
            apiGet(`/staking?address=${wallet.address}`),
            fetchTokenBalance(wallet, "USDCTEST"),
        ]);

        const stakedCct = (staking.positions || []).find((p) => p.symbol === "CCT");
        document.getElementById("stakedCct").innerText = stakedCct ? stakedCct.staked : 0;
        document.getElementById("oethBalance").innerText = staking.oethBalance || 0;
        document.getElementById("lockedCollateralCct").innerText = collateral.lockedCCT;
        document.getElementById("lockedCollateralOeth").innerText = collateral.lockedOETH;
        document.getElementById("usdcTestBalance").innerText = usdcTestBalance.balance;
    } catch (e) {
        console.error(e);
    }

    refreshLendBalance();
}

async function refreshLendBalance() {
    if (!wallet) return;

    try {
        const symbol = document.getElementById("offerSymbol").value;
        const balance = await fetchTokenBalance(wallet, symbol);
        document.getElementById("lendBalance").innerText = `${balance.balance} ${symbol}`;
    } catch (e) {
        console.error(e);
    }
}

async function lockCollateral() {
    if (!wallet) return;

    const amount = parseFloat(document.getElementById("lockAmount").value);
    const symbol = document.getElementById("lockSymbol").value;

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/collateral/lock", {
            amount,
            symbol,
        });

        if (result.success === false) {
            alert(`Insufficient staked ${symbol === "CCT" ? "CCT" : "oETH"} balance to lock that much collateral.`);
            return;
        }

        document.getElementById("pendingStatus").innerText =
            `Collateral lock pending (tx ${result.txId}) - confirms in the next block.`;
        setTimeout(refreshCollateral, 9000);
    } catch (e) {
        alert("Lock failed: " + e.message);
    }
}

async function borrow() {
    if (!wallet) return;

    const amount = parseFloat(document.getElementById("borrowAmount").value);

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/borrow", {
            Amount: amount,
        });

        document.getElementById("pendingStatus").innerText =
            `Pending (tx ${result.txId}) - loan appears below once the next block confirms it.`;
    } catch (e) {
        alert("Borrow failed: " + e.message);
    }
}

// repayAmountFor mirrors api/apply.go's repayAmountFor - principal
// plus interest (a whole-percent rate), the real amount repaying
// actually costs, not just the bare principal.
function repayAmountFor(loan) {
    return loan.Amount + Math.floor((loan.Amount * loan.Interest) / 100);
}

async function repay(loanID, total, symbol) {
    if (!confirm(`Repay ${total} ${symbol} (principal + interest)? ${total > 0 ? "This will be deducted from your balance." : ""}`)) {
        return;
    }

    try {
        const result = await apiPostSigned("/repay", { LoanID: loanID });
        document.getElementById("pendingStatus").innerText =
            `Repay pending (tx ${result.txId}) - confirms in the next block.`;
    } catch (e) {
        alert("Repay failed: " + e.message);
    }
}

async function render() {
    if (!wallet) return;

    const div = document.getElementById("loanList");

    try {
        const loans = await apiGet("/loans");
        const mine = Object.values(loans).filter((loan) => loan.Borrower === wallet.address);

        div.innerHTML = "";

        if (mine.length === 0) {
            div.innerHTML = "No Loans";
            return;
        }

        mine.forEach((loan) => {
            const symbol = loan.Symbol || "USDCTEST";
            const total = repayAmountFor(loan);
            const lenderLabel = loan.Lender
                ? `lender: ${loan.Lender.slice(0, 8)}...${loan.Lender.slice(-6)} @ ${loan.Interest}%`
                : "self-funded";
            div.innerHTML += `
                <p>
                    Loan ${loan.ID}: ${loan.Amount} ${symbol} | ${loan.Status} | ${lenderLabel}
                    ${loan.Status === "FUNDED" ? `| repay ${total} ${symbol} total` : ""}
                    ${loan.Status === "PENDING" || loan.Status === "FUNDED"
                        ? `<button onclick="repay('${loan.ID}', ${total}, '${symbol}')">Repay ${total} ${symbol}</button>`
                        : ""}
                </p>
            `;
        });
    } catch (e) {
        console.error(e);
        div.innerHTML = "No Loans";
    }
}

async function createLoanOffer() {
    if (!wallet) return;

    const symbol = document.getElementById("offerSymbol").value;
    const ratePercent = parseInt(document.getElementById("offerRate").value, 10);
    const totalAmount = parseInt(document.getElementById("offerAmount").value, 10);
    const statusEl = document.getElementById("lendStatus");

    if (isNaN(ratePercent) || ratePercent < 0) {
        statusEl.innerText = "Enter a valid rate.";
        return;
    }
    if (!totalAmount || totalAmount <= 0) {
        statusEl.innerText = "Enter a valid amount to lend.";
        return;
    }

    try {
        const result = await apiPostSigned("/loans/offers/create", { symbol, ratePercent, totalAmount });
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
    if (!wallet) return;

    const div = document.getElementById("myOfferList");

    try {
        const offers = await apiGet("/loans/offers");
        const mine = (offers || []).filter((o) => o.Lender === wallet.address);

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
    if (!wallet) return;

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
    if (!wallet) return;

    const div = document.getElementById("negotiationList");

    try {
        const negotiations = await apiGet(`/negotiations?address=${wallet.address}`);
        negotiationsCache = negotiations || [];

        if (negotiationsCache.length === 0) {
            div.innerHTML = `<div class="explorer-empty">No negotiations yet.</div>`;
        } else {
            div.innerHTML = negotiationsCache
                .slice()
                .sort((a, b) => b.CreatedAt - a.CreatedAt)
                .map((n) => {
                    const role = n.Lender === wallet.address ? "lender" : "borrower";
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

    const isLender = wallet && n.Lender === wallet.address;
    const receiverAddress = await balanceAddressForSymbol(wallet, n.Symbol);

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
            render();
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

refreshCollateral();
render();
loadMyOffers();
loadMarketplace();
loadNegotiations();

setInterval(() => {
    refreshCollateral();
    render();
    loadMyOffers();
    loadMarketplace();
    loadNegotiations();
}, 10000);
