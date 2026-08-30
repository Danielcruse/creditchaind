// protocol-badge.js - marks a page as CreditChain protocol
// infrastructure, not an OUTERNET-owned feature. OUTERNET is the
// interface serving the protocol to users; the marketplace, earning
// services (moderation/validation), lending, staking, governance,
// swap/pool, and vaulting operations themselves are CreditChain's own
// on-chain infrastructure. This script is included on every page that
// also includes device-lock.js (the existing, established discriminator
// for "a real protocol/app page" vs. OUTERNET's own marketing/landing
// pages, which carry no wallet state and don't get this badge either -
// see device-lock.js's own doc comment).
(function () {
    "use strict";

    function insertBadge() {
        // Placed alongside device-lock.js at the very top of <body>
        // (deliberately, so the lock overlay can cover content before
        // paint) - meaning it executes before the page's own <h1> has
        // been parsed into the DOM yet. Deferring to DOMContentLoaded
        // (already-fired case handled too, for the rare page where
        // this script is injected/re-run after load) makes this work
        // regardless of where the script tag sits in the document.
        var h1 = document.querySelector("h1");
        if (!h1) return;

        var badge = document.createElement("span");
        badge.innerText = "CreditChain Protocol";
        badge.title = "This page operates CreditChain's own protocol infrastructure. OUTERNET is the interface serving it to you, not the operator.";
        badge.style.cssText =
            "display:inline-block;vertical-align:middle;margin-left:10px;font-size:11px;font-weight:600;" +
            "letter-spacing:0.3px;padding:2px 9px;border-radius:10px;" +
            "background:rgba(22,184,166,0.12);border:1px solid var(--cc-accent-gold, #16B8A6);" +
            "color:var(--cc-accent-gold, #16B8A6);white-space:nowrap;";

        h1.appendChild(document.createTextNode(" "));
        h1.appendChild(badge);
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", insertBadge);
    } else {
        insertBadge();
    }
})();
