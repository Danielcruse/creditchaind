// Shared region list for OUTERNET registration/profile - broad
// geographic regions rather than individual countries, since this is
// just a coarse self-reported signal, not a legal jurisdiction.
const OUTERNET_REGIONS = [
    "Africa",
    "Caribbean",
    "Central Asia",
    "East Asia",
    "Europe",
    "Middle East",
    "North America",
    "Oceania",
    "South America",
    "South Asia",
    "Southeast Asia",
];

// populateRegionSelect fills a <select> with the shared region list,
// pre-selecting `selected` if it matches one of the options (e.g. a
// user's previously saved region when editing their profile).
function populateRegionSelect(selectEl, selected) {
    if (!selectEl) return;
    const options = [
        `<option value="" disabled${selected ? "" : " selected"}>Select region</option>`,
    ];
    OUTERNET_REGIONS.forEach((region) => {
        options.push(
            `<option value="${region}"${region === selected ? " selected" : ""}>${region}</option>`
        );
    });
    selectEl.innerHTML = options.join("");
}
