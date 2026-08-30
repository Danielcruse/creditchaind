// Device-level app lock for OUTERNET pages - separate from wallet key
// custody entirely. The landing page (index/register/login/whitepaper/
// faq/developers) stays passwordless since it carries no wallet state;
// every "app" page includes this script as its very first <body>
// script so the lock overlay covers the page before any wallet content
// can flash into view.
//
// This is a LOCAL, per-browser gate, not encryption: the wallet private
// key already sits unencrypted in localStorage (this project's existing,
// disclosed model - see website/js/wallet.js), and this script never
// touches it. The password only blocks casual access to an already-
// unlocked device/browser (e.g. someone picking up your unlocked phone),
// it does not protect against anyone who already has the recovery
// phrase or raw localStorage contents. Importing a different wallet or
// changing the active recovery phrase inside an already-unlocked session
// never re-prompts for this password - only closing and reopening the
// site (which clears the sessionStorage unlock flag) does.
(function () {
    "use strict";

    var LOCK_KEY = "cc_device_lock";
    var SESSION_KEY = "cc_device_unlocked";

    if (sessionStorage.getItem(SESSION_KEY) === "1") {
        return;
    }

    function getLock() {
        try {
            return JSON.parse(localStorage.getItem(LOCK_KEY) || "null");
        } catch (e) {
            return null;
        }
    }

    function setLock(lock) {
        localStorage.setItem(LOCK_KEY, JSON.stringify(lock));
    }

    async function sha256Hex(text) {
        var enc = new TextEncoder().encode(text);
        var digest = await crypto.subtle.digest("SHA-256", enc);
        return Array.from(new Uint8Array(digest))
            .map(function (b) {
                return b.toString(16).padStart(2, "0");
            })
            .join("");
    }

    function randomHex(byteLength) {
        var arr = new Uint8Array(byteLength);
        crypto.getRandomValues(arr);
        return Array.from(arr)
            .map(function (b) {
                return b.toString(16).padStart(2, "0");
            })
            .join("");
    }

    function escapeHtml(s) {
        var div = document.createElement("div");
        div.innerText = s;
        return div.innerHTML;
    }

    var overlay = document.createElement("div");
    overlay.id = "ccDeviceLockOverlay";
    overlay.style.cssText =
        "position:fixed;inset:0;z-index:999999;background:var(--surface-bg, #07111F);color:var(--text-on-dark, #E7ECF5);" +
        "display:flex;align-items:center;justify-content:center;font-family:sans-serif;" +
        "padding:20px;box-sizing:border-box;";
    // Appended to <html>, not <body> - this script is the very first
    // thing inside <body>, so nothing else has been parsed/painted yet;
    // the overlay fully covers whatever renders afterward.
    document.documentElement.appendChild(overlay);

    function render(html) {
        overlay.innerHTML = html;
    }

    function unlock() {
        sessionStorage.setItem(SESSION_KEY, "1");
        overlay.remove();
    }

    var boxStyle =
        "background:rgba(255,255,255,0.05);border:1px solid rgba(255,255,255,0.15);" +
        "border-radius:14px;padding:28px;max-width:360px;width:100%;text-align:center;box-sizing:border-box;";
    var inputStyle =
        "width:100%;box-sizing:border-box;padding:10px 12px;margin:8px 0;border-radius:8px;" +
        "border:1px solid rgba(255,255,255,0.2);background:rgba(255,255,255,0.05);color:#eee;font-size:14px;";
    var btnStyle =
        "width:100%;padding:10px;border-radius:8px;border:none;background:var(--brand-blue, #2F6BFF);color:#fff;" +
        "font-weight:600;cursor:pointer;margin-top:6px;font-size:14px;";
    var linkStyle = "color:var(--brand-teal, #16B8A6);cursor:pointer;font-size:12px;text-decoration:underline;";

    function showSetup() {
        render(
            '<div style="' +
                boxStyle +
                '">' +
                '<h2 style="margin-top:0;">Set a device password</h2>' +
                '<p style="opacity:0.75;font-size:13px;">This protects OUTERNET on this device only - it is ' +
                "not your wallet's private key and does not encrypt it. Anyone who already has your recovery " +
                "phrase can still restore your wallet elsewhere. This just stops someone who picks up this " +
                "device from opening your account.</p>" +
                '<input id="ccDlPass1" type="password" placeholder="New device password" style="' +
                inputStyle +
                '">' +
                '<input id="ccDlPass2" type="password" placeholder="Confirm password" style="' +
                inputStyle +
                '">' +
                '<input id="ccDlHintQ" type="text" placeholder="A memory hint question (e.g. first pet)" style="' +
                inputStyle +
                '">' +
                '<input id="ccDlHintA" type="text" placeholder="Answer to your hint" style="' +
                inputStyle +
                '">' +
                '<button id="ccDlSetupBtn" style="' +
                btnStyle +
                '">Set Password</button>' +
                '<p id="ccDlSetupError" style="color:#f88;font-size:12px;"></p>' +
                "</div>"
        );
        document.getElementById("ccDlSetupBtn").addEventListener("click", async function () {
            var p1 = document.getElementById("ccDlPass1").value;
            var p2 = document.getElementById("ccDlPass2").value;
            var hq = document.getElementById("ccDlHintQ").value.trim();
            var ha = document.getElementById("ccDlHintA").value.trim();
            var err = document.getElementById("ccDlSetupError");
            if (!p1 || p1.length < 4) {
                err.innerText = "Password must be at least 4 characters.";
                return;
            }
            if (p1 !== p2) {
                err.innerText = "Passwords do not match.";
                return;
            }
            if (!hq || !ha) {
                err.innerText = "Fill in a hint question and answer - this is how you recover if you forget your password.";
                return;
            }
            var salt = randomHex(16);
            var hash = await sha256Hex(salt + p1);
            var hintSalt = randomHex(16);
            var hintHash = await sha256Hex(hintSalt + ha.toLowerCase());
            setLock({
                salt: salt,
                hash: hash,
                hintQuestion: hq,
                hintSalt: hintSalt,
                hintHash: hintHash,
            });
            unlock();
        });
    }

    function showUnlock(lock) {
        render(
            '<div style="' +
                boxStyle +
                '">' +
                '<h2 style="margin-top:0;">Enter device password</h2>' +
                '<p style="opacity:0.75;font-size:13px;">This device is locked. Enter the password you set for OUTERNET on this browser.</p>' +
                '<input id="ccDlPass" type="password" placeholder="Device password" style="' +
                inputStyle +
                '">' +
                '<button id="ccDlUnlockBtn" style="' +
                btnStyle +
                '">Unlock</button>' +
                '<p id="ccDlUnlockError" style="color:#f88;font-size:12px;"></p>' +
                '<p style="margin-top:14px;"><span id="ccDlForgotLink" style="' +
                linkStyle +
                '">Forgot your password?</span></p>' +
                "</div>"
        );

        function tryUnlock() {
            var p = document.getElementById("ccDlPass").value;
            var err = document.getElementById("ccDlUnlockError");
            sha256Hex(lock.salt + p).then(function (hash) {
                if (hash === lock.hash) {
                    unlock();
                } else {
                    err.innerText = "Wrong password.";
                }
            });
        }

        document.getElementById("ccDlUnlockBtn").addEventListener("click", tryUnlock);
        document.getElementById("ccDlPass").addEventListener("keydown", function (e) {
            if (e.key === "Enter") tryUnlock();
        });
        document.getElementById("ccDlForgotLink").addEventListener("click", function () {
            showForgot(lock);
        });
    }

    function showForgot(lock) {
        render(
            '<div style="' +
                boxStyle +
                '">' +
                '<h2 style="margin-top:0;">Reset device password</h2>' +
                '<p style="opacity:0.75;font-size:13px;">Answer your memory hint to set a new device password. ' +
                "This does not touch your wallet or funds - it only resets this device's local lock.</p>" +
                "<p><strong>" +
                escapeHtml(lock.hintQuestion) +
                '</strong></p>' +
                '<input id="ccDlHintAnswer" type="text" placeholder="Your answer" style="' +
                inputStyle +
                '">' +
                '<button id="ccDlCheckHintBtn" style="' +
                btnStyle +
                '">Continue</button>' +
                '<p id="ccDlForgotError" style="color:#f88;font-size:12px;"></p>' +
                '<p style="margin-top:14px;"><span id="ccDlBackLink" style="' +
                linkStyle +
                '">Back to password entry</span></p>' +
                "</div>"
        );
        document.getElementById("ccDlBackLink").addEventListener("click", function () {
            showUnlock(lock);
        });
        document.getElementById("ccDlCheckHintBtn").addEventListener("click", async function () {
            var a = document.getElementById("ccDlHintAnswer").value.trim().toLowerCase();
            var err = document.getElementById("ccDlForgotError");
            var hash = await sha256Hex(lock.hintSalt + a);
            if (hash !== lock.hintHash) {
                err.innerText = "That doesn't match - try again.";
                return;
            }
            showSetup();
        });
    }

    var lock = getLock();
    if (!lock) {
        showSetup();
    } else {
        showUnlock(lock);
    }
})();
