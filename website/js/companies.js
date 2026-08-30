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
                const rowStyle = isHighlighted ? " style=\"border:1px solid var(--cc-accent-gold, #16B8A6);border-radius:8px;background:rgba(22,184,166,0.08);\"" : "";
                return `
                <div class="explorer-row" id="company-${escapeHtml(c.Address)}"${rowStyle}>
                    <span><strong>${escapeHtml(c.Name)}</strong>${badge}</span>
                    <span class="explorer-hash">${c.Address.slice(0, 8)}...${c.Address.slice(-6)}</span>
                    <span>${escapeHtml(c.Description || "")}</span>
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

document.getElementById("hubTermsAck").addEventListener("change", (e) => {
    document.getElementById("companyApplyBtn").disabled = !e.target.checked;
});

async function applyCompany() {
    if (!wallet) return;

    const name = document.getElementById("companyName").value.trim();
    const description = document.getElementById("companyDescription").value.trim();
    const documentNote = document.getElementById("companyDocumentNote").value.trim();
    const contactEmail = document.getElementById("companyContactEmail").value.trim();
    const statusEl = document.getElementById("companyApplyStatus");

    if (!document.getElementById("hubTermsAck").checked) {
        statusEl.innerText = "Please confirm and agree to the Terms of the P2P Marketplace first.";
        return;
    }

    if (!name) {
        statusEl.innerText = "Enter a company / trading name.";
        return;
    }

    try {
        const result = await apiPostSigned("/companies/apply", { name, description, documentNote, contactEmail });
        statusEl.innerText = `Application pending (tx ${result.txId}) - a moderator will review it once the next block confirms it.`;
        await loadCompanies();
        // The tx above is only PENDING (not yet confirmed), so the new
        // application record isn't readable yet - give the next block a
        // moment before checking for it, same delay pattern this
        // codebase's other post-submit refreshes already use.
        setTimeout(loadMyApplication, 9000);
    } catch (e) {
        statusEl.innerText = "Could not submit application: " + e.message;
    }
}

// loadMyApplication shows the logged-in wallet's OWN application status -
// without this, a PENDING application with no certificate on file looks
// identical (from the applicant's side) to one just waiting on the
// moderator, with literally no way to discover that approval can never
// succeed until they upload one (see applyCompanyReview's certificate
// check in api/apply.go). Hidden entirely if this wallet never applied.
async function loadMyApplication() {
    if (!wallet) return;

    const card = document.getElementById("myApplicationCard");
    const statusEl = document.getElementById("myApplicationStatus");
    const uploadBox = document.getElementById("certificateUploadBox");

    try {
        const info = await apiGet(`/companies/status?address=${wallet.address}`);
        if (!info.exists) {
            card.style.display = "none";
            return;
        }

        card.style.display = "block";

        if (info.status === "VERIFIED") {
            statusEl.innerText = "✅ Verified.";
            uploadBox.style.display = "none";
        } else if (info.status === "REJECTED") {
            statusEl.innerText = "❌ Rejected.";
            uploadBox.style.display = "none";
        } else if (info.certificateUploaded) {
            statusEl.innerText = "⏳ Pending review - certificate submitted, waiting on a moderator.";
            uploadBox.style.display = "none";
        } else {
            statusEl.innerText = "⏳ Pending review - action needed from you before this can be approved:";
            uploadBox.style.display = "block";
        }
    } catch (e) {
        console.error(e);
        card.style.display = "none";
    }
}

// readFileAsBase64 strips the "data:...;base64," prefix
// FileReader.readAsDataURL always prepends - the backend
// (decodeAndValidateCertificate) expects raw base64 only.
function readFileAsBase64(file) {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result.split(",", 2)[1] || "");
        reader.onerror = () => reject(reader.error || new Error("could not read file"));
        reader.readAsDataURL(file);
    });
}

async function uploadCertificate() {
    if (!wallet) return;

    const input = document.getElementById("certificateFileInput");
    const statusEl = document.getElementById("certificateUploadStatus");
    const file = input.files && input.files[0];

    if (!file) {
        statusEl.innerText = "Choose a file first.";
        return;
    }
    if (file.size > 5 * 1024 * 1024) {
        statusEl.innerText = "File exceeds the 5 MB limit.";
        return;
    }

    statusEl.innerText = "Uploading...";
    try {
        const fileBase64 = await readFileAsBase64(file);
        const result = await apiPostSigned("/companies/upload-certificate", { fileName: file.name, fileBase64 });
        statusEl.innerText = `Uploaded (tx ${result.txId}) - a moderator can now review your application.`;
        input.value = "";
        setTimeout(loadMyApplication, 9000);
    } catch (e) {
        statusEl.innerText = "Could not upload: " + e.message;
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
loadMyApplication();
setInterval(loadMyApplication, 15000);
