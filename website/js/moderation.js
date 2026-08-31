const wallet = requireWallet();

// hubCountryAllowlist mirrors core.AllowedHubCountries (GET
// /companies/countries) - used only to flag, in refreshAssignedCompanies
// below, a pending application whose Country isn't eligible for
// approval (including "not on the list at all", which is how a United
// States jurisdiction always reads, since that list has no US entry).
// The real enforcement is server-side (core.CompanyStore.Review); this
// is purely so a moderator sees why before ever clicking Approve.
let hubCountryAllowlist = null;
async function loadHubCountryAllowlist() {
    try {
        hubCountryAllowlist = await apiGet("/companies/countries");
    } catch (e) {
        console.error(e);
    }
}

async function refreshModeratorStatus() {
    if (!wallet) return;

    try {
        const moderators = await apiGet("/moderators");
        const mine = moderators.find((m) => m.address === wallet.address);

        document.getElementById("moderatorStatus").innerText = mine ? "Registered" : "Not registered";
        document.getElementById("moderatorConfirmations").innerText = mine ? mine.confirmations : 0;
        document.getElementById("moderatorDisputes").innerText = mine ? mine.disputes : 0;

        renderModeratorList(moderators);
    } catch (e) {
        console.error(e);
    }
}

function renderModeratorList(moderators) {
    const el = document.getElementById("moderatorList");
    if (!moderators || moderators.length === 0) {
        el.innerText = "No moderators registered yet.";
        return;
    }

    el.innerHTML = moderators
        .map(
            (m) =>
                `<p><span class="explorer-hash">${m.address.slice(0, 8)}...${m.address.slice(-6)}</span> - ` +
                `${m.confirmations} confirmed, ${m.disputes} disputed</p>`
        )
        .join("");
}

async function registerModerator() {
    if (!wallet) return;

    try {
        const result = await apiPostSigned("/moderators/register", {});
        alert(`Registration pending (tx ${result.txId}) - you become a registered moderator once the next block confirms it.`);
        await refreshModeratorStatus();
    } catch (e) {
        alert("Could not register as a moderator: " + e.message);
    }
}

async function moderateTrade(kind, id, disputed) {
    if (!wallet) return;

    const verb = disputed ? "dispute" : "confirm";
    if (!confirm(`${disputed ? "Dispute" : "Confirm"} this trade concluded ${disputed ? "with a problem" : "successfully"}? This is recorded on chain and can't be changed afterward.`)) {
        return;
    }

    try {
        const result = await apiPostSigned("/moderators/confirm", { kind, id, disputed });
        alert(`Moderation ${verb} pending (tx ${result.txId}).`);
        await refreshAssignedTrades();
    } catch (e) {
        alert(`Could not ${verb} this trade: ` + e.message);
    }
}

async function refreshAssignedTrades() {
    if (!wallet) return;

    const el = document.getElementById("assignedTrades");

    try {
        const [negotiations, offers] = await Promise.all([
            apiGet("/negotiations"),
            apiGet("/p2p/offers"),
        ]);

        const myNegotiations = (negotiations || []).filter((n) => n.ModeratorAddress === wallet.address);
        const myOffers = (offers || []).filter((o) => o.ModeratorAddress === wallet.address);

        if (myNegotiations.length === 0 && myOffers.length === 0) {
            el.innerText = "No trades assigned to you yet.";
            return;
        }

        const rows = [];

        myNegotiations.forEach((n) => {
            const pending = n.ModerationStatus === "PENDING" || !n.ModerationStatus;
            rows.push(
                `<p>Negotiation ${n.ID}: ${n.Amount} ${n.Symbol}, lender ${n.Lender.slice(0, 8)}... / borrower ${n.Borrower.slice(0, 8)}... ` +
                `- trade status ${n.Status}, moderation ${n.ModerationStatus || "PENDING"} ` +
                (pending
                    ? `<button onclick="moderateTrade('negotiation','${n.ID}',false)">Confirm</button> ` +
                      `<button onclick="moderateTrade('negotiation','${n.ID}',true)">Dispute</button>`
                    : "") +
                `</p>`
            );
        });

        myOffers.forEach((o) => {
            if (o.PaymentMode === "EXTERNAL") {
                // External trades settle through the dedicated
                // seller/buyer/moderator flow on the Marketplace (P2P), not
                // the generic attestation buttons below - a dispute
                // there is the one place a moderator's call actually
                // moves escrowed funds (see api/p2p_external.go).
                const disputeNote = o.SettlementStatus === "DISPUTED" ? " - awaiting your resolution" : "";
                rows.push(
                    `<p>P2P Offer ${o.ID} (external): ${o.OfferAmount} ${o.OfferSymbol} for ${o.WantAmount} ${o.WantSymbol} (off-app), ` +
                    `creator ${o.Creator.slice(0, 8)}... - settlement ${o.SettlementStatus || "-"}${disputeNote} ` +
                    `<a href="p2p" style="color:var(--cc-accent-gold, #16B8A6);">Open on Marketplace (P2P)</a></p>`
                );
                return;
            }

            const pending = o.ModerationStatus === "PENDING" || !o.ModerationStatus;
            rows.push(
                `<p>P2P Offer ${o.ID}: ${o.OfferAmount} ${o.OfferSymbol} for ${o.WantAmount} ${o.WantSymbol}, creator ${o.Creator.slice(0, 8)}... ` +
                `- trade status ${o.Status}, moderation ${o.ModerationStatus || "PENDING"} ` +
                (pending
                    ? `<button onclick="moderateTrade('p2p','${o.ID}',false)">Confirm</button> ` +
                      `<button onclick="moderateTrade('p2p','${o.ID}',true)">Dispute</button>`
                    : "") +
                `</p>`
            );
        });

        el.innerHTML = rows.join("");
    } catch (e) {
        console.error(e);
        el.innerText = "Could not load assigned trades.";
    }
}

async function reviewCompany(address, approve) {
    const verb = approve ? "approve" : "reject";
    if (!confirm(`${approve ? "Approve" : "Reject"} this company's Verified badge application? This is recorded on chain and can't be changed afterward.`)) {
        return;
    }

    try {
        const result = await apiPostSigned("/companies/review", { address, approve });
        alert(`Review ${verb} pending (tx ${result.txId}).`);
        await refreshAssignedCompanies();
    } catch (e) {
        alert(`Could not ${verb} this application: ` + e.message);
    }
}

async function refreshAssignedCompanies() {
    if (!wallet) return;

    const el = document.getElementById("assignedCompanies");
    if (!el) return;

    try {
        const companies = await apiGet("/companies");
        const mine = (companies || []).filter((c) => c.ModeratorAddress === wallet.address);

        if (mine.length === 0) {
            el.innerText = "No company applications assigned to you yet.";
            return;
        }

        el.innerHTML = mine
            .map((c) => {
                const pending = c.Status === "PENDING";
                // A blank/unrecognized Country can never actually be
                // approved - core.CompanyStore.Review refuses it server-
                // side regardless of what a moderator clicks (see that
                // function's own doc comment) - but the Approve button is
                // hidden here too, so a moderator sees why up front
                // instead of getting a rejected transaction with no
                // context. This should be rare in practice: the
                // application form only ever offers a dropdown with no
                // United States option, never free text.
                const usBlocked = !hubCountryAllowlist || !c.Country || !hubCountryAllowlist.includes(c.Country);
                const countryLine = `<p style="opacity:0.7;font-size:12px;">Jurisdiction: ${c.Country ? escapeHtml(c.Country) : "(not set)"}` +
                    (usBlocked ? ` - <strong style="color:#ff6b6b;">not eligible for approval</strong> (this platform holds no US money-transmitter/MSB license anywhere; approving a US-based or unset-jurisdiction Trading Hub can mean real fines and legal consequences for the approving moderator)` : "") +
                    `</p>`;
                return `<p>${escapeHtml(c.Name)} (${c.Address.slice(0, 8)}...) - ${escapeHtml(c.Status)} ` +
                    (pending
                        ? (usBlocked
                            ? `<button disabled title="Not an eligible jurisdiction">Approve</button> `
                            : `<button onclick="reviewCompany('${c.Address}', true)">Approve</button> `) +
                          `<button onclick="reviewCompany('${c.Address}', false)">Reject</button>`
                        : "") +
                    `</p>` + countryLine +
                    `<p style="opacity:0.7;font-size:12px;">${escapeHtml(c.Description || "")}${c.DocumentNote ? " - " + escapeHtml(c.DocumentNote) : ""}</p>`;
            })
            .join("");
    } catch (e) {
        console.error(e);
        el.innerText = "Could not load assigned applications.";
    }
}

async function reassignCompanyModerator(address) {
    if (!confirm("Claim this application for your own review? This reassigns it away from whoever it was originally assigned to - only do this if that moderator is unreachable.")) {
        return;
    }

    try {
        const result = await apiPostSigned("/companies/reassign-moderator", { address });
        alert(`Claim pending (tx ${result.txId}) - it'll show up under "Assigned To You" once the next block confirms it.`);
        await refreshUnclaimedCompanies();
        await refreshAssignedCompanies();
    } catch (e) {
        alert("Could not claim this application: " + e.message);
    }
}

async function refreshUnclaimedCompanies() {
    if (!wallet) return;

    const el = document.getElementById("unclaimedCompanies");
    if (!el) return;

    try {
        const moderators = await apiGet("/moderators");
        const amModerator = (moderators || []).some((m) => m.address === wallet.address);
        if (!amModerator) {
            el.innerText = "Register as a moderator above to claim unassigned applications.";
            return;
        }

        const companies = await apiGet("/companies");
        const unclaimed = (companies || []).filter(
            (c) => c.Status === "PENDING" && c.ModeratorAddress !== wallet.address
        );

        if (unclaimed.length === 0) {
            el.innerText = "No unclaimed pending applications right now.";
            return;
        }

        el.innerHTML = unclaimed
            .map(
                (c) =>
                    `<p>${escapeHtml(c.Name)} (${c.Address.slice(0, 8)}...) - assigned to ${c.ModeratorAddress ? c.ModeratorAddress.slice(0, 8) + "..." : "nobody"} ` +
                    `<button onclick="reassignCompanyModerator('${c.Address}')">Claim for My Review</button></p>`
            )
            .join("");
    } catch (e) {
        console.error(e);
        el.innerText = "Could not load unclaimed applications.";
    }
}

// --- Sponsored Post review -------------------------------------------

async function reviewSponsoredPost(id, approve) {
    const verb = approve ? "approve" : "reject";
    if (!confirm(`${approve ? "Approve" : "Reject"} this Sponsored Post? Once approved it becomes visible to eligible viewers immediately after the next block confirms it.`)) {
        return;
    }

    try {
        const result = await apiPostSigned("/sponsored-posts/review", { id, approve });
        alert(`Review ${verb} pending (tx ${result.txId}).`);
        await refreshAssignedSponsoredPosts();
    } catch (e) {
        alert(`Could not ${verb} this post: ` + e.message);
    }
}

const SPONSORED_POST_CATEGORY_LABELS = {
    VALIDATOR_STAKING: "Validator Staking",
    LENDING: "Lending",
    TRADING_HUB: "Trading Hub",
    GENERAL_BUSINESS: "General Business",
    COMMUNITY: "Community",
};

async function refreshAssignedSponsoredPosts() {
    if (!wallet) return;

    const el = document.getElementById("assignedSponsoredPosts");
    if (!el) return;

    try {
        const posts = await apiGet(`/sponsored-posts/assigned?moderator=${wallet.address}`);

        if (!posts || posts.length === 0) {
            el.innerText = "No Sponsored Posts assigned to you yet.";
            return;
        }

        el.innerHTML = posts
            .map((p) => {
                const pending = p.Status === "PENDING_REVIEW";
                const categoryLabel = SPONSORED_POST_CATEGORY_LABELS[p.Category] || p.Category;
                const scopeLabel = p.ValidatorID
                    ? `scoped to validator ${p.ValidatorID.slice(0, 8)}...`
                    : "global reach";
                // makesNumericClaims (core.MakesNumericClaims, server-computed
                // and included in the response) gates whether this panel
                // even offers a numbers-to-check note - a general-business
                // post never gets this lane at all (see api/sponsored_post.go's
                // doc comment).
                const numericClaimsNote = p.makesNumericClaims
                    ? `<p style="opacity:0.85;font-size:12px;color:#b45309;">⚠ This category commonly makes a numeric claim (APR/rate/yield) -
                       verify the claimed number against the real current rate before approving
                       ${p.ValidatorID ? `(check validator ${p.ValidatorID.slice(0, 8)}... on the Validators page)` : ""}.</p>`
                    : "";
                return `<p><strong>${categoryLabel}</strong> - ${p.CCTPaid} CCT, ${scopeLabel} - ${escapeHtml(p.Status)} ` +
                    (pending
                        ? `<button onclick="reviewSponsoredPost('${p.ID}', true)">Approve</button> ` +
                          `<button onclick="reviewSponsoredPost('${p.ID}', false)">Reject</button>`
                        : "") +
                    `</p>` +
                    `<p style="opacity:0.8;font-size:13px;">${escapeHtml(p.Text)}</p>` +
                    numericClaimsNote;
            })
            .join("<hr style=\"opacity:0.15;\">");
    } catch (e) {
        console.error(e);
        el.innerText = "Could not load assigned Sponsored Posts.";
    }
}

refreshModeratorStatus();
refreshAssignedTrades();
loadHubCountryAllowlist().then(refreshAssignedCompanies);
refreshUnclaimedCompanies();
refreshAssignedSponsoredPosts();

setInterval(() => {
    refreshModeratorStatus();
    refreshAssignedTrades();
    loadHubCountryAllowlist().then(refreshAssignedCompanies);
    refreshUnclaimedCompanies();
    refreshAssignedSponsoredPosts();
}, 10000);
