(function () {
    const mq = window.matchMedia("(max-width: 900px)");

    function openDrawer() {
        document.body.classList.add("mobile-drawer-open");
    }

    function closeDrawer() {
        document.body.classList.remove("mobile-drawer-open");
    }

    window.openDrawer = openDrawer;
    window.closeDrawer = closeDrawer;

    function showTab(tabId) {
        document.querySelectorAll(".mobile-page").forEach((p) => {
            p.classList.toggle("mobile-page-active", p.dataset.tab === tabId);
        });
        document.querySelectorAll(".tabbar-btn").forEach((b) => {
            b.classList.toggle("active", b.dataset.tab === tabId);
        });
    }

    function applyMode(e) {
        if (e.matches) {
            document.body.classList.add("mobile-nav");
            showTab("home");
        } else {
            document.body.classList.remove("mobile-nav", "mobile-drawer-open");
        }
    }

    mq.addEventListener("change", applyMode);
    applyMode(mq);

    const toggleBtn = document.getElementById("drawerToggleBtn");
    if (toggleBtn) toggleBtn.addEventListener("click", openDrawer);

    const backdrop = document.getElementById("mobileDrawerBackdrop");
    if (backdrop) backdrop.addEventListener("click", closeDrawer);

    document.addEventListener("keydown", (e) => {
        if (e.key === "Escape") closeDrawer();
    });

    document.querySelectorAll(".tabbar-btn").forEach((btn) => {
        btn.addEventListener("click", () => showTab(btn.dataset.tab));
    });
})();
