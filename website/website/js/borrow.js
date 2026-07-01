let wallet =
    JSON.parse(
        localStorage.getItem(
            "wallet"
        )
    );

let loans =
    JSON.parse(
        localStorage.getItem(
            "loans"
        )
    ) || [];

function saveLoans() {

    localStorage.setItem(
        "loans",
        JSON.stringify(
            loans
        )
    );
}

function borrow() {

    let amount =
        parseFloat(
            document
                .getElementById(
                    "borrowAmount"
                )
                .value
        );

    if (
        isNaN(amount) ||
        amount <= 0
    ) {
        alert(
            "Invalid amount."
        );
        return;
    }

    let borrowPower =
        wallet.reputation * 10 +
        wallet.oeth * 100;

    if (
        amount >
        borrowPower
    ) {
        alert(
            "Borrow limit exceeded."
        );
        return;
    }

    loans.push({

        id:
            Date.now(),

        amount:
            amount,

        status:
            "Active"
    });

    wallet.cct += amount;

    wallet.transactions.push(
        "Borrowed " +
        amount +
        " CCT"
    );

    localStorage.setItem(
        "wallet",
        JSON.stringify(
            wallet
        )
    );

    saveLoans();

    render();

    alert(
        "Loan Created"
    );
}

function render() {

    let div =
        document.getElementById(
            "loanList"
        );

    div.innerHTML = "";

    if (
        loans.length === 0
    ) {

        div.innerHTML =
            "No Loans";

        return;
    }

    loans.forEach(
        loan => {

            div.innerHTML +=
                `
                <p>

                Loan:
                ${loan.amount}

                |

                ${loan.status}

                </p>
                `;
        }
    );
}

render();