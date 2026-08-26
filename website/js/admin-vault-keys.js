// admin-vault-keys.js - page controller for admin-vault-keys.html.
// Wires the two localStorage key boxes (see vault-relay.js's
// saveVaultRelayerKey/loadVaultRelayerKey/clearVaultRelayerKey) and the
// client-side relay loop toggle. No CreditChain wallet login is required
// to use this page - unlike every other admin.html card, nothing here
// calls a CreditChain-authenticated endpoint; holding the real relayer
// key IS the access control (see vault-relay.js's top doc comment).

const currentNetwork = window.CC_NETWORK === "mainnet" ? "mainnet" : "testnet";
document.getElementById("currentNetworkDisplay").textContent = currentNetwork;

function refreshKeyStatus(network) {
    const key = loadVaultRelayerKey(network);
    const statusEl = document.getElementById(`${network}KeyStatus`);
    if (!key) {
        statusEl.textContent = `No key saved for ${network}.`;
        statusEl.className = "keyStatusEmpty";
        return;
    }
    try {
        const address = EthTx.addressFromPrivateKey(key);
        statusEl.textContent = `Key saved for ${network} - address ${address}`;
        statusEl.className = "keyStatusOk";
    } catch (e) {
        statusEl.textContent = `A key is saved for ${network}, but it doesn't look valid: ${e.message}`;
        statusEl.className = "";
    }
}

function saveKey(network) {
    const input = document.getElementById(`${network}KeyInput`);
    const value = input.value.trim();
    if (!value) return;
    try {
        EthTx.addressFromPrivateKey(value); // throws on malformed hex before saving anything
    } catch (e) {
        alert(`That doesn't look like a valid private key: ${e.message}`);
        return;
    }
    saveVaultRelayerKey(network, value);
    input.value = "";
    refreshKeyStatus(network);
    if (network === currentNetwork) refreshDerivedKeys();
}

function clearKey(network) {
    clearVaultRelayerKey(network);
    refreshKeyStatus(network);
    if (network === currentNetwork) refreshDerivedKeys();
}

refreshKeyStatus("testnet");
refreshKeyStatus("mainnet");

// --- bridge validator registration --------------------------------------
//
// Derives from the CURRENT network's saved relayer key (same key, reused -
// see the card's own doc text for why one secp256k1 key covers both the
// Ethereum and Bitcoin sides). Registration itself signs with whatever
// CreditChain wallet is logged into this browser (api.js's apiPostSigned) -
// completely separate credential from the relayer key, exactly as
// core/bridge_validator.go's doc comment requires.

function refreshDerivedKeys() {
    const key = loadVaultRelayerKey(currentNetwork);
    const ethEl = document.getElementById("derivedEthAddress");
    const btcEl = document.getElementById("derivedBtcPubkey");
    if (!key) {
        ethEl.textContent = `- (save a ${currentNetwork} relayer key above first)`;
        btcEl.textContent = "-";
        return;
    }
    try {
        ethEl.textContent = EthTx.addressFromPrivateKey(key);
        btcEl.textContent = EthTx.compressedPublicKeyFromPrivateKey(key);
    } catch (e) {
        ethEl.textContent = `invalid key: ${e.message}`;
        btcEl.textContent = "-";
    }
}

async function registerBridgeValidator() {
    const key = loadVaultRelayerKey(currentNetwork);
    const statusEl = document.getElementById("registerStatus");
    if (!key) {
        alert(`No ${currentNetwork} relayer key saved above yet.`);
        return;
    }

    let ethSigningAddress, btcPublicKeyHex;
    try {
        ethSigningAddress = EthTx.addressFromPrivateKey(key);
        btcPublicKeyHex = EthTx.compressedPublicKeyFromPrivateKey(key);
    } catch (e) {
        alert(`Could not derive keys: ${e.message}`);
        return;
    }

    statusEl.textContent = "Submitting (will prompt for wallet login if needed)...";
    statusEl.className = "keyStatusEmpty";
    try {
        const result = await apiPostSigned("/bridge-validator/register", { ethSigningAddress, btcPublicKeyHex });
        statusEl.textContent = `Submitted - txId ${result.txId || "(pending)"}. Confirms in the next block.`;
        statusEl.className = "keyStatusOk";
    } catch (e) {
        statusEl.textContent = `Failed: ${e.message || e}`;
        statusEl.className = "";
    }
}

refreshDerivedKeys();

// --- relay loop wiring --------------------------------------------------

let relayIntervalHandle = null;
const RELAY_POLL_MS = 30000;

function appendLog(line) {
    const el = document.getElementById("relayLog");
    const stamp = new Date().toLocaleTimeString();
    el.textContent = `[${stamp}] ${line}\n` + el.textContent;
}

async function tick() {
    try {
        await runVaultRelayOnce(currentNetwork, appendLog);
    } catch (e) {
        appendLog(`Relay tick failed: ${e.message || e}`);
    }
}

// --- lock attestation loop wiring ----------------------------------------
//
// Uses the currently-logged-in CreditChain wallet (apiPostSigned), not
// either relayer key box above - see vault-lock-attest.js's top doc
// comment for why this is a different credential from the release-
// approval signing step below.

let lockAttestIntervalHandle = null;
const LOCK_ATTEST_POLL_MS = 30000;

function appendLockAttestLog(line) {
    const el = document.getElementById("lockAttestLog");
    const stamp = new Date().toLocaleTimeString();
    el.textContent = `[${stamp}] ${line}\n` + el.textContent;
}

async function lockAttestTick() {
    try {
        await scanVaultEthLogsOnceFromBrowser(appendLockAttestLog);
    } catch (e) {
        appendLockAttestLog(`Scan failed: ${e.message || e}`);
    }
}

function toggleLockAttest() {
    const btn = document.getElementById("lockAttestToggleBtn");
    const statusEl = document.getElementById("lockAttestStatus");

    if (lockAttestIntervalHandle) {
        clearInterval(lockAttestIntervalHandle);
        lockAttestIntervalHandle = null;
        btn.textContent = "Start Watching";
        statusEl.textContent = "Stopped.";
        statusEl.className = "keyStatusEmpty";
        return;
    }

    btn.textContent = "Stop Watching";
    statusEl.textContent = `Running - checking every ${LOCK_ATTEST_POLL_MS / 1000}s while this tab stays open.`;
    statusEl.className = "keyStatusOk";
    appendLockAttestLog(`Lock attestation watcher started for ${currentNetwork}.`);
    lockAttestTick();
    lockAttestIntervalHandle = setInterval(lockAttestTick, LOCK_ATTEST_POLL_MS);
}

// ETH_LOCK_ATTEST_REWIND_BLOCKS is ~7 days back at Ethereum's ~12s
// block time - the default rewind depth when no specific block is
// given, generous enough to catch a deposit made before this browser
// ever ran the watcher (see the incident this button exists for:
// scanVaultEthLogsOnceFromBrowser's normal "first run" behavior starts
// from the current safe head, not history, so an earlier deposit is
// permanently invisible to it otherwise).
const ETH_LOCK_ATTEST_REWIND_BLOCKS = 50400;

// rescanLockAttestFromBlock rewinds the persisted scan cursor (see
// lockAttestScanStateKey) to an earlier block and immediately runs one
// scan tick from there - a manual recovery path for exactly the
// "deposit made before the watcher ever ran" gap, reachable without a
// browser console.
async function rescanLockAttestFromBlock() {
    const input = document.getElementById("lockAttestRescanBlockInput");
    const requested = input.value.trim();

    appendLockAttestLog("Rewinding scan cursor...");
    try {
        let targetBlock;
        if (requested) {
            targetBlock = parseInt(requested, 10);
            if (isNaN(targetBlock) || targetBlock < 0) {
                appendLockAttestLog("Enter a valid block number, or leave blank to rewind ~7 days.");
                return;
            }
        } else {
            const headHex = await ethRpcCall("eth_blockNumber", []);
            const head = parseInt(headHex, 16);
            targetBlock = Math.max(0, head - ETH_LOCK_ATTEST_REWIND_BLOCKS);
        }

        localStorage.setItem(lockAttestScanStateKey(), String(targetBlock - 1));
        appendLockAttestLog(`Cursor rewound to block ${targetBlock} - scanning now (this may take a moment for a wide range).`);
        input.value = "";
        await lockAttestTick();
    } catch (e) {
        appendLockAttestLog(`Rewind failed: ${e.message || e}`);
    }
}

// --- BTC lock attestation loop wiring -------------------------------------
//
// Mirrors the ETH/USDC lock attestation wiring above exactly, just against
// vault-btc-lock-attest.js's scanBtcVaultDepositsOnceFromBrowser instead -
// same currently-logged-in-CreditChain-wallet credential, same "only runs
// while this tab stays open" tradeoff.

let btcLockAttestIntervalHandle = null;
const BTC_LOCK_ATTEST_POLL_MS = 30000;

function appendBtcLockAttestLog(line) {
    const el = document.getElementById("btcLockAttestLog");
    const stamp = new Date().toLocaleTimeString();
    el.textContent = `[${stamp}] ${line}\n` + el.textContent;
}

async function btcLockAttestTick() {
    try {
        await scanBtcVaultDepositsOnceFromBrowser(appendBtcLockAttestLog);
    } catch (e) {
        appendBtcLockAttestLog(`Scan failed: ${e.message || e}`);
    }
}

function toggleBtcLockAttest() {
    const btn = document.getElementById("btcLockAttestToggleBtn");
    const statusEl = document.getElementById("btcLockAttestStatus");

    if (btcLockAttestIntervalHandle) {
        clearInterval(btcLockAttestIntervalHandle);
        btcLockAttestIntervalHandle = null;
        btn.textContent = "Start Watching";
        statusEl.textContent = "Stopped.";
        statusEl.className = "keyStatusEmpty";
        return;
    }

    btn.textContent = "Stop Watching";
    statusEl.textContent = `Running - checking every ${BTC_LOCK_ATTEST_POLL_MS / 1000}s while this tab stays open.`;
    statusEl.className = "keyStatusOk";
    appendBtcLockAttestLog(`BTC lock attestation watcher started for ${currentNetwork}.`);
    btcLockAttestTick();
    btcLockAttestIntervalHandle = setInterval(btcLockAttestTick, BTC_LOCK_ATTEST_POLL_MS);
}

// --- release approval signing -------------------------------------------
//
// Two SEPARATE credentials are involved, same split registerBridgeValidator
// already uses: the relayer key (saved above, secp256k1, used ONLY to
// compute the raw digest signature the contract's ecrecover checks) and
// the currently-logged-in CreditChain wallet (Ed25519, used to
// AUTHENTICATE the TxVaultReleaseApprove submission itself via
// apiPostSigned - api/vault_lock.go's HandleVaultReleaseApprove checks
// that THIS address is a registered bridge validator, not the relayer
// key's address).

function appendApprovalLog(line) {
    const el = document.getElementById("approvalLog");
    const stamp = new Date().toLocaleTimeString();
    el.textContent = `[${stamp}] ${line}\n` + el.textContent;
}

async function loadPendingApprovals() {
    const listEl = document.getElementById("pendingApprovalsList");
    listEl.textContent = "Loading...";
    try {
        const result = await apiGet("/vault/pending-approvals");
        const approvals = result.approvals || [];
        if (approvals.length === 0) {
            listEl.innerHTML = `<p style="opacity:0.6;font-size:12px;">Nothing pending.</p>`;
            return;
        }
        listEl.innerHTML = approvals
            .map((a) => {
                const info = VAULT_CHAIN_INFO[a.chain];
                const display = info ? smallestToDecimal(a.amountSmallestUnit, info.decimals) : a.amountSmallestUnit;
                return `
                    <div style="border-bottom:1px solid rgba(255,255,255,0.08);padding:8px 0;">
                        <p style="margin:0;font-size:12px;">
                            <strong>${a.chain}</strong> - ${display} to
                            <span style="font-family:monospace;">${a.destinationExtAddress}</span><br>
                            <span style="opacity:0.6;">burn tx ${a.burnTxId} - releaseId ${a.releaseId} - ${a.signatureCount} signature(s) so far</span>
                        </p>
                        <button onclick="signApproval(${JSON.stringify(a.burnTxId)}, ${JSON.stringify(a.chain)}, ${JSON.stringify(a.destinationExtAddress)}, ${JSON.stringify(a.amountSmallestUnit)}, ${a.releaseId})">Sign &amp; Approve</button>
                    </div>
                `;
            })
            .join("");
    } catch (e) {
        listEl.innerHTML = `<p style="opacity:0.6;font-size:12px;">Couldn't load: ${e.message}</p>`;
    }
}

async function signApproval(burnTxId, chain, destinationExtAddress, amountSmallestUnit, releaseId) {
    const key = loadVaultRelayerKey(currentNetwork);
    if (!key) {
        alert(`No ${currentNetwork} relayer key saved above yet.`);
        return;
    }

    try {
        let signatureHex;
        if (chain === "BTC") {
            // BTC has no abstract-digest shortcut - buildAndSignBtcVaultRelease
            // reconstructs the exact unsigned release transaction itself
            // (vault-btc-release-sign.js) and returns a DER signature, not
            // the ETH-style r||s||v this branch produces below.
            signatureHex = await buildAndSignBtcVaultRelease(key, {
                destinationExtAddress, amountSmallestUnit, releaseId,
            });
        } else {
            const vaultInfo = await fetchVaultInfo("ETH");
            if (!vaultInfo.configured) {
                appendApprovalLog("Vault contract not configured on this server yet.");
                return;
            }

            let tokenAddress = "0x0000000000000000000000000000000000000000";
            if (chain === "USDC") {
                const usdcInfo = await fetchVaultInfo("USDC");
                if (!usdcInfo.tokenContractAddress) {
                    appendApprovalLog("USDC contract address not available - cannot approve this release.");
                    return;
                }
                tokenAddress = usdcInfo.tokenContractAddress;
            }

            const chainId = currentNetwork === "mainnet" ? 1 : 11155111;
            const digest = buildReleaseApprovalDigest(
                destinationExtAddress, tokenAddress, amountSmallestUnit, releaseId, vaultInfo.depositAddress, chainId
            );
            signatureHex = (await EthTx.signDigest(key, digest)).replace(/^0x/i, "");
        }

        const result = await apiPostSigned("/vault/release-approve", { burnTxId, signatureHex });
        appendApprovalLog(`Signed and submitted approval for burn ${burnTxId} - tx ${result.txId}.`);
        loadPendingApprovals();
    } catch (e) {
        appendApprovalLog(`Failed to sign ${burnTxId}: ${e.message || e}`);
    }
}

// --- Vault validator-set governance (one-off action) ---------------------

// signAndSubmitSetValidators builds and signs contracts/CreditChainVault.sol's
// setValidators digest with the relayer key saved above (must belong to
// a CURRENT on-chain validator, or the real transaction will revert with
// "insufficient distinct valid signatures" - this only produces ONE
// signature, so it only works standalone while the contract's threshold
// is 1; a higher threshold needs this run once per required signer
// first, collecting signaturesHex externally, since this UI doesn't
// coordinate multiple browsers).
async function signAndSubmitSetValidators() {
    const key = loadVaultRelayerKey(currentNetwork);
    const statusEl = document.getElementById("setValidatorsStatus");
    if (!key) {
        statusEl.innerText = `No ${currentNetwork} relayer key saved above yet.`;
        return;
    }

    const newValidators = document
        .getElementById("newValidatorsInput")
        .value.split("\n")
        .map((line) => line.trim())
        .filter((line) => line.length > 0);
    const newThreshold = parseInt(document.getElementById("newThresholdInput").value, 10);

    if (newValidators.length === 0) {
        statusEl.innerText = "Enter at least one validator address.";
        return;
    }
    if (!newThreshold || newThreshold < 1 || newThreshold > newValidators.length) {
        statusEl.innerText = "Threshold must be between 1 and the number of validators.";
        return;
    }
    if (!confirm(
        `Replace the vault contract's real validator list with these ${newValidators.length} address(es) ` +
        `(threshold ${newThreshold})? This is a real on-chain transaction and cannot be undone by mistake - ` +
        `double check every address.`
    )) {
        return;
    }

    statusEl.innerText = "Signing...";
    try {
        const vaultInfo = await fetchVaultInfo("ETH");
        if (!vaultInfo.configured) {
            statusEl.innerText = "Vault contract not configured on this server yet.";
            return;
        }

        const chainId = currentNetwork === "mainnet" ? 1 : 11155111;
        // Date.now() lives in a wholly different numeric range from
        // vaultReleaseIDFor's hash-derived release IDs (see
        // api/vault_lock.go) - collision odds against the shared
        // usedReleaseIds nonce space are negligible without needing to
        // coordinate a nonce with the server first.
        const actionNonce = Date.now();

        const digest = buildSetValidatorsDigest(newValidators, newThreshold, actionNonce, vaultInfo.depositAddress, chainId);
        const signatureHex = (await EthTx.signDigest(key, digest)).replace(/^0x/i, "");

        const result = await apiPostSigned("/vault/set-validators-relay", {
            newValidators,
            newThreshold,
            actionNonce,
            signaturesHex: [signatureHex],
        });
        statusEl.innerText = `Submitted - real tx ${result.txHash}. Refresh a block explorer to confirm it landed.`;
    } catch (e) {
        statusEl.innerText = "Failed: " + (e.message || e);
    }
}

// signAndSubmitSetTokenAllowed/signAndSubmitSetPerDepositorCaps mirror
// signAndSubmitSetValidators exactly (same relayer-key/threshold-1
// caveat in its doc comment above) - the two prerequisites for the ETH
// liquid-staking feature: allow-listing Lido's real stETH as a
// depositable vault token, and zeroing the per-depositor cap that
// can't correctly evaluate an 18-decimal token (see
// api/vault_set_token_allowed_relay.go's own doc comment for why both
// are needed, not just the allowlist entry).
async function signAndSubmitSetTokenAllowed() {
    const key = loadVaultRelayerKey(currentNetwork);
    const statusEl = document.getElementById("setTokenAllowedStatus");
    if (!key) {
        statusEl.innerText = `No ${currentNetwork} relayer key saved above yet.`;
        return;
    }

    const token = document.getElementById("tokenAllowedAddressInput").value.trim();
    const allowed = document.getElementById("tokenAllowedCheckbox").checked;

    if (!token) {
        statusEl.innerText = "Enter the token's contract address first.";
        return;
    }
    if (!confirm(
        `${allowed ? "Allow" : "Disallow"} ${token} as a real vault deposit token? This is a real on-chain ` +
        `transaction and cannot be undone by mistake - double check the address.`
    )) {
        return;
    }

    statusEl.innerText = "Signing...";
    try {
        const vaultInfo = await fetchVaultInfo("ETH");
        if (!vaultInfo.configured) {
            statusEl.innerText = "Vault contract not configured on this server yet.";
            return;
        }

        const chainId = currentNetwork === "mainnet" ? 1 : 11155111;
        const actionNonce = Date.now();

        const digest = buildSetTokenAllowedDigest(token, allowed, actionNonce, vaultInfo.depositAddress, chainId);
        const signatureHex = (await EthTx.signDigest(key, digest)).replace(/^0x/i, "");

        const result = await apiPostSigned("/vault/set-token-allowed-relay", {
            token,
            allowed,
            actionNonce,
            signaturesHex: [signatureHex],
        });
        statusEl.innerText = `Submitted - real tx ${result.txHash}. Refresh a block explorer to confirm it landed.`;
    } catch (e) {
        statusEl.innerText = "Failed: " + (e.message || e);
    }
}

async function signAndSubmitSetPerDepositorCaps() {
    const key = loadVaultRelayerKey(currentNetwork);
    const statusEl = document.getElementById("setPerDepositorCapsStatus");
    if (!key) {
        statusEl.innerText = `No ${currentNetwork} relayer key saved above yet.`;
        return;
    }

    const newCapWei = document.getElementById("newCapWeiInput").value.trim() || "0";
    const newCapUsdc = document.getElementById("newCapUsdcInput").value.trim() || "0";

    if (!confirm(
        `Set the vault's per-depositor caps to ${newCapWei} wei / ${newCapUsdc} (6-decimal USDC-scaled) units? ` +
        `This applies to EVERY depositor and EVERY ERC20 token accepted through depositERC20(), not just one - ` +
        `0 disables that specific cap entirely. This is a real on-chain transaction and cannot be undone by mistake.`
    )) {
        return;
    }

    statusEl.innerText = "Signing...";
    try {
        const vaultInfo = await fetchVaultInfo("ETH");
        if (!vaultInfo.configured) {
            statusEl.innerText = "Vault contract not configured on this server yet.";
            return;
        }

        const chainId = currentNetwork === "mainnet" ? 1 : 11155111;
        const actionNonce = Date.now();

        const digest = buildSetPerDepositorCapsDigest(newCapWei, newCapUsdc, actionNonce, vaultInfo.depositAddress, chainId);
        const signatureHex = (await EthTx.signDigest(key, digest)).replace(/^0x/i, "");

        const result = await apiPostSigned("/vault/set-per-depositor-caps-relay", {
            newCapWei,
            newCapUsdc,
            actionNonce,
            signaturesHex: [signatureHex],
        });
        statusEl.innerText = `Submitted - real tx ${result.txHash}. Refresh a block explorer to confirm it landed.`;
    } catch (e) {
        statusEl.innerText = "Failed: " + (e.message || e);
    }
}

loadPendingApprovals();

function toggleRelay() {
    const btn = document.getElementById("relayToggleBtn");
    const statusEl = document.getElementById("relayStatus");

    if (relayIntervalHandle) {
        clearInterval(relayIntervalHandle);
        relayIntervalHandle = null;
        btn.textContent = "Start Relay";
        statusEl.textContent = "Stopped.";
        statusEl.className = "keyStatusEmpty";
        return;
    }

    if (!loadVaultRelayerKey(currentNetwork)) {
        alert(`No relayer key saved for ${currentNetwork} yet - save one above first.`);
        return;
    }

    btn.textContent = "Stop Relay";
    statusEl.textContent = `Running - checking every ${RELAY_POLL_MS / 1000}s while this tab stays open.`;
    statusEl.className = "keyStatusOk";
    appendLog(`Relay started for ${currentNetwork}.`);
    tick();
    relayIntervalHandle = setInterval(tick, RELAY_POLL_MS);
}

// --- auto-bridge relayer fees (ETH) --------------------------------------
//
// See the card's own doc text in admin-vault-keys.html for the full
// rationale. Threshold is value-based (~10 releases' worth of the current
// fee estimate), not a literal on-chain event counter - no cheap way to
// count discrete fee-credit events from the browser, and this serves the
// same purpose.
//
// IMPORTANT - two genuinely different credentials are involved, and they
// do NOT have to (and here, do not) belong to the same key:
//   - The relayer key (box above) is a raw secp256k1 key that may have no
//     CreditChain wallet behind it at all (e.g. exported from an exchange
//     like Coinbase, as this network's actually is) - resolveBalanceAddress
//     can never produce a CreditChain-wallet signature for an arbitrary
//     address like that, so burnForRelease could never unvault a balance
//     sitting AT the relayer's own address. Its address is used here only
//     as the RELEASE DESTINATION (where real ETH should land to refill
//     gas), never as something this code tries to sign with or hold a
//     balance at.
//   - RELAYER_FEE_RECIPIENT_ETH_ADDRESS (server-side, api/vault_fee.go)
//     is what actually accumulates ETH_CCC1 fees - it must be a real
//     CreditChain wallet's own self-custodial ETH address, so that
//     wallet's own signature can burn it. This ticks against whichever
//     CreditChain wallet is logged into THIS browser (getWallet(), a
//     read-only localStorage check - no redirect if absent) and burns
//     ITS balance - only works if that's the specific wallet the operator
//     configured as the fee recipient.

let autoBridgeIntervalHandle = null;
let autoBridgeInFlight = false;
const AUTO_BRIDGE_POLL_MS = 60000;
const AUTO_BRIDGE_FEE_MULTIPLE = 10n;

function appendAutoBridgeLog(line) {
    const el = document.getElementById("autoBridgeLog");
    const stamp = new Date().toLocaleTimeString();
    el.textContent = `[${stamp}] ${line}\n` + el.textContent;
}

async function autoBridgeTick() {
    if (autoBridgeInFlight) return;

    const key = loadVaultRelayerKey(currentNetwork);
    if (!key) {
        appendAutoBridgeLog("No relayer key saved above - stopping (needed to know the release destination).");
        toggleAutoBridge();
        return;
    }
    let releaseDestination;
    try {
        releaseDestination = EthTx.addressFromPrivateKey(key);
    } catch (e) {
        appendAutoBridgeLog(`Saved key is invalid: ${e.message}`);
        return;
    }

    const wallet = getWallet();
    if (!wallet) {
        appendAutoBridgeLog("No CreditChain wallet logged into this browser - stopping. Log in as the wallet configured to receive relayer fees (RELAYER_FEE_RECIPIENT_ETH_ADDRESS) and restart.");
        toggleAutoBridge();
        return;
    }

    try {
        const [ccc1Info, vaultInfo] = await Promise.all([
            fetchCcc1Balance(wallet, "ETH"),
            fetchVaultInfo("ETH"),
        ]);
        const balance = BigInt(ccc1Info.balance || "0");
        const perReleaseFee = BigInt(vaultInfo.estimatedReleaseFeeSmallestUnit || "0");
        if (perReleaseFee === 0n) {
            appendAutoBridgeLog("Vault not configured yet - skipping.");
            return;
        }
        const threshold = perReleaseFee * AUTO_BRIDGE_FEE_MULTIPLE;
        appendAutoBridgeLog(
            `Logged-in wallet's ETH_CCC1 (${ccc1Info.address}): ${smallestToDecimal(balance.toString(), 18)} accumulated ` +
            `(bridges at ${smallestToDecimal(threshold.toString(), 18)}, ~${AUTO_BRIDGE_FEE_MULTIPLE} releases' worth).`
        );
        if (balance < threshold || balance === 0n) return;

        autoBridgeInFlight = true;
        appendAutoBridgeLog(`Threshold reached - unvaulting ${smallestToDecimal(balance.toString(), 18)} ETH_CCC1, real ETH landing at ${releaseDestination}...`);
        const result = await burnForRelease("ETH", releaseDestination, balance.toString());
        appendAutoBridgeLog(`Submitted (burn tx ${result.txId}) - real ETH lands once validator signatures clear.`);
    } catch (e) {
        appendAutoBridgeLog(`Tick failed: ${e.message || e}`);
    } finally {
        autoBridgeInFlight = false;
    }
}

function toggleAutoBridge() {
    const btn = document.getElementById("autoBridgeToggleBtn");
    const statusEl = document.getElementById("autoBridgeStatus");

    if (autoBridgeIntervalHandle) {
        clearInterval(autoBridgeIntervalHandle);
        autoBridgeIntervalHandle = null;
        btn.textContent = "Start Auto-Bridge";
        statusEl.textContent = "Stopped.";
        statusEl.className = "keyStatusEmpty";
        return;
    }

    if (!loadVaultRelayerKey(currentNetwork)) {
        alert(`No relayer key saved for ${currentNetwork} yet - save one above first (needed for the release destination).`);
        return;
    }
    if (!getWallet()) {
        alert("No CreditChain wallet logged into this browser - log in as the wallet configured to receive relayer fees first.");
        return;
    }

    btn.textContent = "Stop Auto-Bridge";
    statusEl.textContent = `Running - checking every ${AUTO_BRIDGE_POLL_MS / 1000}s while this tab stays open.`;
    statusEl.className = "keyStatusOk";
    appendAutoBridgeLog(`Auto-bridge started for ${currentNetwork}.`);
    autoBridgeTick();
    autoBridgeIntervalHandle = setInterval(autoBridgeTick, AUTO_BRIDGE_POLL_MS);
}
