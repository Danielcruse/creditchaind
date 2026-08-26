// vault-lock-gate.js - a non-dismissable prompt that appears whenever
// this wallet has real ETH sitting at its self-custodial address that
// hasn't yet been locked into ETH_CCC1. CreditChain's Marketplace only
// lists CCC1 balances (see p2p.html) - a real deposit has to be locked
// before it's usable there, sendable, or swappable. Multiple deposits
// aggregate into one pending figure (see checkVaultLockGate's balance
// read, which is always the address's CURRENT total, not a per-deposit
// queue) so a wallet that received several deposits while closed still
// only needs one signature to lock all of it at once - see
// vault-deposit-eth.js for the underlying deposit()-call signing this
// reuses.
//
// Deliberately has no close/X/skip control and doesn't dismiss on
// outside-click or Escape - custody never moves to CreditChain (signing
// happens entirely in this browser, same as every other flow in this
// app), only the UI is blocking. The one exception is a real submission
// failure (RPC down, insufficient gas, etc.) - a Retry button appears
// instead of a permanent dead end, since a transient technical failure
// must never brick access to the whole site.

const GATE_GAS_LIMIT_DEPOSIT = 150000n;
const GATE_ETH_CHAIN_ID = window.CC_NETWORK === "mainnet" ? 1 : 11155111;
const GATE_DUST_WEI = 1000000000000n; // 0.000001 ETH - not worth prompting for

function gateHdEthAccount() {
    try {
        const accounts = getHdAccounts();
        return accounts && accounts.eth ? accounts.eth : null;
    } catch (e) {
        return null;
    }
}

async function checkVaultLockGate() {
    const wallet = requireWallet();
    if (!wallet) return;
    const account = gateHdEthAccount();
    if (!account) return; // no self-custodial account yet - nothing to gate

    let selfInfo;
    try {
        selfInfo = await apiGet(`/eth-selfcustody/balance?address=${account.address}`);
    } catch (e) {
        return; // RPC unavailable - don't block the page over a transient read failure
    }

    const realWei = BigInt(selfInfo.weiBalance || "0");
    const gasReserveWei = GATE_GAS_LIMIT_DEPOSIT * BigInt(selfInfo.suggestedGasPriceWei || "0");
    const pendingWei = realWei > gasReserveWei ? realWei - gasReserveWei : 0n;
    if (pendingWei <= GATE_DUST_WEI) return;

    let vaultInfo;
    try {
        vaultInfo = await fetchVaultInfo("ETH");
    } catch (e) {
        return;
    }
    if (!vaultInfo.configured) return; // nowhere to lock it yet on this node

    showGateOverlay(wallet, account, selfInfo, vaultInfo, pendingWei);
}

function showGateOverlay(wallet, account, selfInfo, vaultInfo, pendingWei) {
    if (document.getElementById("vaultLockGateOverlay")) return; // already showing

    const pendingDisplay = smallestToDecimal(pendingWei.toString(), 18);

    const overlay = document.createElement("div");
    overlay.id = "vaultLockGateOverlay";
    overlay.style.cssText =
        "position:fixed;inset:0;background:rgba(0,0,0,0.75);z-index:99999;" +
        "display:flex;align-items:center;justify-content:center;padding:20px;";
    overlay.innerHTML = `
        <div style="background:#fff;color:#111827;border-radius:14px;max-width:420px;width:100%;padding:24px;text-align:center;">
            <h2 style="margin:0 0 10px;font-size:19px;">Received amount detected - credit it to your balance</h2>
            <p style="font-size:14px;opacity:0.8;margin:0 0 14px;line-height:1.5;">
                <strong>${pendingDisplay} ETH</strong> has been received at your address. Credit it to your
                CreditChain balance to make it usable - this is required before it can be sent, swapped, or listed
                on the Marketplace, which only shows CreditChain-network balances, not raw incoming deposits.
                Signing happens entirely in this browser - your private key never leaves it, and you keep full
                custody the whole time.
            </p>
            <p id="vaultLockGateStatus" style="font-size:13px;opacity:0.75;min-height:18px;margin:0 0 10px;"></p>
            <button id="vaultLockGateBtn" style="width:100%;padding:12px;border-radius:10px;border:none;background:#0284c7;color:#fff;font-size:15px;cursor:pointer;">
                Credit ${pendingDisplay} ETH to My Balance
            </button>
        </div>
    `;
    document.body.appendChild(overlay);
    document.body.style.overflow = "hidden";

    document.getElementById("vaultLockGateBtn").addEventListener("click", () => {
        performGateLock(wallet, account, selfInfo, vaultInfo, pendingWei);
    });
}

function hideGateOverlay() {
    const overlay = document.getElementById("vaultLockGateOverlay");
    if (overlay) overlay.remove();
    document.body.style.overflow = "";
}

async function performGateLock(wallet, account, selfInfo, vaultInfo, pendingWei) {
    const btn = document.getElementById("vaultLockGateBtn");
    const statusEl = document.getElementById("vaultLockGateStatus");
    btn.disabled = true;
    btn.innerText = "Signing...";

    try {
        const nonce = parseInt(selfInfo.nonce, 10);
        const data = buildDepositCalldata(wallet.address, wallet.publicKey);

        statusEl.innerText = "Signing...";
        const { rawTxHex } = await EthTx.signLegacyTx(account.privateKey, {
            nonce,
            gasPriceWei: selfInfo.suggestedGasPriceWei,
            gasLimit: Number(GATE_GAS_LIMIT_DEPOSIT),
            to: vaultInfo.depositAddress,
            valueWei: pendingWei.toString(),
            chainId: GATE_ETH_CHAIN_ID,
            data,
        });

        statusEl.innerText = "Broadcasting...";
        btn.innerText = "Broadcasting...";
        const result = await apiPost("/eth-selfcustody/broadcast", { rawTxHex });

        statusEl.innerText = `Credited to your CreditChain balance - waiting for validator confirmation (tx ${result.txHash}). You're free to continue.`;
        btn.innerText = "Done";
        setTimeout(hideGateOverlay, 2500);
    } catch (e) {
        // A technical failure (RPC down, stale nonce, etc.) gets a Retry,
        // never a dead end - see this file's top doc comment.
        statusEl.innerText = "Couldn't credit to your balance: " + e.message;
        btn.disabled = false;
        btn.innerText = "Retry";
    }
}

checkVaultLockGate();
