const borrowWallet = requireWallet();

async function refreshCollateral() {
    if (!borrowWallet) return;

    try {
        const [collateral, staking, usdcTestBalance] = await Promise.all([
            apiGet(`/collateral?address=${borrowWallet.address}`),
            apiGet(`/staking?address=${borrowWallet.address}`),
            fetchTokenBalance(borrowWallet, "USDCTEST"),
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
    if (!borrowWallet) return;

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

        mine.forEach((loan) => {
            const symbol = loan.Symbol || "USDCTEST";
            const total = repayAmountFor(loan);
            // Self-funded loans backed by oETH collateral get a rate
            // that moved with the ETHTEST/CCT price at the moment this
            // loan was taken out (see core.PriceAdjustedInterestRate) -
            // called out here so "why is my rate different from the
            // flat tier table" has an answer right on the loan itself.
            const priceAdjustedNote = !loan.Lender && loan.CollateralSymbol === "OETH" ? " (price-adjusted)" : "";
            const lenderLabel = loan.Lender
                ? `lender: ${loan.Lender.slice(0, 8)}...${loan.Lender.slice(-6)} @ ${loan.Interest}%`
                : `self-funded @ ${loan.Interest}%${priceAdjustedNote}`;

            // DueAt (see core.Loan's doc comment) is 0 for self-funded
            // loans and any peer-funded loan drawn against an offer
            // that never set a duration - neither can ever default.
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

            div.innerHTML += `
                <p>
                    Loan ${loan.ID}: ${loan.Amount} ${symbol} | ${loan.Status} | ${lenderLabel}${dueLabel}${defaultWarning}
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

refreshCollateral();
render();

setInterval(() => {
    refreshCollateral();
    render();
}, 10000);
