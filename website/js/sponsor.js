const wallet = requireWallet();

async function loadValidatorOptions() {
    const select = document.getElementById("sponsorValidator");
    if (!select) return;
    try {
        const validators = await apiGet("/validators");
        select.innerHTML =
            `<option value="">Global reach (shown to everyone)</option>` +
            (validators || [])
                .map((v) => `<option value="${v.address}">Scoped to validator ${v.address.slice(0, 8)}...${v.address.slice(-6)}</option>`)
                .join("");
    } catch (e) {
        console.error(e);
    }
}

// showSpendLimitNote shows the advertiser's raw RC (REPT) balance - the
// actual CCT spend cap it unlocks (core.MaxAdSpendForRept,
// core/sponsored_post.go) is only ever computed server-side, so this is
// informational, not authoritative; the real limit is enforced (and, if
// exceeded, explained) when the post is actually submitted.
async function showSpendLimitNote() {
    const noteEl = document.getElementById("sponsorSpendLimitNote");
    if (!wallet || !noteEl) return;
    try {
        const result = await apiGet(`/rept/balance?address=${wallet.address}`);
        noteEl.innerText = `Your RC (REPT) balance: ${result.balance}. A higher balance unlocks a larger single-post spend cap.`;
    } catch (e) {
        noteEl.innerText = "";
    }
}

const SPONSORED_POST_CATEGORY_LABELS = {
    VALIDATOR_STAKING: "Validator Staking",
    LENDING: "Lending",
    TRADING_HUB: "Trading Hub",
    GENERAL_BUSINESS: "General Business",
    COMMUNITY: "Community",
};

async function submitSponsoredPost() {
    if (!wallet) return;

    const category = document.getElementById("sponsorCategory").value;
    const validatorId = document.getElementById("sponsorValidator").value;
    const text = document.getElementById("sponsorText").value.trim();
    const cctAmount = parseInt(document.getElementById("sponsorCctAmount").value, 10);
    const statusEl = document.getElementById("sponsorSubmitStatus");

    if (!category) {
        statusEl.innerText = "Select a category.";
        return;
    }
    if (!text) {
        statusEl.innerText = "Enter your post text.";
        return;
    }
    if (!cctAmount || cctAmount < 10) {
        statusEl.innerText = "Enter a CCT amount of at least 10.";
        return;
    }

    try {
        const result = await apiPostSigned("/sponsored-posts/create", {
            validatorId, text, category, cctAmount,
        });
        statusEl.innerText = `Submitted for review (tx ${result.txId}) - it stays hidden until a moderator approves it.`;
        document.getElementById("sponsorText").value = "";
        document.getElementById("sponsorCctAmount").value = "";
        setTimeout(loadMySponsoredPosts, 9000);
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

async function loadMySponsoredPosts() {
    if (!wallet) return;

    const el = document.getElementById("mySponsoredPosts");
    if (!el) return;

    try {
        const posts = await apiGet(`/sponsored-posts/mine?address=${wallet.address}`);

        if (!posts || posts.length === 0) {
            el.innerText = "No Sponsored Posts submitted yet.";
            return;
        }

        el.innerHTML = posts
            .map((p) => {
                const categoryLabel = SPONSORED_POST_CATEGORY_LABELS[p.Category] || p.Category;
                const scopeLabel = p.ValidatorID
                    ? `scoped to validator ${p.ValidatorID.slice(0, 8)}...`
                    : "global reach";
                return `<div class="explorer-row">` +
                    `<span><strong>${categoryLabel}</strong> - ${p.CCTPaid} CCT, ${scopeLabel}</span>` +
                    `<span>${escapeHtml(p.Status)}</span>` +
                    `<span>${escapeHtml(p.Text)}</span>` +
                    `</div>`;
            })
            .join("");
    } catch (e) {
        console.error(e);
        el.innerText = "Could not load your Sponsored Posts.";
    }
}

loadValidatorOptions();
showSpendLimitNote();
loadMySponsoredPosts();
