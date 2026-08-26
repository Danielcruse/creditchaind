// vault-lock-attest.js - browser-only bridge-validator lock watcher for
// the ETH/USDC vault. This is the piece that was completely missing:
// without it, a real deposit to the vault contract just sits there
// forever - nothing ever independently confirms it happened, so
// applyVaultLockAttest's threshold is never reached and ETH_CCC1/
// USDC_CCC1 never mints. See core/vault_lock_attestation.go's doc
// comment for why this needs a REAL independent confirmation (scanning
// the real chain directly) rather than trusting a single report.
//
// Deliberately uses the CURRENTLY LOGGED-IN CreditChain wallet to submit
// TxVaultLockAttest (via apiPostSigned, the same auth every other signed
// action on this site already uses) - NOT the secp256k1 relayer key
// admin-vault-keys.html's key boxes hold. Attestation is authenticated
// as "this CreditChain wallet address says it independently observed
// this event" (api/vault_lock.go's isBridgeValidatorAddress check), a
// completely different credential from the relayer key that later signs
// a RELEASE digest (see vault.js's buildReleaseApprovalDigest /
// admin-vault-keys.js's signApproval) - no private key handling in this
// file at all.
//
// Scope: ETH/USDC only, mirroring api/vault_eth_watch.go exactly (same
// event, same confirmation-depth logic, just run from a public RPC
// instead of a dedicated node connection). BTC lock attestation
// (api/vault_btc_watch.go's OP_RETURN memo scanning) is a materially
// different mechanism and is NOT covered here - deposits to the BTC
// vault address still won't mint BTC_CCC1 until that's built separately.

// A single free public RPC is not reliable long-term for eth_getLogs
// specifically - providers frequently gate log queries behind a paid/
// "archive" tier, or cap the block range, even when eth_blockNumber
// stays free. A real incident directly probed every candidate below
// (2026-08) with an actual 400-block eth_getLogs call before trusting
// it: eth.drpc.org intermittently fails to route ("can't route your
// request to suitable provider"); ethereum-rpc.publicnode.com demands
// a paid token for this specific query shape; eth-mainnet.public.
// blastapi.io hard-caps at 10 blocks (useless at any real chunk size).
// rpc.mevblocker.io was the one that actually worked, confirmed up to
// the full 50400-block backlog window in a single call - kept first.
// Try each endpoint in order and fall through on failure, so one
// provider tightening its free tier doesn't silently stop attestation
// again the way it did here.
const ETH_PUBLIC_RPCS = window.CC_NETWORK === "mainnet"
    ? ["https://rpc.mevblocker.io", "https://eth.drpc.org", "https://ethereum-rpc.publicnode.com"]
    : ["https://ethereum-sepolia-rpc.publicnode.com", "https://sepolia.drpc.org"];

// ETH_GETLOGS_CHUNK_BLOCKS caps how many blocks a single eth_getLogs
// call spans - sized to comfortably fit every provider above (mevblocker
// proved fine up to 50400 in testing; publicnode's Sepolia endpoint caps
// at 50000; drpc's Sepolia endpoint caps at 10000) while still keeping
// each individual call reasonably sized. Chunking also means a long gap
// since the last successful scan (extended downtime, or backfilling a
// brand-new watcher - see ETH_LOCK_ATTEST_INITIAL_BACKLOG_BLOCKS below)
// makes steady, resumable progress instead of one all-or-nothing call.
const ETH_GETLOGS_CHUNK_BLOCKS = 2000;

let _ethRpcId = 0;
async function ethRpcCallOnce(rpcUrl, method, params) {
    const res = await fetch(rpcUrl, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ jsonrpc: "2.0", id: ++_ethRpcId, method, params }),
    });
    const body = await res.json();
    if (body.error) throw new Error(body.error.message || `RPC error calling ${method}`);
    return body.result;
}

async function ethRpcCall(method, params) {
    const errors = [];
    for (const rpcUrl of ETH_PUBLIC_RPCS) {
        try {
            return await ethRpcCallOnce(rpcUrl, method, params);
        } catch (e) {
            errors.push(`${rpcUrl}: ${e.message || e}`);
        }
    }
    throw new Error(`All public RPC endpoints failed for ${method} - ${errors.join(" | ")}`);
}

// lockedEventTopic0 mirrors api/vault_contract_abi.go's
// lockedEventSignature - keccak256 of the event's canonical signature
// string, computed here via HDCrypto.keccak256 the same way vault.js's
// abiSelector computes a function selector (just not truncated to 4
// bytes - an event topic is the full 32-byte hash).
function lockedEventTopic0() {
    return "0x" + HDCrypto.bytesToHex(HDCrypto.keccak256(new TextEncoder().encode("Locked(address,address,uint256,bytes20,bytes32)")));
}

// decodeLockedLog mirrors api/vault_contract_abi.go's decodeLockedEvent:
// topics[1]=depositor, topics[2]=token (both indexed, so they live in
// topics not data), data = amount(uint256) || creditChainAddress(bytes20,
// right-padded to fill its 32-byte word - same convention
// abiEncodeBytesN uses when BUILDING this shape) || creditChainPublicKey
// (bytes32, fills its word exactly).
function decodeLockedLog(log) {
    const dataBytes = hexToBytes(log.data.replace(/^0x/i, ""));
    const amount = HDCrypto.bytesToBigInt(dataBytes.slice(0, 32));
    const ccAddressBytes = dataBytes.slice(32, 52); // first 20 bytes of word 2
    const ccPubKeyBytes = dataBytes.slice(64, 96); // word 3, in full
    const tokenTopic = log.topics[2];
    const tokenAddress = "0x" + tokenTopic.slice(-40);
    return {
        amountSmallestUnit: amount.toString(),
        depositorCcAddressHex: HDCrypto.bytesToHex(ccAddressBytes),
        depositorPublicKeyHex: HDCrypto.bytesToHex(ccPubKeyBytes),
        tokenAddress,
        txHash: log.transactionHash,
        logIndex: parseInt(log.logIndex, 16),
    };
}

// lockAttestScanStateKey is per-network, mirroring
// api/vault_eth_watch.go's vaultEthLastScannedBlock - node-local watch
// progress, not consensus state (every validator, including this
// browser tab, independently tracks how far IT has scanned).
function lockAttestScanStateKey() {
    return `cc_vault_eth_lastscan_${window.CC_NETWORK}`;
}

async function scanVaultEthLogsOnceFromBrowser(log) {
    const vaultInfo = await fetchVaultInfo("ETH");
    if (!vaultInfo.configured) {
        log("Vault contract not configured on this server - nothing to scan.");
        return;
    }

    const headHex = await ethRpcCall("eth_blockNumber", []);
    const head = parseInt(headHex, 16);
    const confirmations = vaultInfo.requiredConfirmations || 12;
    if (head < confirmations) {
        log("Chain too young for the required confirmation depth yet.");
        return;
    }
    const safeHead = head - confirmations;

    // ETH_LOCK_ATTEST_INITIAL_BACKLOG_BLOCKS (~7 days at ~12s/block) is
    // how far back a genuinely first-ever run looks, instead of jumping
    // straight to the current block. A real incident caught this: a
    // deposit made before anyone had EVER clicked "Start Watching" in
    // this browser was permanently invisible to every later scan - the
    // old code treated "first run" as "start counting from right now,"
    // silently skipping anything already sitting on-chain. This doesn't
    // fully solve it (a deposit older than 7 days before the first-ever
    // click would still be missed - use the console rescan-from-block
    // workaround documented in this file's incident notes for that
    // case), but it closes the gap for realistic day-to-day delays
    // between a deposit and someone getting around to opening this page.
    const ETH_LOCK_ATTEST_INITIAL_BACKLOG_BLOCKS = 50400;

    const stateKey = lockAttestScanStateKey();
    let lastScanned = parseInt(localStorage.getItem(stateKey) || "0", 10);
    if (!lastScanned || lastScanned > safeHead) {
        lastScanned = Math.max(0, safeHead - ETH_LOCK_ATTEST_INITIAL_BACKLOG_BLOCKS);
        localStorage.setItem(stateKey, String(lastScanned));
        log(`First run - backfilling from block ${lastScanned} (~7 days back) instead of skipping straight to ${safeHead}.`);
    }
    if (safeHead <= lastScanned) {
        log("Nothing new to scan.");
        return;
    }

    const fromBlock = lastScanned + 1;
    const usdcInfo = await fetchVaultInfo("USDC").catch(() => null);
    const usdcAddr = usdcInfo && usdcInfo.tokenContractAddress ? usdcInfo.tokenContractAddress.toLowerCase() : null;

    // Chunked, not one eth_getLogs call for the whole [fromBlock,safeHead]
    // range - see ETH_GETLOGS_CHUNK_BLOCKS's doc comment. A long gap since
    // the last successful scan (extended downtime, or this tab's first
    // run after being closed a while) can span thousands of blocks; each
    // chunk still advances and persists lastScanned independently, so a
    // failure partway through a big catch-up isn't lost progress - the
    // next tick resumes from wherever it stopped.
    const logs = [];
    for (let chunkFrom = fromBlock; chunkFrom <= safeHead; chunkFrom += ETH_GETLOGS_CHUNK_BLOCKS) {
        const chunkTo = Math.min(chunkFrom + ETH_GETLOGS_CHUNK_BLOCKS - 1, safeHead);
        const chunkLogs = await ethRpcCall("eth_getLogs", [{
            fromBlock: "0x" + chunkFrom.toString(16),
            toBlock: "0x" + chunkTo.toString(16),
            address: vaultInfo.depositAddress,
            topics: [lockedEventTopic0()],
        }]);
        logs.push(...chunkLogs);
        localStorage.setItem(stateKey, String(chunkTo));
    }

    for (const l of logs) {
        const decoded = decodeLockedLog(l);
        let chain;
        if (/^0x0+$/i.test(decoded.tokenAddress)) {
            chain = "ETH";
        } else if (usdcAddr && decoded.tokenAddress.toLowerCase() === usdcAddr) {
            chain = "USDC";
        } else {
            log(`Locked event for unrecognized token ${decoded.tokenAddress} - skipping.`);
            continue;
        }

        try {
            const result = await apiPostSigned("/vault/lock-attest", {
                chain,
                externalTxHash: decoded.txHash,
                externalIndex: decoded.logIndex,
                depositorCcAddress: decoded.depositorCcAddressHex,
                depositorPublicKey: decoded.depositorPublicKeyHex,
                amountSmallestUnit: decoded.amountSmallestUnit,
            });
            log(`Attested ${chain} lock ${decoded.txHash} (${decoded.amountSmallestUnit} smallest units) - tx ${result.txId}.`);
        } catch (e) {
            log(`Failed to attest ${decoded.txHash}: ${e.message || e}`);
        }
    }

    // stateKey is already persisted per-chunk above (advances even if a
    // later chunk in this same tick fails) - nothing left to save here.
    if (logs.length === 0) log(`Scanned blocks ${fromBlock}-${safeHead} - no new locks.`);
}
