// lending-shared.js - small helpers shared between borrow.html/borrow.js
// and lend.html/lend.js. Deliberately pure/presentational: nothing
// here calls an API or mutates state, it only buckets numbers the
// pages already fetched from real endpoints into display labels.

// healthTier buckets a real /loans/health ratioPercent (only
// meaningful when priceAvailable is true - see api/loan_collateral.go's
// doc comment) into the 4-tier Healthy/Safe/Warning/Liquidation Risk
// scale. Thresholds are relative to core.LiquidationThresholdPercent
// (120): >=180% "Healthy", >=150% "Safe", >=120% "Warning", below
// that "Liquidation Risk" - a judgment call on exact cutoffs, not a
// backend-enforced constant.
function healthTier(ratioPercent, priceAvailable) {
    if (!priceAvailable) {
        return { label: "No price data", cls: "health-unknown" };
    }
    if (ratioPercent >= 180) return { label: "Healthy", cls: "health-healthy" };
    if (ratioPercent >= 150) return { label: "Safe", cls: "health-safe" };
    if (ratioPercent >= 120) return { label: "Warning", cls: "health-warning" };
    return { label: "Liquidation Risk", cls: "health-liquidation" };
}

// riskRating is a pure-frontend display convenience (NOT a new source
// of truth - the real risk-relevant numbers all come from already-
// fetched endpoints) that scores Very Low..Very High from collateral
// ratio, borrower reputation, loan size relative to their tier's
// BorrowLimit, and duration. Weighted, then bucketed - a reasonable
// starting formula, not derived from a specified spec.
function riskRating({ ratioPercent, priceAvailable, reputation, amount, borrowLimit, durationSeconds }) {
    let score = 0; // higher = riskier

    if (!priceAvailable) {
        score += 30; // no collateral price data is itself a risk signal
    } else if (ratioPercent < 120) {
        score += 50;
    } else if (ratioPercent < 150) {
        score += 30;
    } else if (ratioPercent < 180) {
        score += 15;
    }

    score += Math.max(0, 40 - (reputation || 0) * 0.4); // 0 rep -> +40, 100 rep -> 0

    if (borrowLimit > 0) {
        const sizeRatio = amount / borrowLimit;
        score += Math.min(20, sizeRatio * 20);
    }

    if (durationSeconds > 0) {
        const days = durationSeconds / 86400;
        score += Math.min(10, days / 36); // ~360 days -> +10
    }

    if (score < 20) return { label: "Very Low", cls: "risk-verylow" };
    if (score < 40) return { label: "Low", cls: "risk-low" };
    if (score < 60) return { label: "Medium", cls: "risk-medium" };
    if (score < 80) return { label: "High", cls: "risk-high" };
    return { label: "Very High", cls: "risk-veryhigh" };
}

// makeSortableTable wires click-to-sort on every <th data-sort-key>
// inside theadEl, re-rendering tbodyEl via renderFn(sortedRows)
// whenever the sort column/direction changes. No framework, no
// external dependency, consistent with this codebase's existing
// vanilla-JS convention (see order-book.js).
function makeSortableTable(theadEl, tbodyEl, rows, renderFn) {
    let sortKey = null;
    let sortDir = 1;

    function render() {
        let sorted = rows.slice();
        if (sortKey) {
            sorted.sort((a, b) => {
                const av = a[sortKey], bv = b[sortKey];
                if (av < bv) return -1 * sortDir;
                if (av > bv) return 1 * sortDir;
                return 0;
            });
        }
        renderFn(sorted);
    }

    theadEl.querySelectorAll("th[data-sort-key]").forEach((th) => {
        th.addEventListener("click", () => {
            const key = th.dataset.sortKey;
            if (sortKey === key) {
                sortDir *= -1;
            } else {
                sortKey = key;
                sortDir = 1;
            }
            render();
        });
    });

    render();
    return { rerender: (newRows) => { rows = newRows; render(); } };
}
