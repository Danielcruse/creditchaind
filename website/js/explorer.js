let wallet =
    JSON.parse(
        localStorage.getItem(
            "wallet"
        )
    );

function loadExplorer() {

    let txDiv =
        document.getElementById(
            "transactions"
        );

    txDiv.innerHTML = "";

    if (
        wallet &&
        wallet.transactions
    ) {

        wallet.transactions
            .slice()
            .reverse()
            .forEach(tx => {

                txDiv.innerHTML +=
                    `<p>${tx}</p>`;
            });
    }

    let blockDiv =
        document.getElementById(
            "blocks"
        );

    blockDiv.innerHTML = "";

    let blocks =
        wallet.blocksProduced || 0;

    for (
        let i = blocks;
        i >= 1;
        i--
    ) {
        blockDiv.innerHTML +=
            `<p>Block #${i}</p>`;
    }
}

loadExplorer();

setInterval(
    loadExplorer,
    10000
);