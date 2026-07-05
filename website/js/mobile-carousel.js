// Mobile-only carousel presentation for outernet.html's dashboard
// cards (Welcome, User Profile, Network Overview, Wallet, Recent
// Blocks, Recent Transactions, Notifications, Network Activity). At
// desktop widths this file changes nothing - .grid keeps its normal
// CSS grid layout from outernet.css and every .dash-panel is visible
// at once, same as before. Only below the same 900px breakpoint
// outernet.css already uses to stack the sidebar does this swap the
// long vertical scroll for one panel at a time with dot navigation,
// swipe/arrow controls, and a floating slide-in transition - the
// underlying elements outernet.js fills in (#height, #balance, etc.)
// are untouched either way, so data loading keeps working regardless
// of which panel is currently shown.
(function () {
    const mq = window.matchMedia("(max-width: 900px)");
    const panels = Array.from(document.querySelectorAll(".dash-panel"));
    if (panels.length === 0) return;

    let active = 0;
    let enabled = false;
    let nav = null;
    let dots = [];
    let prevBtn = null;
    let nextBtn = null;

    function ensureControls() {
        if (nav) return;

        nav = document.createElement("div");
        nav.className = "carousel-nav";
        panels.forEach((panel, i) => {
            const dot = document.createElement("button");
            dot.type = "button";
            dot.className = "carousel-dot";
            dot.innerText = panel.dataset.title || `Panel ${i + 1}`;
            dot.addEventListener("click", () => showPanel(i));
            nav.appendChild(dot);
            dots.push(dot);
        });

        const main = document.querySelector(".main");
        main.insertBefore(nav, main.firstChild);

        prevBtn = document.createElement("button");
        prevBtn.type = "button";
        prevBtn.className = "carousel-arrow prev";
        prevBtn.innerText = "‹";
        prevBtn.setAttribute("aria-label", "Previous panel");
        prevBtn.addEventListener("click", () => showPanel(active - 1));

        nextBtn = document.createElement("button");
        nextBtn.type = "button";
        nextBtn.className = "carousel-arrow next";
        nextBtn.innerText = "›";
        nextBtn.setAttribute("aria-label", "Next panel");
        nextBtn.addEventListener("click", () => showPanel(active + 1));

        document.body.appendChild(prevBtn);
        document.body.appendChild(nextBtn);

        // Swipe left/right anywhere in .main moves between panels; the
        // floating slide-in transition on the panel itself (see
        // outernet.css's panelFloatIn keyframes) is what gives the
        // up/down motion feel on each switch, independent of which
        // direction the swipe went.
        let touchStartX = null;
        main.addEventListener("touchstart", (e) => {
            touchStartX = e.touches[0].clientX;
        }, { passive: true });
        main.addEventListener("touchend", (e) => {
            if (touchStartX === null) return;
            const dx = e.changedTouches[0].clientX - touchStartX;
            touchStartX = null;
            if (Math.abs(dx) < 40) return;
            showPanel(dx < 0 ? active + 1 : active - 1);
        }, { passive: true });
    }

    function showPanel(index) {
        active = (index + panels.length) % panels.length;
        panels.forEach((panel, i) => {
            panel.classList.toggle("carousel-current", i === active);
        });
        dots.forEach((dot, i) => dot.classList.toggle("active", i === active));
    }

    function applyMode() {
        const shouldEnable = mq.matches;
        if (shouldEnable === enabled) return;
        enabled = shouldEnable;

        if (enabled) {
            ensureControls();
            document.body.classList.add("mobile-carousel");
            showPanel(active);
        } else {
            document.body.classList.remove("mobile-carousel");
            panels.forEach((panel) => panel.classList.remove("carousel-current"));
        }
    }

    applyMode();
    // matchMedia's change event is the standards-correct way to react
    // to crossing the breakpoint (covers window resize, orientation
    // change, and devtools viewport overrides in real browsers alike).
    // A plain window resize listener is kept alongside it as a
    // fallback for any environment where one fires without the other.
    mq.addEventListener("change", applyMode);
    window.addEventListener("resize", applyMode);
})();
