const wallet = requireWallet();

function formatCountdown(ms) {
    const totalMinutes = Math.floor(ms / 60000);
    const days = Math.floor(totalMinutes / (60 * 24));
    const hours = Math.floor((totalMinutes % (60 * 24)) / 60);
    if (days > 0) return `${days}d ${hours}h`;
    const minutes = totalMinutes % 60;
    if (hours > 0) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
}

async function checkGovernanceActivation() {
    if (!wallet) return;

    try {
        const w = await apiGet(`/wallet?address=${wallet.address}`);
        const activated = w.votingPower > 0;

        document.getElementById("govNotActivated").style.display = activated ? "none" : "block";
        document.getElementById("govActivated").style.display = activated ? "block" : "none";

        if (activated) {
            const weight = await apiGet(`/governance/my-weight?address=${wallet.address}`);
            document.getElementById("myVotingWeight").innerText = weight.weight;
        }
    } catch (e) {
        console.error(e);
    }
}

function onProposalKindChange() {
    const kind = document.getElementById("proposalKind").value;
    document.getElementById("paramChangeFields").style.display = kind === "PARAM_CHANGE" ? "block" : "none";
    document.getElementById("versionActivationFields").style.display = kind === "VERSION_ACTIVATION" ? "block" : "none";
}

async function activateGovernance() {
    if (!wallet) return;

    try {
        const result = await apiPostSigned("/governance/activate", {});
        document.getElementById("activateStatus").innerText =
            `Activating (tx ${result.txId}) - takes effect once the next block confirms it.`;
        setTimeout(checkGovernanceActivation, 9000);
    } catch (e) {
        document.getElementById("activateStatus").innerText = "Could not activate: " + e.message;
    }
}

async function postGovernanceStatus() {
    if (!wallet) return;

    const text = document.getElementById("statusText").value.trim();
    if (!text) {
        alert("Enter a status update first.");
        return;
    }

    try {
        await apiPostSigned("/governance/status/post", { text });
        document.getElementById("statusText").value = "";
        document.getElementById("statusPostResult").innerText = "Posted - now live on the landing page.";
        loadStatusFeed();
    } catch (e) {
        document.getElementById("statusPostResult").innerText = "Could not post: " + e.message;
    }
}

async function loadStatusFeed() {
    const div = document.getElementById("statusFeed");

    try {
        const statuses = await apiGet("/governance/status/recent");

        if (!statuses || statuses.length === 0) {
            div.innerHTML = "No community updates yet.";
            return;
        }

        div.innerHTML = statuses
            .map((s) => {
                const time = s.Timestamp ? new Date(s.Timestamp * 1000).toLocaleString() : "-";
                return `
                    <div style="padding:8px 0;border-bottom:1px solid rgba(255,255,255,0.08);">
                        <p style="margin:0 0 4px;">${s.Text}</p>
                        <p style="margin:0;opacity:0.55;font-size:11px;">${formatAddress(s.Address)} &middot; ${time}</p>
                    </div>
                `;
            })
            .join("");
    } catch (e) {
        console.error(e);
        div.innerHTML = "No community updates yet.";
    }
}

async function loadAnnouncements() {
    const card = document.getElementById("announcementsCard");
    const div = document.getElementById("announcementsList");

    try {
        const announcements = await apiGet("/announcements/recent");

        if (!announcements || announcements.length === 0) {
            card.style.display = "none";
            return;
        }

        card.style.display = "block";
        div.innerHTML = announcements
            .map((a) => {
                const time = a.CreatedAt ? new Date(a.CreatedAt * 1000).toLocaleString() : "-";
                return `
                    <div style="padding:8px 0;border-bottom:1px solid rgba(255,255,255,0.08);">
                        <p style="margin:0 0 4px;font-weight:bold;">${a.Title}</p>
                        <p style="margin:0 0 4px;">${a.Text}</p>
                        <p style="margin:0;opacity:0.55;font-size:11px;">${time}</p>
                    </div>
                `;
            })
            .join("");
    } catch (e) {
        console.error(e);
        card.style.display = "none";
    }
}

async function loadUserLog() {
    const input = document.getElementById("userLogAddress").value.trim();
    const address = input || (wallet ? wallet.address : "");
    const div = document.getElementById("userLogResult");

    if (!address) {
        div.innerHTML = "Enter an address, or log in to view your own.";
        return;
    }

    div.innerHTML = "Loading...";

    try {
        const [statuses, votes] = await Promise.all([
            apiGet(`/governance/status/user?address=${address}`),
            apiGet(`/governance/votes?address=${address}`),
        ]);

        let html = `<h4 style="margin-bottom:4px;">${formatAddress(address)}</h4>`;

        html += `<p style="opacity:0.7;font-size:12px;margin-top:0;">Status History</p>`;
        if (!statuses || statuses.length === 0) {
            html += `<p style="opacity:0.6;font-size:13px;">No status updates posted.</p>`;
        } else {
            html += statuses
                .slice()
                .reverse()
                .map((s) => {
                    const time = s.Timestamp ? new Date(s.Timestamp * 1000).toLocaleString() : "-";
                    return `<div style="padding:6px 0;border-bottom:1px solid rgba(255,255,255,0.06);">
                        <p style="margin:0 0 2px;">${s.Text}</p>
                        <p style="margin:0;opacity:0.55;font-size:11px;">${time}</p>
                    </div>`;
                })
                .join("");
        }

        html += `<p style="opacity:0.7;font-size:12px;margin-top:14px;">Vote History - permanent, public record. Votes are never punished, only remembered.</p>`;
        if (!votes || votes.length === 0) {
            html += `<p style="opacity:0.6;font-size:13px;">No votes cast yet.</p>`;
        } else {
            html += votes
                .slice()
                .reverse()
                .map((v) => {
                    const time = v.Timestamp ? new Date(v.Timestamp * 1000).toLocaleString() : "-";
                    const side = v.Support
                        ? `<span style="color:#4caf50;">YES</span>`
                        : `<span style="color:#e05252;">NO</span>`;
                    return `<div style="padding:6px 0;border-bottom:1px solid rgba(255,255,255,0.06);">
                        <p style="margin:0 0 2px;">${side} on "${v.ProposalTitle}" (weight ${v.Weight})</p>
                        <p style="margin:0;opacity:0.55;font-size:11px;">${time}</p>
                    </div>`;
                })
                .join("");
        }

        div.innerHTML = html;
    } catch (e) {
        console.error(e);
        div.innerHTML = "Could not load this user's log.";
    }
}

async function createProposal() {
    if (!wallet) return;

    const title = document.getElementById("proposalTitle").value;
    const description = document.getElementById("proposalDescription").value;
    const kind = document.getElementById("proposalKind").value;

    if (title === "") {
        alert("Title required.");
        return;
    }

    const body = { title: title, description: description, kind: kind };

    if (kind === "PARAM_CHANGE") {
        const paramValue = document.getElementById("paramValue").value;
        if (paramValue === "") {
            alert("Enter a new value for the parameter.");
            return;
        }
        body.paramKey = document.getElementById("paramKey").value;
        body.paramValue = Number(paramValue);
    } else if (kind === "VERSION_ACTIVATION") {
        const targetVersion = document.getElementById("targetVersion").value.trim();
        if (targetVersion === "") {
            alert("Enter a target version.");
            return;
        }
        body.targetVersion = targetVersion;
    }

    try {
        const result = await apiPostSigned("/governance/create", body);

        document.getElementById("proposalTitle").value = "";
        document.getElementById("proposalDescription").value = "";

        document.getElementById("pendingStatus").innerText =
            `Pending (tx ${result.txId}) - appears below once the next block confirms it.`;
    } catch (e) {
        alert("Could not create proposal: " + e.message);
    }
}

async function vote(id, support) {
    if (!wallet) return;

    try {
        const result = await apiPostSigned("/governance/vote", {
            proposalId: id,
            support: support,
        });

        document.getElementById("pendingStatus").innerText =
            `Vote pending (tx ${result.txId}) - tally updates once confirmed.`;
    } catch (e) {
        alert("Vote failed: " + e.message);
    }
}

async function render() {
    const div = document.getElementById("proposalList");

    try {
        const proposals = await apiGet("/governance");

        div.innerHTML = "";

        if (!proposals || proposals.length === 0) {
            div.innerHTML = "No proposals yet.";
            return;
        }

        proposals.forEach((p) => {
            const totalWeight = p.YesVotes + p.NoVotes;
            const quorum = p.Quorum || 0;
            const quorumPct = quorum > 0 ? Math.min(100, Math.round((totalWeight / quorum) * 100)) : 0;

            const kindBadge = p.Kind === "PARAM_CHANGE"
                ? `<span style="opacity:0.7;">[Parameter Change: ${p.ParamKey} → ${p.ParamValue}]</span>`
                : p.Kind === "VERSION_ACTIVATION"
                    ? `<span style="opacity:0.7;">[Version Activation: ${p.TargetVersion}]</span>`
                    : "";

            const systemBadge = p.SystemGenerated
                ? `<span style="opacity:0.7;color:#c9a227;">[System-generated]</span>`
                : "";

            let statusBadge;
            let deadlineLine;
            let voteButtons;
            if (p.Open) {
                statusBadge = `<span style="opacity:0.7;">Open</span>`;
                const remainingMs = p.Deadline ? (p.Deadline * 1000 - Date.now()) : 0;
                deadlineLine = p.Deadline
                    ? `Voting ends ${new Date(p.Deadline * 1000).toLocaleString()}${remainingMs > 0 ? " (" + formatCountdown(remainingMs) + " left)" : " (closing soon)"}`
                    : "";
                voteButtons = `
                    <button onclick="vote('${p.ID}', true)">Vote Yes</button>
                    <button onclick="vote('${p.ID}', false)">Vote No</button>
                `;
            } else {
                statusBadge = p.Status === "PASSED"
                    ? `<span style="color:#4caf50;">Passed</span>`
                    : `<span style="color:#e05252;">Failed</span>`;
                deadlineLine = "Voting closed.";
                voteButtons = "";
            }

            div.innerHTML += `
                <div class="card">
                    <h3>${p.Title} ${kindBadge} ${systemBadge}</h3>
                    <p>${p.Description}</p>
                    <p>Status: ${statusBadge}</p>
                    <p>YES: ${p.YesVotes} &middot; NO: ${p.NoVotes}</p>
                    <div style="background:rgba(255,255,255,0.08);border-radius:4px;height:8px;overflow:hidden;margin:6px 0;">
                        <div style="background:#4c8bf5;height:100%;width:${quorumPct}%;"></div>
                    </div>
                    <p style="opacity:0.6;font-size:12px;">Quorum: ${totalWeight}/${quorum} weight (${quorumPct}%)</p>
                    <p style="opacity:0.6;font-size:12px;">${deadlineLine}</p>
                    ${voteButtons}
                </div>
                <br>
            `;
        });
    } catch (e) {
        console.error(e);
        div.innerHTML = "No proposals yet.";
    }
}

render();
checkGovernanceActivation();
loadStatusFeed();
loadAnnouncements();
loadUserLog();

setInterval(render, 10000);
setInterval(loadStatusFeed, 10000);
setInterval(loadAnnouncements, 30000);
