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

    if (!fullName) { statusEl.innerText = "Enter your full name."; return; }
    if (!dateOfBirth) { statusEl.innerText = "Enter your date of birth."; return; }
    if (!country) { statusEl.innerText = "Enter your country."; return; }
    if (!agreedTerms) { statusEl.innerText = "You must agree to the terms."; return; }

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

async function loadLenderPortfolio() {
    if (!lendWallet) return;

    try {
        const [pf, balance] = await Promise.all([
            apiGet(`/lender/portfolio?address=${lendWallet.address}`),
            fetchTokenBalance(lendWallet, "YENE"),
        ]);
        document.getElementById("pfCapital").innerText = balance.balance + " YEN-e";
        document.getElementById("pfYield").innerText = pf.averageYieldPercent + "%";
        document.getElementById("pfEarned").innerText = pf.interestEarned;
        document.getElementById("pfActive").innerText = pf.activeLoans;
        document.getElementById("pfCompleted").innerText = pf.completedLoans;
        document.getElementById("pfExposure").innerText = pf.defaultExposure;
        document.getElementById("pfDiversification").innerText = pf.diversification;
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

    if (isNaN(ratePercent) || ratePercent < 0) { statusEl.innerText = "Enter a valid rate."; return; }
    if (!totalAmount || totalAmount <= 0) { statusEl.innerText = "Enter a valid amount to lend."; return; }

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

// --- Investment Marketplace / Borrower Comparison (borrower-posted
// requests, see core.LoanRequest) ------------------------------------

let requestsCache = [];
let requestsTableController = null;
const requestBorrowerReputation = {};

async function loadRequestMarketplace() {
    try {
        const all = await apiGet("/loan-requests");
        const open = (all || []).filter((r) => r.Status === "OPEN");

        await Promise.all(
            [...new Set(open.map((r) => r.Borrower))].map(async (addr) => {
                if (requestBorrowerReputation[addr] !== undefined) return;
                try {
                    requestBorrowerReputation[addr] = await apiGet(`/reputation?address=${addr}`);
                } catch (e) {
                    requestBorrowerReputation[addr] = null;
                }
            })
        );

        requestsCache = open.map((r) => ({
            ...r,
            borrowerReputation: requestBorrowerReputation[r.Borrower] ? requestBorrowerReputation[r.Borrower].score : 0,
        }));

        requestsTableController.rerender(requestsCache);
    } catch (e) {
        console.error(e);
    }
}

function renderRequestsTable(rows) {
    const tbody = document.getElementById("requestsTbody");
    if (rows.length === 0) {
        tbody.innerHTML = `<tr class="no-rows"><td colspan="7">No open borrow requests right now.</td></tr>`;
        return;
    }
    tbody.innerHTML = rows
        .map((r) => {
            const rep = requestBorrowerReputation[r.Borrower];
            const risk = riskRating({
                ratioPercent: r.CollateralSymbol === "OETH" && r.Amount > 0 ? Math.round((r.CollateralAmount / r.Amount) * 100) : 0,
                priceAvailable: r.CollateralSymbol === "OETH",
                reputation: rep ? rep.score : 50,
                amount: r.Amount,
                borrowLimit: 25000,
                durationSeconds: r.DurationSeconds,
            });
            return `
                <tr>
                    <td>${r.borrowerReputation}</td>
                    <td>${r.CollateralSymbol} (${r.CollateralAmount})</td>
                    <td>${r.Amount} ${r.RequestedSymbol}</td>
                    <td>${r.DurationSeconds > 0 ? Math.round(r.DurationSeconds / 86400) + " days" : "No due date"}</td>
                    <td>${r.MaxInterestPercent}%</td>
                    <td><span class="badge-pill ${risk.cls}">${risk.label}</span></td>
                    <td><button onclick="fundRequest('${r.ID}')">Fund</button></td>
                </tr>`;
        })
        .join("");
}

async function fundRequest(requestId) {
    if (!lendWallet) return;

    const request = requestsCache.find((r) => r.ID === requestId);
    if (!request) return;

    const rateStr = prompt(`Rate you're offering (must be at most ${request.MaxInterestPercent}%):`);
    if (!rateStr) return;
    const ratePercent = parseInt(rateStr, 10);
    if (isNaN(ratePercent) || ratePercent <= 0 || ratePercent > request.MaxInterestPercent) {
        alert("Invalid rate.");
        return;
    }

    try {
        const result = await apiPostSigned("/negotiations/start", { requestId, ratePercent });
        await loadNegotiations();
        openChatModal(result.negotiationId);
    } catch (e) {
        alert("Couldn't start negotiation: " + e.message);
    }
}

document.getElementById("offerSymbol").addEventListener("change", refreshLendBalance);

requestsTableController = makeSortableTable(
    document.getElementById("requestsThead"),
    document.getElementById("requestsTbody"),
    [],
    renderRequestsTable
);

checkKYCAccess();
refreshLendBalance();
loadLenderPortfolio();
loadMyOffers();
loadMarketplace();
loadRequestMarketplace();
loadNegotiations();

setInterval(() => {
    checkKYCAccess();
    refreshLendBalance();
    loadLenderPortfolio();
    loadMyOffers();
    loadMarketplace();
    loadRequestMarketplace();
    loadNegotiations();
}, 10000);
