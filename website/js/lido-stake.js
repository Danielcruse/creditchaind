// lido-stake.js - the ETH liquid-staking flow's client-side
// orchestration: three separate, independently-signed real
// transactions (unvault ETH_CCC1 -> stake on Lido -> lock stETH into
// the vault), each one the user reviews and confirms on their own.
// Deliberately NOT auto-chained: real ETH/stETH sits in the user's own
// self-custodial wallet at every point in between, so stopping partway
// through never leaves funds stuck - see lido-stake.html's own intro
// card. Reuses the exact same signing/broadcast/poll helpers
// vault-deposit-eth.js and vault-withdraw.js already use.

const stakeWallet = requireWallet();

const GAS_LIMIT_SUBMIT = 120000n;
const GAS_LIMIT_APPROVE = 90000n;
const GAS_LIMIT_DEPOSIT_ERC20 = 220000n;
const ETH_CHAIN_ID = window.CC_NETWORK === "mainnet" ? 1 : 11155111;
const ETH_DECIMALS = 18;

function hdEthAccount() {
    const accounts = getHdAccounts();
    return accounts && accounts.eth ? accounts.eth : null;
}

let latestVaultInfoSteth = null; // fetchVaultInfo("STETH") - depositAddress, tokenContractAddress (Lido's real contract)

// --- Balances (refreshed at load and after each step completes) -----------

async function refreshBalances() {
    const account = hdEthAccount();
    if (!account) {
        document.getElementById("ethCcc1Balance").innerText = "No self-custodial Ethereum account found - migrate your wallet first.";
        return;
    }

    try {
        const { balance } = await fetchCcc1Balance(stakeWallet, "ETH");
        document.getElementById("ethCcc1Balance").innerText = `${smallestToDecimal(balance, ETH_DECIMALS)} ETH_CCC1`;
    } catch (e) {
        document.getElementById("ethCcc1Balance").innerText = "Couldn't load balance";
    }

    document.getElementById("stakeAddress").innerText = account.address;
    try {
        const info = await apiGet(`/eth-selfcustody/balance?address=${account.address}`);
        document.getElementById("realEthBalance").innerText = `${smallestToDecimal(info.weiBalance, ETH_DECIMALS)} ETH`;
    } catch (e) {
        document.getElementById("realEthBalance").innerText = "Couldn't load balance (RPC unavailable)";
    }

    try {
        latestVaultInfoSteth = await fetchVaultInfo("STETH");
        if (latestVaultInfoSteth.configured && latestVaultInfoSteth.tokenContractAddress) {
            const stethInfo = await apiGet(`/erc20/balance?address=${account.address}&contract=${latestVaultInfoSteth.tokenContractAddress}`);
            document.getElementById("stethBalance").innerText = `${smallestToDecimal(stethInfo.balance, ETH_DECIMALS)} stETH`;
        } else {
            document.getElementById("stethBalance").innerText = "Staking isn't enabled on this node yet.";
        }
    } catch (e) {
        document.getElementById("stethBalance").innerText = "Couldn't load balance (RPC unavailable)";
    }

    try {
        const { balance } = await fetchCcc1Balance(stakeWallet, "STETH");
        document.getElementById("stethCcc1Balance").innerText = `${smallestToDecimal(balance, ETH_DECIMALS)} STETH_CCC1`;
    } catch (e) {
        document.getElementById("stethCcc1Balance").innerText = "Couldn't load balance";
    }
}

// --- Step 1: Unvault ETH_CCC1 (real ETH lands back at your own address) ---

async function startUnvault() {
    const statusEl = document.getElementById("step1Status");
    const amountStr = document.getElementById("unvaultAmount").value.trim();
    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    const account = hdEthAccount();
    if (!account) {
        statusEl.innerText = "No self-custodial Ethereum account available.";
        return;
    }
    if (!confirm(`Unvault ${amountStr} ETH_CCC1? This burns your ETH_CCC1 and releases real ETH (minus the release fee) to your own self-custodial address ${account.address}.`)) return;

    statusEl.innerText = "Submitting burn...";
    try {
        const amountSmallest = decimalToSmallest(amountStr, ETH_DECIMALS);
        const result = await burnForRelease("ETH", account.address, amountSmallest);
        const burnTxId = result.txId;

        document.getElementById("step1Form").style.display = "none";
        document.getElementById("step1Progress").style.display = "block";
        document.getElementById("step1ProgressNote").innerText = `Burned - waiting for validator co-signatures (burn tx ${burnTxId}).`;

        const info = await fetchVaultInfo("ETH");
        document.getElementById("unvaultSigThreshold").innerText = info.requiredThreshold;

        pollUntil(
            () => fetchReleaseStatus(burnTxId),
            (status) => status.status === "RELEASED",
            (status) => {
                document.getElementById("unvaultSigCount").innerText = status.signatures || 0;
                document.getElementById("unvaultStatusPill").innerText = status.status || "PENDING";
                if (status.status === "RELEASED") {
                    document.getElementById("step1Title").classList.add("stake-step-done");
                    document.getElementById("step1ProgressNote").innerText =
                        "Released - real ETH has landed in your self-custodial wallet. Continue to step 2 below.";
                    refreshBalances();
                }
            },
            { intervalMs: 10000, maxAttempts: 240 }
        );
    } catch (e) {
        statusEl.innerText = "Unvault failed: " + e.message;
    }
}

// --- Step 2: Stake on Lido (real ETH -> real stETH, in your own wallet) ---

async function startLidoStake() {
    const statusEl = document.getElementById("step2Status");
    const amountStr = document.getElementById("stakeAmount").value.trim();
    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    const account = hdEthAccount();
    if (!account) {
        statusEl.innerText = "No self-custodial Ethereum account available.";
        return;
    }
    if (!latestVaultInfoSteth || !latestVaultInfoSteth.configured || !latestVaultInfoSteth.tokenContractAddress) {
        statusEl.innerText = "Staking isn't enabled on this node yet - the operator needs to allowlist Lido's stETH contract first.";
        return;
    }
    if (!confirm(`Stake ${amountStr} ETH on Lido? This sends real ETH to Lido's contract (${latestVaultInfoSteth.tokenContractAddress}) and mints real stETH directly into your own wallet.`)) return;

    statusEl.innerText = "Loading gas info...";
    try {
        const selfInfo = await apiGet(`/eth-selfcustody/balance?address=${account.address}`);
        const amountSmallest = decimalToSmallest(amountStr, ETH_DECIMALS);

        statusEl.innerText = "Signing Lido stake transaction...";
        const data = buildLidoSubmitCalldata(LIDO_ZERO_REFERRAL);
        const { rawTxHex } = await EthTx.signLegacyTx(account.privateKey, {
            nonce: parseInt(selfInfo.nonce, 10),
            gasPriceWei: selfInfo.suggestedGasPriceWei,
            gasLimit: Number(GAS_LIMIT_SUBMIT),
            to: latestVaultInfoSteth.tokenContractAddress,
            valueWei: amountSmallest,
            chainId: ETH_CHAIN_ID,
            data,
        });
        statusEl.innerText = "Broadcasting...";
        await apiPost("/eth-selfcustody/broadcast", { rawTxHex });

        document.getElementById("step2Form").style.display = "none";
        document.getElementById("step2Progress").style.display = "block";
        document.getElementById("step2ProgressNote").innerText = "Submitted - waiting for the stake transaction to confirm...";

        const startBalance = await apiGet(`/erc20/balance?address=${account.address}&contract=${latestVaultInfoSteth.tokenContractAddress}`);
        const startAmount = BigInt(startBalance.balance || "0");

        // pollUntil returns a plain setInterval handle, not a Promise -
        // "done" has to be handled inside onUpdate itself (same pattern
        // steps 1 and 3 use), not chained after the call.
        pollUntil(
            () => apiGet(`/erc20/balance?address=${account.address}&contract=${latestVaultInfoSteth.tokenContractAddress}`),
            (info) => BigInt(info.balance || "0") > startAmount,
            (info) => {
                const gained = BigInt(info.balance || "0") > startAmount;
                document.getElementById("step2ProgressNote").innerText = gained
                    ? "Staked - real stETH is in your wallet. Continue to step 3 below."
                    : `Current stETH balance: ${smallestToDecimal(info.balance, ETH_DECIMALS)} - waiting for the stake to confirm...`;
                if (gained) {
                    document.getElementById("step2Title").classList.add("stake-step-done");
                    refreshBalances();
                }
            },
            { intervalMs: 10000, maxAttempts: 60 }
        );
    } catch (e) {
        statusEl.innerText = "Stake failed: " + e.message;
    }
}

// --- Step 3: Lock stETH into the vault (mints STETH_CCC1) ------------------

async function startLidoLock() {
    const statusEl = document.getElementById("step3Status");
    const amountStr = document.getElementById("lockAmount").value.trim();
    const amount = parseFloat(amountStr);
    if (isNaN(amount) || amount <= 0) {
        statusEl.innerText = "Enter a valid amount.";
        return;
    }
    const account = hdEthAccount();
    if (!account) {
        statusEl.innerText = "No self-custodial Ethereum account available.";
        return;
    }
    if (!latestVaultInfoSteth || !latestVaultInfoSteth.configured) {
        statusEl.innerText = "Staking isn't enabled on this node yet.";
        return;
    }
    if (!confirm(`Lock ${amountStr} stETH into the vault? This approves and deposits your real stETH, minting STETH_CCC1 once enough validators confirm.`)) return;

    try {
        const selfInfo = await apiGet(`/eth-selfcustody/balance?address=${account.address}`);
        const amountSmallest = decimalToSmallest(amountStr, ETH_DECIMALS);
        let nonce = parseInt(selfInfo.nonce, 10);
        const gasPriceWei = selfInfo.suggestedGasPriceWei;

        statusEl.innerText = "Signing approval...";
        const approveData = buildErc20ApproveCalldata(latestVaultInfoSteth.depositAddress, amountSmallest);
        const approveTx = await EthTx.signLegacyTx(account.privateKey, {
            nonce,
            gasPriceWei,
            gasLimit: Number(GAS_LIMIT_APPROVE),
            to: latestVaultInfoSteth.tokenContractAddress,
            valueWei: "0",
            chainId: ETH_CHAIN_ID,
            data: approveData,
        });
        statusEl.innerText = "Broadcasting approval...";
        await apiPost("/eth-selfcustody/broadcast", { rawTxHex: approveTx.rawTxHex });
        nonce++;

        statusEl.innerText = "Signing lock transaction...";
        const depositData = buildDepositERC20Calldata(latestVaultInfoSteth.tokenContractAddress, amountSmallest, stakeWallet.address, stakeWallet.publicKey);
        const depositTx = await EthTx.signLegacyTx(account.privateKey, {
            nonce,
            gasPriceWei,
            gasLimit: Number(GAS_LIMIT_DEPOSIT_ERC20),
            to: latestVaultInfoSteth.depositAddress,
            valueWei: "0",
            chainId: ETH_CHAIN_ID,
            data: depositData,
        });
        statusEl.innerText = "Broadcasting lock transaction...";
        const result = await apiPost("/eth-selfcustody/broadcast", { rawTxHex: depositTx.rawTxHex });
        const depositTxHash = result.txHash;

        document.getElementById("step3Form").style.display = "none";
        document.getElementById("step3Progress").style.display = "block";
        document.getElementById("step3ProgressNote").innerText = `Locked - waiting for confirmation depth and validator attestations (tx ${depositTxHash}).`;
        document.getElementById("lockAttestThreshold").innerText = latestVaultInfoSteth.requiredThreshold;

        pollUntil(
            () => fetchLockStatus("STETH", depositTxHash, 0, { ccAddress: stakeWallet.address, publicKey: stakeWallet.publicKey, amountSmallestUnit: amountSmallest }),
            (status) => status.status === "MINTED",
            (status) => {
                document.getElementById("lockAttestCount").innerText = status.attestations || 0;
                document.getElementById("lockStatusPill").innerText = status.status || "PENDING";
                if (status.status === "MINTED") {
                    document.getElementById("step3Title").classList.add("stake-step-done");
                    document.getElementById("step3ProgressNote").innerText = "STETH_CCC1 minted - your balance is updated.";
                    refreshBalances();
                }
            },
            { intervalMs: 10000, maxAttempts: 180 }
        );
    } catch (e) {
        statusEl.innerText = "Lock failed: " + e.message;
    }
}

refreshBalances();
