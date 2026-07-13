const wallet = requireWallet();

// Supports deep-linking a specific hub - e.g. companies.html?address=abc123
// (see p2p.html's sponsor credit line) - scrolls to and highlights that
// entry once the directory loads, rather than needing a separate
// per-company profile page.
const highlightAddress = new URLSearchParams(window.location.search).get("address");

async function loadCompanies() {
    const el = document.getElementById("companyList");

    try {
        const companies = await apiGet("/companies");

        if (!companies || companies.length === 0) {
            el.innerText = "No applications yet.";
            return;
        }

        el.innerHTML = companies
            .slice()
            .sort((a, b) => b.CreatedAt - a.CreatedAt)
            .map((c) => {
                const badge = c.Status === "VERIFIED" ? " ✅ Verified" : c.Status === "REJECTED" ? " ❌ Rejected" : " ⏳ Pending review";
                const isHighlighted = highlightAddress && c.Address === highlightAddress;
                const rowStyle = isHighlighted ? " style=\"border:1px solid #8ef;border-radius:8px;background:rgba(136,238,255,0.08);\"" : "";
                return `
                <div class="explorer-row" id="company-${c.Address}"${rowStyle}>
                    <span><strong>${c.Name}</strong>${badge}</span>
                    <span class="explorer-hash">${c.Address.slice(0, 8)}...${c.Address.slice(-6)}</span>
                    <span>${c.Description || ""}</span>
                </div>`;
            })
            .join("");

        if (highlightAddress) {
            const row = document.getElementById(`company-${highlightAddress}`);
            if (row) row.scrollIntoView({ behavior: "smooth", block: "center" });
        }
    } catch (e) {
        console.error(e);
        el.innerText = "Could not load the directory.";
    }
}

async function applyCompany() {
    if (!wallet) return;

    const name = document.getElementById("companyName").value.trim();
    const description = document.getElementById("companyDescription").value.trim();
    const documentNote = document.getElementById("companyDocumentNote").value.trim();
    const contactEmail = document.getElementById("companyContactEmail").value.trim();
    const statusEl = document.getElementById("companyApplyStatus");

    if (!name) {
        statusEl.innerText = "Enter a company / trading name.";
        return;
    }

    try {
        const result = await apiPostSigned("/companies/apply", { name, description, documentNote, contactEmail });
        statusEl.innerText = `Application pending (tx ${result.txId}) - a moderator will review it once the next block confirms it.`;
        await loadCompanies();
    } catch (e) {
        statusEl.innerText = "Could not submit application: " + e.message;
    }
}

// updateContactEmail lets an existing applicant/hub change where trade-
// attention alerts get sent - separate from the initial application
// since a hub's inbox may change long after it's already verified.
async function updateContactEmail() {
    if (!wallet) return;

    const contactEmail = document.getElementById("companyUpdateContactEmail").value.trim();
    const statusEl = document.getElementById("companyUpdateContactStatus");

    try {
        const result = await apiPostSigned("/companies/update-contact", { contactEmail });
        statusEl.innerText = `Saved (tx ${result.txId}) - takes effect once the next block confirms it.`;
    } catch (e) {
        statusEl.innerText = "Could not update: " + e.message;
    }
}

loadCompanies();
setInterval(loadCompanies, 15000);
