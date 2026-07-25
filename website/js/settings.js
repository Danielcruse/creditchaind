function settingsLogout() {
    localStorage.removeItem("loggedIn");
    location.href = "login.html";
}

function resetWallet() {
    if (confirm("Forget this browser's wallet? This does not delete anything on-chain - your address, balances and loans remain on the node, you will just need to sign back in with the same address to see them again.")) {
        clearWallet();
        alert("Local wallet forgotten.");
        location.href = "register.html";
    }
}

async function requestSettingsEmailCode() {
    const email = document.getElementById("settingsRecoveryEmail").value.trim();
    const statusEl = document.getElementById("settingsCodeDisplay");
    if (!email) {
        statusEl.innerText = "Enter an email first.";
        return;
    }
    try {
        const result = await apiPostSigned("/recovery/request-email-code", { email });
        statusEl.innerText = result.code
            ? `Code: ${result.code} (${result.note})`
            : `Code sent to ${email} - check your inbox.`;
    } catch (e) {
        statusEl.innerText = "Could not request code: " + e.message;
    }
}

async function confirmSettingsEmail() {
    const email = document.getElementById("settingsRecoveryEmail").value.trim();
    const code = document.getElementById("settingsEmailCode").value.trim();
    const statusEl = document.getElementById("settingsEmailStatus");
    if (!email || !code) {
        statusEl.innerText = "Enter both email and code.";
        return;
    }
    try {
        const result = await apiPostSigned("/recovery/confirm-email", { email, code });
        statusEl.innerText = `Recovery email confirmed (tx ${result.txId}) - this wallet can now be migrated by email + creation year.`;
    } catch (e) {
        statusEl.innerText = "Could not confirm: " + e.message;
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
