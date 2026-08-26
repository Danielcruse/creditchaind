// profile.html - the full-page version of outernet.html's Profile
// card, split out per the user's request to separate wallet/assets/
// profile/network into their own pages. Reputation/voting/credit/
// validator data mirrors outernet.js's loadReputation/loadValidatorStatus
// exactly - same endpoints, no new backend surface. Display-name
// editing replaces the old standalone profile.html's username/email/
// region form - email/region were never read anywhere else in the
// app (dead, decorative fields); display name IS read (outernet.js's
// greeting), so that part carries over.

const profileWallet = requireWallet();

if (profileWallet) {
    document.getElementById("profileAddress").innerText = formatAddress(profileWallet.address);
    document.getElementById("currentUsername").innerText = localStorage.getItem("username") || "Not set";
}

function saveUsername() {
    const value = document.getElementById("newUsername").value.trim();
    if (!value) return;
    localStorage.setItem("username", value);
    document.getElementById("currentUsername").innerText = value;
    document.getElementById("newUsername").value = "";
}

async function loadProfile() {
    if (!profileWallet) return;

    try {
        const w = await apiGet(`/wallet?address=${profileWallet.address}`);
        document.getElementById("voting").innerText = w.votingPower;
    } catch (e) {
        console.error(e);
    }

    try {
        const data = await apiGet(`/reputation?address=${profileWallet.address}`);
        document.getElementById("reputation").innerText = data.score;
        document.getElementById("credit").innerText = data.score;
        document.getElementById("level").innerText = data.creditTier;
    } catch (e) {
        console.error(e);
    }

    try {
        const validators = await apiGet("/validators");
        const mine = validators.find((v) => v.address === profileWallet.address);
        document.getElementById("validator").innerText = mine && mine.active ? "Active" : "Inactive";
        // wallet.NodeID (the old /wallet-sourced value this used to
        // read) is dead state - never written anywhere in the live
        // codebase. A validator's real node delegation lives on
        // ValidatorStatus.AuthorizedNodeID instead, which this same
        // /validators fetch already carries.
        document.getElementById("nodeId").innerText = (mine && mine.authorizedNodeId) || "Not Connected";
    } catch (e) {
        console.error(e);
    }
}

loadProfile();
setInterval(loadProfile, 10000);
