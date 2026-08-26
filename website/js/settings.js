// settingsLogout previously only removed a "loggedIn" key nothing else
// in the app ever reads or sets - getWallet()/setWallet()/clearWallet()
// (api.js) are the real session primitives, keyed on "cc_wallet", so
// this never actually signed anyone out; login.html's returning-wallet
// check would just show the same wallet again. Fixed to call the real
// clearWallet() and land on login.html, which already supports signing
// back in with either a 12-word phrase or a raw private key.
function settingsLogout() {
    if (confirm("Sign out of this wallet? You'll need your 12-word phrase or private key to sign back in - this browser will forget it otherwise.")) {
        clearWallet();
        location.href = "login";
    }
}

function resetWallet() {
    if (confirm("Forget this browser's wallet? This does not delete anything on-chain - your address, balances and loans remain on the node, you will just need to sign back in with the same address to see them again.")) {
        clearWallet();
        alert("Local wallet forgotten.");
        location.href = "register";
    }
}

let ratingSelectedStars = 0;

function paintRatingStars(count) {
    document.querySelectorAll("#ratingStarPicker span").forEach((el) => {
        el.innerHTML = Number(el.dataset.star) <= count ? "&#9733;" : "&#9734;";
    });
}

document.querySelectorAll("#ratingStarPicker span").forEach((el) => {
    el.addEventListener("click", () => {
        ratingSelectedStars = Number(el.dataset.star);
        paintRatingStars(ratingSelectedStars);
    });
    el.addEventListener("mouseenter", () => paintRatingStars(Number(el.dataset.star)));
});
document.getElementById("ratingStarPicker").addEventListener("mouseleave", () => paintRatingStars(ratingSelectedStars));

async function loadExistingRating() {
    const wallet = getWallet();
    if (!wallet) return;
    try {
        const result = await apiGet(`/ratings/mine?address=${wallet.address}`);
        if (result.rated) {
            document.getElementById("ratingExistingNote").innerText =
                `You already rated us ${result.rating.Stars} star(s) - resubmitting updates your rating but won't pay out again.`;
            ratingSelectedStars = result.rating.Stars;
            paintRatingStars(ratingSelectedStars);
            document.getElementById("ratingComment").value = result.rating.Comment || "";
        }
    } catch (e) {
        console.error(e);
    }
}

async function submitRating() {
    const statusEl = document.getElementById("ratingStatus");
    if (ratingSelectedStars < 1) {
        statusEl.innerText = "Pick a star rating first.";
        return;
    }
    const comment = document.getElementById("ratingComment").value.trim();
    try {
        const result = await apiPostSigned("/ratings/submit", { stars: ratingSelectedStars, comment });
        statusEl.innerText = `Thanks! (tx ${result.txId}) - takes effect once the next block confirms it.`;
    } catch (e) {
        statusEl.innerText = "Could not submit: " + e.message;
    }
}

loadExistingRating();

// Contact email (see api/contact_email.go for the full backstory - this
// is a verified NOTIFICATION address, not a recovery credential).
async function loadContactEmailStatus() {
    const wallet = getWallet();
    const statusEl = document.getElementById("contactEmailStatus");
    const confirmRow = document.getElementById("contactEmailConfirmRow");
    if (!wallet || !statusEl) return;

    try {
        const result = await apiGet(`/contact-email/status?address=${wallet.address}`);
        if (result.verified) {
            statusEl.innerText = `Verified: ${result.email}`;
            document.getElementById("contactEmailInput").value = result.email;
            confirmRow.style.display = "none";
        } else if (result.pending) {
            statusEl.innerText = `Verification pending for ${result.pendingEmail} - enter the code we sent, or request a new one.`;
            document.getElementById("contactEmailInput").value = result.pendingEmail;
            confirmRow.style.display = "block";
        } else {
            statusEl.innerText = "No email on file yet.";
            confirmRow.style.display = "none";
        }
    } catch (e) {
        console.error(e);
    }
}

async function requestContactEmailCode() {
    const statusEl = document.getElementById("contactEmailActionStatus");
    const email = document.getElementById("contactEmailInput").value.trim();
    if (!email || !email.includes("@")) {
        statusEl.innerText = "Enter a valid email first.";
        return;
    }
    try {
        const result = await apiPostSigned("/contact-email/request-code", { email });
        statusEl.innerText = `Code sent (tx ${result.txId}) - check your inbox once the next block confirms it.`;
        document.getElementById("contactEmailConfirmRow").style.display = "block";
    } catch (e) {
        statusEl.innerText = "Could not send code: " + e.message;
    }
}

async function confirmContactEmailCode() {
    const statusEl = document.getElementById("contactEmailActionStatus");
    const code = document.getElementById("contactEmailCodeInput").value.trim();
    if (!code) {
        statusEl.innerText = "Enter the code from your email first.";
        return;
    }
    try {
        const result = await apiPostSigned("/contact-email/confirm", { code });
        statusEl.innerText = `Submitted (tx ${result.txId}) - confirming once the next block lands.`;
        setTimeout(loadContactEmailStatus, 3000);
    } catch (e) {
        statusEl.innerText = "Could not confirm: " + e.message;
    }
}

loadContactEmailStatus();

async function initDisplayCurrencySelector() {
    const select = document.getElementById("displayCurrencySelect");
    if (!select) return;
    try {
        const currencies = await apiGet("/price/currencies");
        const current = getDisplayCurrency();
        select.innerHTML = currencies.map((c) => `<option value="${c}" ${c === current ? "selected" : ""}>${c}</option>`).join("");
        select.addEventListener("change", () => setDisplayCurrency(select.value));
    } catch (e) {
        console.error(e);
    }
}
initDisplayCurrencySelector();
