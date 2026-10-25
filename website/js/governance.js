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
    document.getElementById("tokenCreationFields").style.display = kind === "TOKEN_CREATION" ? "block" : "none";
    document.getElementById("predictionFields").style.display = kind === "PREDICTION" ? "block" : "none";
    document.getElementById("moderatorDisputeFields").style.display = kind === "MODERATOR_DISPUTE" ? "block" : "none";
    document.getElementById("emergencyFields").style.display = kind === "EMERGENCY" ? "block" : "none";
    if (kind === "TOKEN_CREATION") loadTcStandards();
}

let tcStandardsLoaded = false;
async function loadTcStandards() {
    if (tcStandardsLoaded) return;
    const select = document.getElementById("tcStandard");
    try {
        const standards = await apiGet("/token-standards");
        select.innerHTML = (standards || []).map((s) => `<option value="${s.Code}">${s.Code} - ${s.Name}</option>`).join("");
        select.addEventListener("change", () => {
            document.getElementById("tcPegReference").style.display = select.value === "C117S" ? "inline-block" : "none";
        });
        tcStandardsLoaded = true;
    } catch (e) {
        console.error(e);
    }
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
                        <p style="margin:0 0 4px;">${escapeHtmlMultiline(s.Text)}</p>
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
                        <p style="margin:0 0 4px;font-weight:bold;">${escapeHtml(a.Title)}</p>
                        <p style="margin:0 0 4px;">${escapeHtmlMultiline(a.Text)}</p>
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
                        <p style="margin:0 0 2px;">${escapeHtmlMultiline(s.Text)}</p>
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
                        <p style="margin:0 0 2px;">${side} on "${escapeHtml(v.ProposalTitle)}" (weight ${v.Weight})</p>
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
    } else if (kind === "TOKEN_CREATION") {
        const tokenSymbol = document.getElementById("tcSymbol").value.trim().toUpperCase();
        const tokenName = document.getElementById("tcName").value.trim();
        const tokenInitialSupply = parseInt(document.getElementById("tcSupply").value, 10);
        const tokenStandard = document.getElementById("tcStandard").value;
        if (!tokenSymbol || !tokenName || !tokenInitialSupply) {
            alert("Fill in the token symbol, name, and initial supply.");
            return;
        }
        body.tokenSymbol = tokenSymbol;
        body.tokenName = tokenName;
        body.tokenInitialSupply = tokenInitialSupply;
        body.tokenStandard = tokenStandard;
        if (tokenStandard === "C117S") {
            body.tokenPegReference = document.getElementById("tcPegReference").value.trim();
        }
    } else if (kind === "PREDICTION") {
        const targetSymbol = document.getElementById("predTargetSymbol").value;
        const targetPrice = parseInt(document.getElementById("predTargetPrice").value, 10);
        const durationDays = parseInt(document.getElementById("predDurationDays").value, 10);
        if (!targetPrice || targetPrice <= 0) {
            alert("Enter a positive target price (CCT per unit, scaled by 1,000,000).");
            return;
        }
        body.predictionTargetSymbol = targetSymbol;
        body.predictionTargetPrice = targetPrice;
        if (durationDays > 0) {
            body.predictionDurationSeconds = durationDays * 24 * 3600;
        }
    } else if (kind === "MODERATOR_DISPUTE") {
        const disputeModeratorAddress = document.getElementById("mdModeratorAddress").value.trim();
        const disputeReferenceType = document.getElementById("mdReferenceType").value;
        const disputeReferenceId = document.getElementById("mdReferenceId").value.trim();
        if (!disputeModeratorAddress || !disputeReferenceId) {
            alert("Enter the moderator's address and the negotiation/offer/company-application ID you're disputing.");
            return;
        }
        body.disputeModeratorAddress = disputeModeratorAddress;
        body.disputeReferenceType = disputeReferenceType;
        body.disputeReferenceId = disputeReferenceId;
    } else if (kind === "EMERGENCY") {
        body.paramKey = "BridgePaused";
        body.paramValue = Number(document.getElementById("emergencyAction").value);
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
                    : p.Kind === "TOKEN_CREATION"
                        ? `<span style="opacity:0.7;">[Token Creation: ${p.TokenSymbol}]</span>`
                        : p.Kind === "PREDICTION"
                            ? `<span style="opacity:0.7;">[Prediction: ${p.PredictionTargetSymbol} &ge; ${p.PredictionTargetPrice}]</span>`
                            : p.Kind === "EMERGENCY"
                                ? `<span style="opacity:0.7;color:#e05252;">[Emergency: ${p.ParamKey} → ${p.ParamValue}]</span>`
                                : "";

            const systemBadge = p.SystemGenerated
                ? `<span style="opacity:0.7;color:#16B8A6;">[System-generated]</span>`
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
                if (p.Status === "PASSED") {
                    // A PARAM_CHANGE/VERSION_ACTIVATION/TOKEN_CREATION/
                    // EMERGENCY proposal's real effect is timelocked (see
                    // core.Proposal.ExecuteAfter) - it does NOT take
                    // effect the instant it passes, so this says exactly
                    // when it will rather than implying it already has.
                    statusBadge = (p.ExecuteAfter && !p.Executed)
                        ? `<span style="color:#4caf50;">Passed — takes effect ${new Date(p.ExecuteAfter * 1000).toLocaleString()}</span>`
                        : `<span style="color:#4caf50;">Passed${p.Executed ? " — in effect" : ""}</span>`;
                } else {
                    statusBadge = `<span style="color:#e05252;">Failed</span>`;
                }
                deadlineLine = "Voting closed.";
                voteButtons = "";
            }

            div.innerHTML += `
                <div class="card">
                    <h3>${escapeHtml(p.Title)} ${kindBadge} ${systemBadge}</h3>
                    <p>${escapeHtmlMultiline(p.Description)}</p>
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

// --- Intending Validators tab: list of OPEN validator applications,
// so any eligible activated-governance wallet (reputation >= 30) can
// throw its support behind up to 3 of them - see
// api/validator_application.go's validatorApplicationMinReputationToVote/
// MaxVotesPerWallet for the backend side of these same numbers.
async function loadIntendingValidators() {
    const listEl = document.getElementById("intendingValidatorsList");
    const statusEl = document.getElementById("myValidatorVoteStatus");

    try {
        const [applications, myVotes] = await Promise.all([
            apiGet("/validator-application/list"),
            apiGet(`/validator-application/my-votes?address=${wallet.address}`),
        ]);

        if (!myVotes.governanceActive) {
            statusEl.innerHTML = "Activate governance above to support a validator application.";
        } else if (myVotes.reputation < myVotes.minReputation) {
            statusEl.innerHTML = `Your reputation (${myVotes.reputation}) is below the minimum (${myVotes.minReputation}) needed to support an application.`;
        } else {
            statusEl.innerHTML = `You have <b>${myVotes.votesRemaining}</b> of ${myVotes.maxVotes} validator-support votes remaining.`;
        }

        if (!applications || applications.length === 0) {
            listEl.innerHTML = "No one is currently applying to become a validator.";
            return;
        }

        listEl.innerHTML = applications
            .map((a) => {
                const pct = a.requiredWeight > 0 ? Math.min(100, Math.round((a.supportWeight / a.requiredWeight) * 100)) : 0;
                const disabled = !myVotes.eligible || myVotes.votesRemaining <= 0;
                const reason = !myVotes.eligible
                    ? "not eligible"
                    : myVotes.votesRemaining <= 0
                      ? "no votes left"
                      : "";
                return `
                    <div style="border-bottom:1px solid rgba(255,255,255,0.08);padding:10px 0;">
                        <div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:8px;">
                            <div>
                                <b>${a.address}</b><br>
                                <span style="opacity:0.7;font-size:12px;">Reputation: ${a.reputation}</span>
                            </div>
                            <div style="text-align:right;">
                                <span style="font-size:12px;opacity:0.75;">${a.supportWeight} / ${a.requiredWeight} support weight (${pct}%)</span><br>
                                <button ${disabled ? "disabled" : ""} onclick="supportValidatorApplication('${a.address}')">
                                    Support${reason ? ` (${reason})` : ""}
                                </button>
                            </div>
                        </div>
                    </div>
                `;
            })
            .join("");
    } catch (e) {
        console.error(e);
        listEl.innerHTML = "Could not load intending validators.";
    }
}

async function supportValidatorApplication(address) {
    try {
        await apiPostSigned("/validator-application/support", { address });
        alert("Support submitted - pending confirmation in the next block.");
        loadIntendingValidators();
    } catch (e) {
        alert(e.message || "Could not submit support.");
    }
}

// sortBallots is the one place ballot order is decided. Still-promoted
// decision ballots (RC-burned via /proposals/promote, see
// applyProposalPromote in api/rc_promote.go) sort first, longest
// remaining promoted time first; everything else falls back to the
// original recency ordering - decision/executive ballots have no
// CreatedAt field, but their Deadline is a fixed offset from creation
// (see core.ProposalVotingWindowSeconds), so it's a reasonable recency
// proxy; support-application ballots sort by their real CreatedAt.
function sortBallots(ballots) {
    const now = Date.now() / 1000;
    return ballots.slice().sort((a, b) => {
        const aPromoted = a.type === "decision" && a.promotedUntil > now ? a.promotedUntil : 0;
        const bPromoted = b.type === "decision" && b.promotedUntil > now ? b.promotedUntil : 0;
        if (aPromoted !== bPromoted) return bPromoted - aPromoted;

        const aKey = a.type === "support" ? a.createdAt || 0 : a.deadline || 0;
        const bKey = b.type === "support" ? b.createdAt || 0 : b.deadline || 0;
        return bKey - aKey;
    });
}

function ballotKindBadge(kind, ballot) {
    if (kind === "PARAM_CHANGE") return `<span style="opacity:0.7;">[Parameter Change: ${ballot.paramKey} &rarr; ${ballot.paramValue}]</span>`;
    if (kind === "VERSION_ACTIVATION") return `<span style="opacity:0.7;">[Version Activation: ${ballot.targetVersion}]</span>`;
    // TOKEN_CREATION isn't a real proposal kind yet (Phase 4 adds it) -
    // this branch is a placeholder so Phase 4 only needs to add data,
    // not touch this rendering function.
    if (kind === "TOKEN_CREATION") return `<span style="opacity:0.7;">[Token Creation: ${ballot.tokenSymbol || "?"}]</span>`;
    if (kind === "PREDICTION") return `<span style="opacity:0.7;">[Prediction: ${ballot.predictionTargetSymbol} &ge; ${ballot.predictionTargetPrice}]</span>`;
    if (kind === "MODERATOR_DISPUTE") return `<span style="opacity:0.7;">[Moderator Dispute: ${ballot.disputeModeratorAddress}]</span>`;
    return "";
}

async function loadVotingBallots() {
    const listEl = document.getElementById("votingBallotsList");
    if (!listEl) return;

    try {
        const [proposals, applications, myVotes, amIExec, execProposals] = await Promise.all([
            apiGet("/governance"),
            apiGet("/validator-application/list"),
            wallet ? apiGet(`/validator-application/my-votes?address=${wallet.address}`) : Promise.resolve(null),
            wallet ? apiGet(`/rc/executive/am-i?address=${wallet.address}`) : Promise.resolve(null),
            apiGet("/executive-proposals/list"),
        ]);

        const decisionBallots = (proposals || [])
            .filter((p) => p.Open)
            .map((p) => ({
                type: "decision",
                id: p.ID,
                title: p.Title,
                description: p.Description,
                kind: p.Kind,
                paramKey: p.ParamKey,
                paramValue: p.ParamValue,
                targetVersion: p.TargetVersion,
                tokenSymbol: p.TokenSymbol,
                systemGenerated: p.SystemGenerated,
                yesVotes: p.YesVotes,
                noVotes: p.NoVotes,
                quorum: p.Quorum,
                deadline: p.Deadline,
                promotedUntil: p.PromotedUntil || 0,
                predictionTargetSymbol: p.PredictionTargetSymbol,
                predictionTargetPrice: p.PredictionTargetPrice,
                disputeModeratorAddress: p.DisputeModeratorAddress,
                disputeReferenceType: p.DisputeReferenceType,
                disputeReferenceId: p.DisputeReferenceID,
            }));

        const supportBallots = (applications || []).map((a) => ({
            type: "support",
            id: a.address,
            address: a.address,
            reputation: a.reputation,
            supportWeight: a.supportWeight,
            requiredWeight: a.requiredWeight,
            createdAt: a.createdAt,
        }));

        // Executive Ballots: only shown to the current wallet if it's
        // one of the top-N RC holders right now - the real gate is
        // always the apply-time isExecutive re-check server-side, this
        // is purely so a non-executive doesn't see an unusable form.
        const isExec = !!(amIExec && amIExec.isExecutive);
        const executiveBallots = isExec
            ? (execProposals || [])
                  .filter((p) => p.Open)
                  .map((p) => ({
                      type: "executive",
                      id: p.ID,
                      paramKey: p.ParamKey,
                      paramValue: p.ParamValue,
                      targetAddress: p.TargetAddress,
                      yesVotes: p.YesVotes,
                      noVotes: p.NoVotes,
                      deadline: p.Deadline,
                  }))
            : [];

        const ballots = sortBallots([...decisionBallots, ...supportBallots, ...executiveBallots]);

        if (ballots.length === 0) {
            listEl.innerHTML = "No open ballots right now.";
        } else {
            listEl.innerHTML = ballots
                .map((b) => {
                    if (b.type === "decision") {
                        const totalWeight = b.yesVotes + b.noVotes;
                        const quorumPct = b.quorum > 0 ? Math.min(100, Math.round((totalWeight / b.quorum) * 100)) : 0;
                        const remainingMs = b.deadline ? (b.deadline * 1000 - Date.now()) : 0;
                        const deadlineLine = b.deadline
                            ? `Voting ends ${new Date(b.deadline * 1000).toLocaleString()}${remainingMs > 0 ? " (" + formatCountdown(remainingMs) + " left)" : " (closing soon)"}`
                            : "";
                        const systemBadge = b.systemGenerated ? `<span style="opacity:0.7;color:#16B8A6;">[System-generated]</span>` : "";
                        const promoted = b.promotedUntil > Date.now() / 1000;
                        const promotedBadge = promoted
                            ? `<span style="opacity:0.85;color:#16B8A6;">[Promoted, ${formatCountdown(b.promotedUntil * 1000 - Date.now())} left]</span>`
                            : "";

                        if (b.kind === "PREDICTION") {
                            return `
                                <div style="border-bottom:1px solid rgba(255,255,255,0.08);padding:10px 0;">
                                    <span style="opacity:0.7;font-size:11px;text-transform:uppercase;">Prediction / RC Mini-Bet</span> ${systemBadge} ${promotedBadge}
                                    <h4 style="margin:2px 0;">${escapeHtml(b.title)} ${ballotKindBadge(b.kind, b)}</h4>
                                    <p style="margin:0 0 4px;font-size:13px;">${escapeHtml(b.description)}</p>
                                    <p style="margin:0;font-size:12px;">YES: ${b.yesVotes} &middot; NO: ${b.noVotes}</p>
                                    <p style="opacity:0.6;font-size:12px;margin:2px 0;">Quorum: ${totalWeight}/${b.quorum} weight (${quorumPct}%) &middot; ${deadlineLine}</p>
                                    <div id="predPool-${b.id}" style="margin:6px 0;">Loading bet pool...</div>
                                    <select id="predSide-${b.id}">
                                        <option value="YES">YES (target met)</option>
                                        <option value="NO">NO (target not met)</option>
                                    </select>
                                    <input type="number" min="0" placeholder="RC to back this side (optional)" id="predRcAmount-${b.id}" style="width:220px;">
                                    <button onclick="castPredictionVote('${b.id}')">Cast Vote</button>
                                    <span style="margin-left:10px;">
                                        <input type="number" min="1" placeholder="RC" id="promoteAmount-${b.id}" style="width:70px;">
                                        <button onclick="promoteProposal('${b.id}')">Promote</button>
                                    </span>
                                </div>
                            `;
                        }

                        return `
                            <div style="border-bottom:1px solid rgba(255,255,255,0.08);padding:10px 0;">
                                <span style="opacity:0.7;font-size:11px;text-transform:uppercase;">Decision Vote</span> ${systemBadge} ${promotedBadge}
                                <h4 style="margin:2px 0;">${escapeHtml(b.title)} ${ballotKindBadge(b.kind, b)}</h4>
                                <p style="margin:0 0 4px;font-size:13px;">${escapeHtml(b.description)}</p>
                                <p style="margin:0;font-size:12px;">YES: ${b.yesVotes} &middot; NO: ${b.noVotes}</p>
                                <p style="opacity:0.6;font-size:12px;margin:2px 0;">Quorum: ${totalWeight}/${b.quorum} weight (${quorumPct}%) &middot; ${deadlineLine}</p>
                                <button onclick="vote('${b.id}', true)">Vote Yes</button>
                                <button onclick="vote('${b.id}', false)">Vote No</button>
                                <span style="margin-left:10px;">
                                    <input type="number" min="1" placeholder="RC" id="promoteAmount-${b.id}" style="width:70px;">
                                    <button onclick="promoteProposal('${b.id}')">Promote</button>
                                </span>
                            </div>
                        `;
                    }

                    if (b.type === "executive") {
                        const isModDeactivate = b.paramKey === "ModeratorDeactivate";
                        const title = isModDeactivate
                            ? `Deactivate moderator ${b.targetAddress}`
                            : `Set ${b.paramKey} &rarr; ${b.paramValue}`;
                        return `
                            <div style="border-bottom:1px solid rgba(255,255,255,0.08);padding:10px 0;">
                                <span style="opacity:0.7;font-size:11px;text-transform:uppercase;">${isModDeactivate ? "Executive Penalty Vote" : "Executive Vote"}</span>
                                <h4 style="margin:2px 0;">${title}</h4>
                                <p style="margin:0;font-size:12px;">YES: ${b.yesVotes} &middot; NO: ${b.noVotes}</p>
                                <p style="opacity:0.6;font-size:12px;margin:2px 0;">${b.deadline ? "Voting ends " + new Date(b.deadline * 1000).toLocaleString() : ""}</p>
                                <button onclick="voteExecutiveProposal('${b.id}', true)">Vote Yes</button>
                                <button onclick="voteExecutiveProposal('${b.id}', false)">Vote No</button>
                            </div>
                        `;
                    }

                    const pct = b.requiredWeight > 0 ? Math.min(100, Math.round((b.supportWeight / b.requiredWeight) * 100)) : 0;
                    const disabled = !myVotes || !myVotes.eligible || myVotes.votesRemaining <= 0;
                    const reason = !myVotes || !myVotes.eligible
                        ? "not eligible"
                        : myVotes.votesRemaining <= 0
                          ? "no votes left"
                          : "";
                    return `
                        <div style="border-bottom:1px solid rgba(255,255,255,0.08);padding:10px 0;">
                            <span style="opacity:0.7;font-size:11px;text-transform:uppercase;">Support Vote</span>
                            <h4 style="margin:2px 0;">Support ${b.address} as validator</h4>
                            <p style="margin:0;font-size:12px;opacity:0.75;">Reputation: ${b.reputation}</p>
                            <p style="opacity:0.6;font-size:12px;margin:2px 0;">${b.supportWeight} / ${b.requiredWeight} support weight (${pct}%)</p>
                            <button ${disabled ? "disabled" : ""} onclick="supportValidatorApplication('${b.address}')">
                                Support${reason ? ` (${reason})` : ""}
                            </button>
                        </div>
                    `;
                })
                .join("");
        }

        const execCard = document.getElementById("executiveCreateCard");
        if (execCard) execCard.style.display = isExec ? "block" : "none";

        // Live bet-pool bars are loaded separately, after the ballot
        // markup itself is in the DOM - "the pool should be visible,"
        // per the prediction-market feature's own requirement.
        decisionBallots
            .filter((b) => b.kind === "PREDICTION")
            .forEach((b) => loadPredictionPool(b.id));
    } catch (e) {
        console.error(e);
        listEl.innerHTML = "Could not load voting ballots.";
    }
}

async function loadPredictionPool(proposalId) {
    const el = document.getElementById(`predPool-${proposalId}`);
    if (!el) return;
    try {
        const pool = await apiGet(`/predictions/pool?proposalId=${proposalId}`);
        const yesRC = pool.YesPoolRC || 0;
        const noRC = pool.NoPoolRC || 0;
        const total = yesRC + noRC;
        if (total === 0) {
            el.innerHTML = `<span style="opacity:0.6;font-size:12px;">No RC bets placed yet - the pool forms the moment the first RC-backed vote lands.</span>`;
            return;
        }
        const yesPct = Math.round((yesRC / total) * 100);
        let html = `
            <div style="display:flex;justify-content:space-between;font-size:12px;opacity:0.8;">
                <span>YES pool: ${yesRC} RC</span>
                <span>NO pool: ${noRC} RC</span>
            </div>
            <div style="background:rgba(255,255,255,0.08);border-radius:4px;height:8px;overflow:hidden;margin:4px 0;">
                <div style="background:#4c8bf5;height:100%;width:${yesPct}%;"></div>
            </div>
        `;
        if (pool.Resolved) {
            html += `<p style="font-size:12px;color:#16B8A6;margin:2px 0;">Resolved - winning side: ${pool.WinningSide}${pool.BonusMinted ? ` (patience bonus minted: ${pool.BonusMinted} RC)` : ""}</p>`;
            if (wallet && Array.isArray(pool.Infusions)) {
                const mine = pool.Infusions.find((i) => i.Address === wallet.address);
                if (mine) {
                    const won = mine.Side === pool.WinningSide;
                    html += `<p style="font-size:12px;opacity:0.8;">Your stake: ${mine.RCAmount} RC on ${mine.Side} - ${won ? "won, payout minted to your RC balance" : "lost, RC not returned"}.</p>`;
                }
            }
        }
        el.innerHTML = html;
    } catch (e) {
        console.error(e);
        el.innerHTML = `<span style="opacity:0.6;font-size:12px;">Could not load bet pool.</span>`;
    }
}

async function castPredictionVote(proposalId) {
    if (!wallet) return;
    const side = document.getElementById(`predSide-${proposalId}`).value;
    const rcInput = document.getElementById(`predRcAmount-${proposalId}`);
    const rcAmount = parseInt(rcInput.value, 10) || 0;

    try {
        await apiPostSigned("/predictions/vote", { proposalId, side, rcAmount });
        alert(rcAmount > 0
            ? `Vote + ${rcAmount} RC bet submitted - pending confirmation in the next block.`
            : "Vote submitted - pending confirmation in the next block.");
        loadVotingBallots();
    } catch (e) {
        alert(e.message || "Could not cast this vote.");
    }
}

async function promoteProposal(proposalId) {
    if (!wallet) return;
    const input = document.getElementById(`promoteAmount-${proposalId}`);
    const amount = parseInt(input.value, 10);
    if (!amount || amount <= 0) {
        alert("Enter a positive RC amount to promote with.");
        return;
    }
    try {
        await apiPostSigned("/proposals/promote", { proposalId, rcAmount: amount });
        alert("Promotion submitted - pending confirmation in the next block.");
        loadVotingBallots();
    } catch (e) {
        alert(e.message || "Could not promote this proposal.");
    }
}

async function voteExecutiveProposal(proposalId, support) {
    if (!wallet) return;
    try {
        await apiPostSigned("/executive-proposals/vote", { proposalId, support });
        loadVotingBallots();
    } catch (e) {
        alert(e.message || "Vote failed.");
    }
}

async function createExecutiveProposal() {
    if (!wallet) return;
    const paramKey = document.getElementById("execParamKey").value;
    const input = document.getElementById("execParamValue");
    const value = parseInt(input.value, 10);
    if (isNaN(value) || value < 0) {
        alert("Enter a non-negative value.");
        return;
    }
    try {
        await apiPostSigned("/executive-proposals/create", { paramKey, paramValue: value });
        input.value = "";
        alert("Executive proposal submitted - pending confirmation in the next block.");
        loadVotingBallots();
    } catch (e) {
        alert(e.message || "Could not create executive proposal.");
    }
}

async function createModeratorDeactivateProposal() {
    if (!wallet) return;
    const input = document.getElementById("execModeratorTarget");
    const targetAddress = (input.value || "").trim();
    if (!targetAddress) {
        alert("Enter the moderator's address to propose deactivating.");
        return;
    }
    try {
        await apiPostSigned("/executive-proposals/create", { paramKey: "ModeratorDeactivate", targetAddress });
        input.value = "";
        alert("Moderator deactivation proposal submitted - pending confirmation in the next block.");
        loadVotingBallots();
    } catch (e) {
        alert(e.message || "Could not create moderator deactivation proposal.");
    }
}

render();
checkGovernanceActivation();
loadStatusFeed();
loadAnnouncements();
loadUserLog();
loadIntendingValidators();
loadVotingBallots();

setInterval(render, 10000);
setInterval(loadStatusFeed, 10000);
setInterval(loadAnnouncements, 30000);
setInterval(loadIntendingValidators, 15000);
setInterval(loadVotingBallots, 10000);
