// vault-deposit-eth.js - ETH/USDC "Lock in Vault" flow: builds and
// signs a real deposit()/depositERC20() contract call entirely client-
// side (self-custodial key, never leaves the browser), broadcasts via
// the existing /eth-selfcustody/broadcast relay, then polls
// /vault/lock-status until enough bridge validators have independently
// confirmed the lock and ETH_CCC1/USDC_CCC1 is minted. See
// website/js/vault.js for the shared calldata/status-polling helpers
// this reuses.

const vaultWallet = requireWallet();

const GAS_LIMIT_DEPOSIT = 150000n;
const GAS_LIMIT_APPROVE = 90000n;
const GAS_LIMIT_DEPOSIT_ERC20 = 220000n;
const ETH_CHAIN_ID = window.CC_NETWORK === "mainnet" ? 1 : 11155111;

function hdEthAccount() {
    const accounts = getHdAccounts();
    return accounts && accounts.eth ? accounts.eth : null;
}

let chain = "ETH";
let latestSelfCustodialInfo = null; // {weiBalance, nonce, suggestedGasPriceWei}
let latestVaultInfo = null;

function setChain(newChain) {
    chain = newChain;
    document.getElementById("chainEthBtn").classList.toggle("active", chain === "ETH");
    document.getElementById("chainUsdcBtn").classList.toggle("active", chain === "USDC");
    document.getElementById("amount").placeholder = chain === "ETH" ? "Amount in ETH" : "Amount in USDC";
    document.getElementById("formStatus").innerText = "";
    refresh();
}

async function refresh() {
    const info = VAULT_CHAIN_INFO[chain];
    const account = hdEthAccount();
    if (!account) {
        document.getElementById("fromAddress").innerText = "No self-custodial Ethereum account found - migrate your wallet first.";
        document.getElementById("realBalance").innerText = "N/A";
        return;
    }
    document.getElementById("fromAddress").innerText = account.address;

    try {
        const selfInfo = await apiGet(`/eth-selfcustody/balance?address=${account.address}`);
        latestSelfCustodialInfo = selfInfo;
        document.getElementById("realBalance").innerText = chain === "ETH"
            ? `${weiToDisplay(selfInfo.weiBalance, 18)} ETH`
            : "(USDC balance shown separately - this is your real ETH balance for gas)";
    } catch (e) {
        document.getElementById("realBalance").innerText = "Couldn't load balance (RPC unavailable)";
    }

    try {
        latestVaultInfo = await fetchVaultInfo(chain);
        document.getElementById("vaultAddress").innerText = latestVaultInfo.configured
            ? latestVaultInfo.depositAddress
            : "Not configured on this node yet.";
        document.getElementById("requiredThreshold").innerText = latestVaultInfo.requiredThreshold || "-";
        document.getElementById("requiredConfirmations").innerText = latestVaultInfo.requiredConfirmations || "-";
    } catch (e) {
        document.getElementById("vaultAddress").innerText = "Couldn't load vault info.";
    }
}

function weiToDisplay(weiString, decimals) {
    return smallestToDecimal(weiString || "0", decimals);
}

async function review() {
    const statusEl = document.getElementById("formStatus");
    const amountStr = document.getElementById("amount").value.trim();
    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    if (!latestVaultInfo || !latestVaultInfo.configured) {
        statusEl.innerText = "The vault isn't configured on this node yet.";
        return;
    }
    if (!latestSelfCustodialInfo) {
        statusEl.innerText = "Balance/gas info isn't loaded yet - wait a moment and try again.";
        return;
    }

    const info = VAULT_CHAIN_INFO[chain];
    const amountSmallest = decimalToSmallest(amountStr, info.decimals);

    document.getElementById("reviewChain").innerText = chain;
    document.getElementById("reviewAmount").innerText = `${amountStr} ${chain}`;

    const gasPriceWei = BigInt(latestSelfCustodialInfo.suggestedGasPriceWei);
    const gasLimit = chain === "ETH" ? GAS_LIMIT_DEPOSIT : (GAS_LIMIT_APPROVE + GAS_LIMIT_DEPOSIT_ERC20);
    const feeWei = gasLimit * gasPriceWei;
    document.getElementById("reviewFee").innerText = `~${weiToDisplay(feeWei.toString(), 18)} ETH (real network gas cost)`;

    document.getElementById("reviewCard").dataset.amountSmallest = amountSmallest;
    document.getElementById("formCard").style.display = "none";
    document.getElementById("reviewCard").style.display = "block";
}

function cancelReview() {
    document.getElementById("reviewCard").style.display = "none";
    document.getElementById("formCard").style.display = "block";
    document.getElementById("reviewStatus").innerText = "";
}

async function confirmDeposit() {
    const statusEl = document.getElementById("reviewStatus");
    const confirmBtn = document.getElementById("confirmBtn");
    const amountSmallest = document.getElementById("reviewCard").dataset.amountSmallest;

    const account = hdEthAccount();
    if (!account) {
        statusEl.innerText = "No self-custodial Ethereum account available.";
        return;
    }

    confirmBtn.disabled = true;

    try {
        let nonce = parseInt(latestSelfCustodialInfo.nonce, 10);
        const gasPriceWei = latestSelfCustodialInfo.suggestedGasPriceWei;

        let depositTxHash;
        if (chain === "ETH") {
            statusEl.innerText = "Signing lock transaction...";
            const data = buildDepositCalldata(vaultWallet.address, vaultWallet.publicKey);
            const { rawTxHex } = await EthTx.signLegacyTx(account.privateKey, {
                nonce,
                gasPriceWei,
                gasLimit: Number(GAS_LIMIT_DEPOSIT),
                to: latestVaultInfo.depositAddress,
                valueWei: amountSmallest,
                chainId: ETH_CHAIN_ID,
                data,
            });
            statusEl.innerText = "Broadcasting...";
            const result = await apiPost("/eth-selfcustody/broadcast", { rawTxHex });
            depositTxHash = result.txHash;
        } else {
            // USDC: approve() then depositERC20(), sequential nonces.
            statusEl.innerText = "Signing approval...";
            const approveData = buildErc20ApproveCalldata(latestVaultInfo.depositAddress, amountSmallest);
            const approveTx = await EthTx.signLegacyTx(account.privateKey, {
                nonce,
                gasPriceWei,
                gasLimit: Number(GAS_LIMIT_APPROVE),
                to: latestVaultInfo.tokenContractAddress,
                valueWei: "0",
                chainId: ETH_CHAIN_ID,
                data: approveData,
            });
            statusEl.innerText = "Broadcasting approval...";
            await apiPost("/eth-selfcustody/broadcast", { rawTxHex: approveTx.rawTxHex });
            nonce++;

            statusEl.innerText = "Signing lock transaction...";
            const depositData = buildDepositERC20Calldata(latestVaultInfo.tokenContractAddress, amountSmallest, vaultWallet.address, vaultWallet.publicKey);
            const depositTx = await EthTx.signLegacyTx(account.privateKey, {
                nonce,
                gasPriceWei,
                gasLimit: Number(GAS_LIMIT_DEPOSIT_ERC20),
                to: latestVaultInfo.depositAddress,
                valueWei: "0",
                chainId: ETH_CHAIN_ID,
                data: depositData,
            });
            statusEl.innerText = "Broadcasting lock transaction...";
            const result = await apiPost("/eth-selfcustody/broadcast", { rawTxHex: depositTx.rawTxHex });
            depositTxHash = result.txHash;
        }

        document.getElementById("reviewCard").style.display = "none";
        document.getElementById("progressCard").style.display = "block";
        document.getElementById("progressNote").innerText = `Locked - waiting for confirmation depth and validator attestations (tx ${depositTxHash}).`;
        document.getElementById("attestThreshold").innerText = latestVaultInfo.requiredThreshold;

        // externalIndex=0 assumes the Locked event is the only (or
        // first) event this transaction emits - true for a plain user-
        // initiated deposit call, which never itself triggers other
        // contract logs. If that assumption is ever wrong, this page's
        // status just never finds a match (fetchLockStatus's
        // found:false response) - the backend attestation itself is
        // completely unaffected (bridge validators read the REAL log
        // index off-chain and attest under the correct one regardless).
        pollUntil(
            () => fetchLockStatus(chain, depositTxHash, 0, { ccAddress: vaultWallet.address, publicKey: vaultWallet.publicKey, amountSmallestUnit: amountSmallest }),
            (status) => status.status === "MINTED",
            (status) => {
                document.getElementById("attestCount").innerText = status.attestations || 0;
                document.getElementById("attestStatusPill").innerText = status.status || "PENDING";
                if (status.status === "MINTED") {
                    document.getElementById("progressNote").innerText = `${VAULT_CHAIN_INFO[chain].ccc1Symbol} minted - your balance is updated.`;
                }
            },
            { intervalMs: 10000, maxAttempts: 180 }
        );
    } catch (e) {
        statusEl.innerText = "Lock failed: " + e.message;
        confirmBtn.disabled = false;
    }
}

// A caller can deep-link straight to Review with an amount pre-filled
// (see asset.js's "Lock now" pending-deposit banner, which links here
// as vault-deposit-eth.html?chain=ETH&amount=X) - still requires the
// user's own explicit "Confirm and Lock" click to actually sign
// anything, same as manual use of this page, just skipping the typing
// step. Falls back to today's plain ETH-selected, empty-form behavior
// when no query params are present.
async function initFromQuery() {
    const params = new URLSearchParams(window.location.search);
    setChain(params.get("chain") === "USDC" ? "USDC" : "ETH");
    const autoAmount = params.get("amount");
    if (!autoAmount) return;
    await refresh(); // setChain already called refresh(), but review() needs it to have finished
    document.getElementById("amount").value = autoAmount;
    review();
}
initFromQuery();
