// vault-relay.js - client-side, browser-only release relay for the
// ETH/USDC vault (contracts/CreditChainVault.sol's release() call).
//
// This is the intentional alternative to a server-held
// RELAYER_PRIVATE_KEY_HEX (api/vault_release_relay.go): the operator
// pastes their relayer key into admin-vault-keys.html, it's stored ONLY
// in this browser's localStorage, and this file signs+broadcasts
// release() calls entirely client-side using the exact same
// self-custodial primitives every other send flow in this app already
// uses (website/js/vendor/eth-tx.js's signLegacyTx,
// /eth-selfcustody/broadcast) - the key never reaches the server. The
// tradeoff, disclosed to the operator in the admin UI itself: this only
// runs while the admin page is open in a tab, unlike the server-side
// loop it replaces.
//
// Depends on api.js (apiGet/hexToBytes/bytesToHex), vault.js (abiSelector/
// abiEncodeAddress/abiEncodeUint256/concatUint8Arrays), vendor/hd-crypto.js,
// vendor/eth-tx.js.
//
// The relayer's own key carries ZERO authority over which releases are
// valid - contracts/CreditChainVault.sol's release() independently
// verifies each bridge-validator signature on-chain via ecrecover. This
// key only pays gas and assembles already-authorized calldata; a
// mistake here can waste gas or duplicate a broadcast (harmless -
// usedReleaseIds makes a second submission a no-op revert), never move
// funds it wasn't already cryptographically authorized to move.

// abiEncodeDynamicBytes encodes one `bytes` value: a length word followed
// by the raw bytes, right-padded with zeros to the next 32-byte boundary.
function abiEncodeDynamicBytes(bytes) {
    const lenWord = abiEncodeUint256(bytes.length);
    const paddedLen = Math.ceil(bytes.length / 32) * 32;
    const padded = new Uint8Array(paddedLen);
    padded.set(bytes, 0);
    return concatUint8Arrays(lenWord, padded);
}

// abiEncodeBytesArray encodes a `bytes[]` value per Solidity ABI rules:
// a length word, then one offset word per element (relative to the
// start of this array's own data, i.e. right after the length word),
// then each element's own bytes-encoding in order. This is the most
// complex ABI shape this codebase hand-encodes - cross-checked
// byte-for-byte against go-ethereum's real abi.Pack (see
// cmd/abioracle, deleted after use) before this file is trusted against
// any real release() call.
function abiEncodeBytesArray(byteArrays) {
    const n = byteArrays.length;
    const lengthWord = abiEncodeUint256(n);
    const encodedElements = byteArrays.map(abiEncodeDynamicBytes);
    const headSize = n * 32;
    let runningOffset = headSize;
    const offsetWords = [];
    for (const enc of encodedElements) {
        offsetWords.push(abiEncodeUint256(runningOffset));
        runningOffset += enc.length;
    }
    return concatUint8Arrays(lengthWord, ...offsetWords, ...encodedElements);
}

// buildVaultReleaseCalldata builds calldata for
// release(address,address,uint256,uint256,bytes[]) - mirrors
// api/vault_contract_abi.go's packReleaseCalldata exactly.
// signatureByteArrays is an array of Uint8Array, each a 65-byte
// [R||S||V] bridge-validator signature (already collected server-side
// via TxVaultReleaseApprove - this relay never produces or needs any
// signature itself, only assembles already-authorized ones).
function buildVaultReleaseCalldata(toAddressHex, tokenAddressHex, amountSmallestUnit, releaseId, signatureByteArrays) {
    const selector = abiSelector("release(address,address,uint256,uint256,bytes[])");
    const toBytes = hexToBytes(toAddressHex.replace(/^0x/i, ""));
    const tokenBytes = hexToBytes(tokenAddressHex.replace(/^0x/i, ""));
    const head = concatUint8Arrays(
        abiEncodeAddress(toBytes),
        abiEncodeAddress(tokenBytes),
        abiEncodeUint256(amountSmallestUnit),
        abiEncodeUint256(releaseId),
        abiEncodeUint256(160) // fixed offset to the dynamic tail: 5 head words * 32 bytes
    );
    const tail = abiEncodeBytesArray(signatureByteArrays);
    return concatUint8Arrays(selector, head, tail);
}

// --- localStorage key custody (browser-only, per network) -------------

function vaultRelayerKeyStorageKey(network) {
    return `cc_vault_relayer_key_${network}`;
}

function saveVaultRelayerKey(network, privateKeyHex) {
    localStorage.setItem(vaultRelayerKeyStorageKey(network), privateKeyHex.trim());
}

function loadVaultRelayerKey(network) {
    return localStorage.getItem(vaultRelayerKeyStorageKey(network)) || "";
}

function clearVaultRelayerKey(network) {
    localStorage.removeItem(vaultRelayerKeyStorageKey(network));
}

// --- the relay loop itself ---------------------------------------------

// fetchReleaseQueue reads the new public GET /vault/release-queue - every
// ETH/USDC release approval that has already reached bridge-validator
// signature threshold and is still waiting to actually broadcast. This
// is the client-side equivalent of scanVaultReleaseApprovalsOnce
// (api/vault_release_relay.go) - same read, just driven from the
// browser instead of a server goroutine.
async function fetchReleaseQueue() {
    return apiGet("/vault/release-queue");
}

// relayOneApproval builds, signs (with the in-memory relayer key - never
// written anywhere but this closure's argument), and broadcasts exactly
// one release() call, mirroring submitVaultRelease's shape.
async function relayOneApproval(approval, privateKeyHex, relayerAddress, vaultContractAddress, chainId, usdcContractAddress) {
    const token = approval.chain === "USDC" ? usdcContractAddress : "0x0000000000000000000000000000000000000000";
    const signatureBytes = approval.signatures.map(hexToBytes);
    const calldata = buildVaultReleaseCalldata(
        approval.destinationExtAddress,
        token,
        approval.amountSmallestUnit,
        approval.releaseId,
        signatureBytes
    );

    const info = await apiGet(`/eth-selfcustody/balance?address=${relayerAddress}`);
    const { rawTxHex } = await EthTx.signLegacyTx(privateKeyHex, {
        nonce: parseInt(info.nonce, 10),
        gasPriceWei: info.suggestedGasPriceWei,
        gasLimit: 300000,
        to: vaultContractAddress,
        valueWei: 0,
        chainId,
        data: calldata,
    });

    return apiPost("/eth-selfcustody/broadcast", { rawTxHex });
}

// runVaultRelayOnce scans the queue and relays every ready approval -
// called on an interval by the admin page while it's open, same
// degrade-gracefully posture as the server loop it replaces (no-ops
// quietly if the vault isn't configured or the queue is empty).
async function runVaultRelayOnce(network, log) {
    const privateKeyHex = loadVaultRelayerKey(network);
    if (!privateKeyHex) {
        log("No relayer key saved for " + network + " - nothing to do.");
        return;
    }

    const info = await fetchVaultInfo("ETH");
    if (!info.configured) {
        log("Vault contract not configured on this server yet - nothing to relay.");
        return;
    }

    const relayerAddress = EthTx.addressFromPrivateKey
        ? EthTx.addressFromPrivateKey(privateKeyHex)
        : null;
    if (!relayerAddress) {
        log("Could not derive relayer address from the saved key.");
        return;
    }

    const chainId = network === "mainnet" ? 1 : 11155111;
    let usdcAddr = null;
    try {
        const usdcInfo = await fetchVaultInfo("USDC");
        usdcAddr = usdcInfo.tokenContractAddress || null;
    } catch (e) { /* USDC info optional - ETH-only releases still work without it */ }

    const queue = await fetchReleaseQueue();
    if (!queue.approvals || queue.approvals.length === 0) {
        log("Queue empty - nothing ready to relay.");
        return;
    }

    for (const approval of queue.approvals) {
        if (approval.chain === "USDC" && !usdcAddr) {
            log(`Skipping ${approval.id}: USDC contract address not available.`);
            continue;
        }
        try {
            const result = await relayOneApproval(
                approval, privateKeyHex, relayerAddress,
                info.depositAddress, chainId, usdcAddr
            );
            log(`Relayed ${approval.chain} release ${approval.id} - tx ${result.txHash}`);
        } catch (e) {
            log(`Failed to relay ${approval.id}: ${e.message || e}`);
        }
    }
}
