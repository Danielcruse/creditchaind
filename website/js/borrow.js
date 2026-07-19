const borrowWallet = requireWallet();

// toggleCollapsible expands/collapses a section's content on click of
// its header (icon, name, or anywhere on the row) - Collateral and
// Instant Self-Funded Borrow both start collapsed (see borrow.html),
// revealed only on demand instead of always taking up page space.
function toggleCollapsible(contentId, chevronId) {
    const content = document.getElementById(contentId);
    const chevron = document.getElementById(chevronId);
    const isOpen = content.classList.toggle("open");
    if (chevron) chevron.classList.toggle("open", isOpen);
}

async function refreshCollateral() {
    if (!borrowWallet) return;

    try {
        const [collateral, staking, yeneBalance] = await Promise.all([
            apiGet(`/collateral?address=${borrowWallet.address}`),
            apiGet(`/staking?address=${borrowWallet.address}`),
            fetchTokenBalance(borrowWallet, "YENE"),
        ]);

        const stakedCct = (staking.positions || []).find((p) => p.symbol === "CCT");
        document.getElementById("stakedCct").innerText = stakedCct ? stakedCct.staked : 0;
        document.getElementById("oethBalance").innerText = staking.oethBalance || 0;
        document.getElementById("lockedCollateralCct").innerText = collateral.lockedCCT;
        document.getElementById("lockedCollateralOeth").innerText = collateral.lockedOETH;
        document.getElementById("yeneBalance").innerText = yeneBalance.balance;

        // Quick at-a-glance figures on the collapsed headers, so
        // there's something worth reading even before you click.
        document.getElementById("collateralSummary").innerText =
            `${collateral.lockedCCT} CCT + ${collateral.lockedOETH} oETH locked`;
        document.getElementById("instantBorrowSummary").innerText =
            `${yeneBalance.balance} YEN-e available`;
    } catch (e) {
        console.error(e);
    }
}

async function loadProtocolStats() {
    try {
        const s = await apiGet("/protocol/stats");
        document.getElementById("statLiquidity").innerText = s.totalLiquidity;
        document.getElementById("statBorrowed").innerText = s.totalBorrowed;
        document.getElementById("statActive").innerText = s.activeLoans;
        document.getElementById("statInterest").innerText = s.averageInterestPercent + "%";
        document.getElementById("statDefault").innerText = s.defaultRatePercent + "%";
        document.getElementById("statReputation").innerText = s.averageReputation;
        document.getElementById("statValidators").innerText = s.activeValidators;
        document.getElementById("statStaked").innerText = s.totalStaked;
    } catch (e) {
        console.error(e);
    }
}

let latestReputation = null;
let latestBorrowingPower = null;

async function loadBorrowingPower() {
    if (!borrowWallet) return;

    try {
        const [bp, rep] = await Promise.all([
            apiGet(`/borrowing-power?address=${borrowWallet.address}`),
            apiGet(`/reputation?address=${borrowWallet.address}`),
        ]);
        latestReputation = rep;
        latestBorrowingPower = bp;

        document.getElementById("bpScore").innerText = bp.score + "/100";
        document.getElementById("bpTier").innerText = bp.tier;
        document.getElementById("bpMaxBorrow").innerText = bp.maxBorrow + " YEN-e";
        document.getElementById("bpDiscount").innerText = "-" + bp.interestDiscountPercent + "%";
        document.getElementById("factorRep").innerText = bp.reputationFactor;
        document.getElementById("factorRepay").innerText = bp.repaymentFactor;
        document.getElementById("factorStake").innerText = bp.stakingFactor;
        document.getElementById("factorCollateral").innerText = bp.collateralFactor;
        document.getElementById("factorGov").innerText = bp.governanceFactor;

        const totalLoans = rep.loansRepaid + rep.loansDefault;
        document.getElementById("bpHistory").innerText = `${totalLoans} loans (${rep.loansRepaid} repaid)`;
        document.getElementById("bpDefaultRate").innerText =
            totalLoans > 0 ? Math.round((rep.loansDefault / totalLoans) * 100) + "%" : "no history yet";
    } catch (e) {
        console.error(e);
    }
}

async function lockCollateral() {
    if (!borrowWallet) return;

    const amount = parseFloat(document.getElementById("lockAmount").value);
    const symbol = document.getElementById("lockSymbol").value;

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/collateral/lock", { amount, symbol });

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
    if (!borrowWallet) return;

    const amount = parseFloat(document.getElementById("borrowAmount").value);

    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/borrow", { Amount: amount });

        document.getElementById("pendingStatus").innerText =
            `Pending (tx ${result.txId}) - loan appears below once the next block confirms it.`;
    } catch (e) {
        alert("Borrow failed: " + e.message);
    }
}

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

async function addCollateral(loanID) {
    const amountStr = prompt("How much locked oETH collateral would you like to add to this loan? (Lock more above first if you need to.)");
    if (!amountStr) return;

    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/loans/add-collateral", { loanId: loanID, amount });
        document.getElementById("pendingStatus").innerText =
            `Add-collateral pending (tx ${result.txId}) - confirms in the next block.`;
        setTimeout(() => {
            refreshCollateral();
            render();
        }, 9000);
    } catch (e) {
        alert("Add collateral failed: " + e.message);
    }
}

async function redeemShortfall(loanID, shortfall, symbol) {
    if (!confirm(`Repay the outstanding ${shortfall} ${symbol} shortfall now to keep your Mining Power?`)) {
        return;
    }

    try {
        const result = await apiPostSigned("/loans/redeem-shortfall", { loanId: loanID });
        document.getElementById("pendingStatus").innerText =
            `Redemption pending (tx ${result.txId}) - confirms in the next block.`;
        setTimeout(render, 9000);
    } catch (e) {
        alert("Redeem failed: " + e.message);
    }
}

// riskStageLabelHtml turns the extended /loans/health response's
// RiskStage/RiskStageDeadline/LiquidatedShortfall fields (see the
// Default Protection card above this list) into a short inline badge -
// purely presentational, all values come straight from the backend
// state machine (api/apply.go's advanceLoanLifecycle), nothing computed
// client-side.
function riskStageLabelHtml(health, loan) {
    if (!health || !health.riskStage) return "";

    const secondsLeft = health.riskStageDeadline ? health.riskStageDeadline - Math.floor(Date.now() / 1000) : 0;
    const deadlineText = health.riskStageDeadline
        ? secondsLeft > 0
            ? ` (${Math.ceil(secondsLeft / 3600)}h left)`
            : " (expired - next block resolves it)"
        : "";

    const labels = {
        WARNING: "Warning",
        MARGIN_CALL: "Margin Call",
        GRACE_PERIOD: "Grace Period",
        AWAITING_FINAL_REDEMPTION: "Final Redemption Window",
        DEFAULTED: "Defaulted",
    };
    const label = labels[health.riskStage] || health.riskStage;
    const badgeCls = health.riskStage === "DEFAULTED" ? "health-liquidation" : "health-warning";
    const badge = ` <span class="badge-pill ${badgeCls}">${label}${deadlineText}</span>`;

    if (health.riskStage === "AWAITING_FINAL_REDEMPTION" && health.liquidatedShortfall > 0) {
        const symbol = loan.Symbol || "YENE";
        return `${badge} <button onclick="redeemShortfall('${loan.ID}', ${health.liquidatedShortfall}, '${symbol}')">Repay ${health.liquidatedShortfall} ${symbol} to keep Mining Power</button>`;
    }
    return badge;
}

// loanTimelineHtml renders the small horizontal step strip - purely
// derived from real fields already on the loan (Status/DueAt/CreatedAt).
function loanTimelineHtml(loan) {
    const steps = ["Borrow", "Funded", "Repayment Window"];
    const overdue = loan.Status === "FUNDED" && loan.DueAt > 0 && loan.DueAt * 1000 < Date.now();
    if (loan.Status === "DEFAULT") {
        steps.push("Default", "Liquidation");
    } else {
        steps.push("Repaid");
    }

    return `<div class="loan-timeline">${steps
        .map((s) => {
            let cls = "";
            if (s === "Borrow" || s === "Funded") cls = "done";
            else if (s === "Repayment Window" && loan.Status === "FUNDED") cls = overdue ? "danger" : "active";
            else if (s === "Repaid" && loan.Status === "REPAID") cls = "done";
            else if ((s === "Default" || s === "Liquidation") && loan.Status === "DEFAULT") cls = "danger";
            return `<div class="timeline-step ${cls}">${s}</div>`;
        })
        .join("")}</div>`;
}

async function render() {
    if (!borrowWallet) return;

    const div = document.getElementById("loanList");

    try {
        const loans = await apiGet("/loans");
        const mine = Object.values(loans).filter((loan) => loan.Borrower === borrowWallet.address);

        div.innerHTML = "";

        if (mine.length === 0) {
            div.innerHTML = "No Loans";
            return;
        }

        // Any FUNDED peer-funded loan can be in the Default Protection
        // ladder now, not just OETH-collateralized ones - a CCT-collateralized
        // or self-funded... no, self-funded (Lender=="") never enters
        // the ladder (see api/apply.go's processLoanDefaults), but an
        // overdue CCT-collateralized peer-funded loan does, via the
        // due-date path. /loans/health returns RiskStage regardless of
        // whether a live price exists for the collateral symbol.
        const healthByLoan = {};
        await Promise.all(
            mine
                .filter((loan) => loan.Status === "FUNDED" && loan.Lender)
                .map(async (loan) => {
                    try {
                        healthByLoan[loan.ID] = await apiGet(`/loans/health?loanId=${loan.ID}`);
                    } catch (e) {
                        console.error(e);
                    }
                })
        );

        mine.forEach((loan) => {
            const symbol = loan.Symbol || "YENE";
            const total = repayAmountFor(loan);
            const priceAdjustedNote = !loan.Lender && loan.CollateralSymbol === "OETH" ? " (price-adjusted)" : "";
            const lenderLabel = loan.Lender
                ? `lender: ${loan.Lender.slice(0, 8)}...${loan.Lender.slice(-6)} @ ${loan.Interest}%`
                : `self-funded @ ${loan.Interest}%${priceAdjustedNote}`;

            let dueLabel = "";
            if (loan.Status === "FUNDED" && loan.DueAt > 0) {
                const msRemaining = loan.DueAt * 1000 - Date.now();
                dueLabel =
                    msRemaining > 0
                        ? ` | due ${new Date(loan.DueAt * 1000).toLocaleDateString()} (${Math.ceil(msRemaining / 86400000)}d left)`
                        : ` | <span style="color:#ff6b6b;">OVERDUE - defaults soon</span>`;
            }
            const defaultWarning =
                loan.Status === "DEFAULT"
                    ? ` | <span style="color:#ff6b6b;">Defaulted - collateral and reputation forfeited to the lender</span>`
                    : "";

            const health = healthByLoan[loan.ID];
            const tier = healthTier(health ? health.ratioPercent : 0, !!(health && health.priceAvailable));
            const healthBadge = health
                ? ` <span class="badge-pill ${tier.cls}">${tier.label}${health.priceAvailable ? " (" + health.ratioPercent + "% of owed)" : ""}</span>`
                : "";
            const riskStageHtml = riskStageLabelHtml(health, loan);

            div.innerHTML += `
                <p>
                    Loan ${loan.ID}: ${loan.Amount} ${symbol} | ${loan.Status} | ${lenderLabel}${dueLabel}${defaultWarning}${healthBadge}${riskStageHtml}
                    ${loan.Status === "FUNDED" ? `| repay ${total} ${symbol} total` : ""}
                    ${loan.Status === "PENDING" || loan.Status === "FUNDED"
                        ? `<button onclick="repay('${loan.ID}', ${total}, '${symbol}')">Repay ${total} ${symbol}</button>`
                        : ""}
                    ${health && health.atRisk
                        ? `<button onclick="addCollateral('${loan.ID}')">Add Collateral</button>`
                        : ""}
                </p>
                ${loanTimelineHtml(loan)}
            `;
        });
    } catch (e) {
        console.error(e);
        div.innerHTML = "No Loans";
    }
}

// --- Live Loan Offers (lender-posted, sortable) ---------------------

let offersCache = [];
let offersTableController = null;
const offerLenderReputation = {};

async function loadLoanOffers() {
    try {
        const all = await apiGet("/loans/offers");
        offersCache = (all || []).filter((o) => o.Status === "OPEN");

        await Promise.all(
            [...new Set(offersCache.map((o) => o.Lender))].map(async (addr) => {
                if (offerLenderReputation[addr] !== undefined) return;
                try {
                    const rep = await apiGet(`/reputation?address=${addr}`);
                    offerLenderReputation[addr] = rep.score;
                } catch (e) {
                    offerLenderReputation[addr] = "-";
                }
            })
        );

        offersTableController.rerender(offersCache);
    } catch (e) {
        console.error(e);
    }
}

function renderOffersTable(rows) {
    const tbody = document.getElementById("offersTbody");
    if (rows.length === 0) {
        tbody.innerHTML = `<tr class="no-rows"><td colspan="6">No open offers right now.</td></tr>`;
        return;
    }
    tbody.innerHTML = rows
        .map(
            (o) => `
        <tr>
            <td>${o.RatePercent}%</td>
            <td>${o.DurationSeconds > 0 ? Math.round(o.DurationSeconds / 86400) + " days" : "No due date"}</td>
            <td>${o.RemainingAmount}</td>
            <td>${offerLenderReputation[o.Lender] !== undefined ? offerLenderReputation[o.Lender] : "..."}</td>
            <td>${o.Symbol}</td>
            <td><button onclick="acceptOffer('${o.ID}')">Accept</button></td>
        </tr>`
        )
        .join("");
}

async function acceptOffer(offerId) {
    const offer = offersCache.find((o) => o.ID === offerId);
    if (!offer) return;

    const amountStr = prompt(`How much would you like to draw against this offer (up to ${offer.RemainingAmount} ${offer.Symbol})?`);
    if (!amountStr) return;
    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        alert("Invalid amount.");
        return;
    }

    try {
        const result = await apiPostSigned("/negotiations/start", { offerId, amount });
        await loadNegotiations();
        openChatModal(result.negotiationId);
    } catch (e) {
        alert("Couldn't start negotiation: " + e.message);
    }
}

// --- Create a Borrow Request + live calculator -----------------------

function updateRequestCalculator() {
    const amount = parseFloat(document.getElementById("reqAmount").value) || 0;
    const collateralAmount = parseFloat(document.getElementById("reqCollateralAmount").value) || 0;
    const collateralSymbol = document.getElementById("reqCollateralSymbol").value;
    const durationDays = parseFloat(document.getElementById("reqDurationDays").value) || 0;
    const maxInterest = parseFloat(document.getElementById("reqMaxInterest").value) || 0;

    if (amount <= 0) {
        document.getElementById("calcRisk").innerText = "-";
        document.getElementById("calcLiquidation").innerText = "-";
        document.getElementById("calcMonthly").innerText = "-";
        return;
    }

    const reputation = latestReputation ? latestReputation.score : 50;
    const ratioPercent = collateralSymbol === "OETH" && amount > 0 ? Math.round((collateralAmount / amount) * 100) : 0;
    const priceAvailable = collateralSymbol === "OETH" && collateralAmount > 0;

    const risk = riskRating({
        ratioPercent,
        priceAvailable,
        reputation,
        amount,
        borrowLimit: latestBorrowingPower ? latestBorrowingPower.maxBorrow : 10000,
        durationSeconds: durationDays * 86400,
    });
    document.getElementById("calcRisk").innerHTML = `<span class="badge-pill ${risk.cls}">${risk.label}</span>`;

    document.getElementById("calcLiquidation").innerText =
        collateralSymbol === "OETH" && collateralAmount > 0
            ? `collateral must stay above ~${(amount * 1.2).toFixed(0)} YEN-e equivalent (120% of owed)`
            : "n/a (CCT collateral has no live price feed)";

    const monthlyCost = durationDays > 0 ? Math.round(((amount * maxInterest) / 100 / durationDays) * 30) : Math.round((amount * maxInterest) / 100);
    document.getElementById("calcMonthly").innerText = `~${monthlyCost} YEN-e in interest`;
}

async function submitLoanRequest() {
    if (!borrowWallet) return;

    const amount = parseFloat(document.getElementById("reqAmount").value);
    const collateralSymbol = document.getElementById("reqCollateralSymbol").value;
    const collateralAmount = parseFloat(document.getElementById("reqCollateralAmount").value);
    const maxInterestPercent = parseFloat(document.getElementById("reqMaxInterest").value);
    const durationDays = parseFloat(document.getElementById("reqDurationDays").value) || 0;
    const purpose = document.getElementById("reqPurpose").value.trim();

    if (isNaN(amount) || amount <= 0 || isNaN(collateralAmount) || collateralAmount < amount || isNaN(maxInterestPercent) || maxInterestPercent <= 0) {
        alert("Enter a valid amount, collateral (at least the borrow amount), and max interest.");
        return;
    }

    try {
        const result = await apiPostSigned("/loan-requests/create", {
            requestedSymbol: "YENE",
            amount,
            collateralSymbol,
            collateralAmount,
            maxInterestPercent,
            durationSeconds: Math.round(durationDays * 86400),
            purpose,
        });
        document.getElementById("reqStatus").innerText =
            `Request submitted (tx ${result.txId}) - visible to lenders once the next block confirms it.`;
    } catch (e) {
        alert("Couldn't submit request: " + e.message);
    }
}

["reqAmount", "reqCollateralAmount", "reqCollateralSymbol", "reqMaxInterest", "reqDurationDays"].forEach((id) => {
    document.getElementById(id).addEventListener("input", updateRequestCalculator);
    document.getElementById(id).addEventListener("change", updateRequestCalculator);
});

offersTableController = makeSortableTable(
    document.getElementById("offersThead"),
    document.getElementById("offersTbody"),
    [],
    renderOffersTable
);

refreshCollateral();
loadProtocolStats();
loadBorrowingPower();
loadLoanOffers();
loadNegotiations();
render();

setInterval(() => {
    refreshCollateral();
    loadProtocolStats();
    loadBorrowingPower();
    loadLoanOffers();
    loadNegotiations();
    render();
}, 10000);
