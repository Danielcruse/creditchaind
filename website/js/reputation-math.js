function tierRow(tier) {
    return `
        <div class="explorer-row">
            <span>${tier.tier}</span>
            <span>Reputation ${tier.minReputation}+</span>
        </div>
    `;
}

function tierRewardRow(reward) {
    return `
        <div class="explorer-row">
            <span>Reach ${reward.tier}</span>
            <span style="color:#00ff95;font-weight:600;">+${reward.reptReward} REPT (one-time)</span>
        </div>
    `;
}

function mechanismRow(m) {
    const positive = m.delta > 0;
    const sign = positive ? "+" : "";
    const color = positive ? "#00ff95" : "#ff6b6b";
    const wiredBadge = m.wired
        ? ""
        : `<div style="margin-top:4px;"><span style="color:#ffb020;font-weight:600;font-size:11px;border:1px solid #ffb020;border-radius:8px;padding:1px 7px;">defined, not yet wired</span></div>`;
    return `
        <div class="explorer-row" style="flex-direction:column;align-items:flex-start;gap:4px;">
            <div style="display:flex;justify-content:space-between;width:100%;">
                <span><strong>${m.name}</strong> - ${m.target}</span>
                <span style="color:${color};font-weight:600;">${sign}${m.delta} rep</span>
            </div>
            <span style="opacity:0.75;font-size:12px;">${m.description}</span>
            ${wiredBadge}
        </div>
    `;
}

async function loadReputationMath() {
    try {
        const data = await apiGet("/reputation/math");

        document.getElementById("floor").innerText = data.floor;
        document.getElementById("ceiling").innerText = data.ceiling;

        document.getElementById("tiers").innerHTML = (data.tiers || []).map(tierRow).join("");
        document.getElementById("tierRewards").innerHTML = (data.tierRewards || []).map(tierRewardRow).join("");
        document.getElementById("mechanisms").innerHTML = (data.mechanisms || []).map(mechanismRow).join("");
    } catch (e) {
        console.error(e);
    }
}

loadReputationMath();
