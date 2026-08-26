// vault-btc-lock-attest.js - browser-only bridge-validator lock watcher
// for the BTC vault. BTC's sibling to vault-lock-attest.js (ETH/USDC) -
// see api/vault_btc_watch.go's scanBtcVaultDepositsOnce, which this
// mirrors exactly, just run from a public Esplora API instead of a
// dedicated node. Without this running, a real BTC deposit to the vault's
// shared P2WSH address just sits there forever - nothing independently
// confirms it happened, so BTC_CCC1 never mints.
//
// Same credential as the ETH/USDC watcher: submits TxVaultLockAttest
// signed by the CURRENTLY LOGGED-IN CreditChain wallet (apiPostSigned) -
// no relayer key needed for this part, that's only for release approval.
//
// Deliberately keeps no persisted "last scanned" cursor, same as the
// server-side loop's own doc comment explains: it re-examines the vault
// address's full current UTXO set every poll. Safe (vaultLockAttestationID
// dedups on chain+txid+vout, applyVaultLockAttest's per-validator
// already-attested guard makes a re-submission a harmless no-op) and
// simpler than a cursor for a small UTXO set.

function btcVaultApiBase() {
    return window.CC_NETWORK === "mainnet"
        ? "https://blockstream.info/api"
        : "https://blockstream.info/testnet/api";
}

async function btcVaultApiGet(path) {
    const res = await fetch(btcVaultApiBase() + path);
    if (!res.ok) {
        const text = await res.text().catch(() => "");
        throw new Error(`BTC API ${path} failed: ${res.status} ${text}`);
    }
    return res.json();
}

async function btcVaultChainTipHeight() {
    const res = await fetch(btcVaultApiBase() + "/blocks/tip/height");
    if (!res.ok) throw new Error(`BTC API /blocks/tip/height failed: ${res.status}`);
    const text = await res.text();
    return parseInt(text.trim(), 10);
}

// decodeBtcVaultDepositMemoFromVout mirrors api/vault_btc_watch.go's
// decodeBtcVaultDepositMemo - scans a tx's outputs for a single well-
// formed 52-byte OP_RETURN memo (direct-push encoding, matching
// website/js/vault.js's opReturnScriptPubKey, the exact encoding a
// depositor's own browser produces). Returns null if none/malformed -
// same "never guess" posture as the server: a deposit with no valid
// memo is never attestable.
function decodeBtcVaultDepositMemoFromVout(vout) {
    for (const out of vout) {
        if (out.scriptpubkey_type !== "op_return") continue;
        const script = hexToBytes(out.scriptpubkey);
        // OP_RETURN (0x6a) + direct-push length byte + data.
        if (script.length < 2 || script[0] !== 0x6a) continue;
        const pushLen = script[1];
        if (pushLen < 1 || pushLen > 75) continue; // only the direct-push range this memo ever uses
        if (script.length !== 2 + pushLen) continue; // exactly one push, nothing else in the script
        if (pushLen !== 52) continue; // 20-byte cc address + 32-byte Ed25519 pubkey
        const data = script.slice(2, 2 + pushLen);
        return {
            ccAddressHex: HDCrypto.bytesToHex(data.slice(0, 20)),
            publicKeyHex: HDCrypto.bytesToHex(data.slice(20, 52)),
        };
    }
    return null;
}

function btcVaultDepositOutputIndex(vout, vaultAddress) {
    for (let i = 0; i < vout.length; i++) {
        if (vout[i].scriptpubkey_address === vaultAddress) {
            return { index: i, sats: vout[i].value };
        }
    }
    return null;
}

async function scanBtcVaultDepositsOnceFromBrowser(log) {
    const vaultInfo = await fetchVaultInfo("BTC");
    if (!vaultInfo.configured) {
        log("BTC vault address not derivable yet (not enough registered BTC-capable validators) - nothing to scan.");
        return;
    }

    const vaultAddress = vaultInfo.depositAddress;
    const confirmations = vaultInfo.requiredConfirmations || 1;

    const [utxos, tip] = await Promise.all([
        btcVaultApiGet(`/address/${vaultAddress}/utxo`),
        btcVaultChainTipHeight(),
    ]);

    let scanned = 0;
    let attested = 0;
    for (const utxo of utxos) {
        if (!utxo.status || !utxo.status.confirmed) continue;
        if (tip < utxo.status.block_height || tip - utxo.status.block_height + 1 < confirmations) continue;

        scanned++;
        let tx;
        try {
            tx = await btcVaultApiGet(`/tx/${utxo.txid}`);
        } catch (e) {
            log(`Failed to fetch tx detail for ${utxo.txid}: ${e.message || e}`);
            continue;
        }

        const memo = decodeBtcVaultDepositMemoFromVout(tx.vout);
        if (!memo) {
            // No valid depositor memo - real locked BTC with no way to
            // attribute it to a CreditChain wallet, same disclosed
            // stuck-funds edge case the server-side loop already accepts.
            continue;
        }

        const payingOutput = btcVaultDepositOutputIndex(tx.vout, vaultAddress);
        if (!payingOutput || payingOutput.index !== utxo.vout) continue;

        try {
            const result = await apiPostSigned("/vault/lock-attest", {
                chain: "BTC",
                externalTxHash: utxo.txid,
                externalIndex: utxo.vout,
                depositorCcAddress: memo.ccAddressHex,
                depositorPublicKey: memo.publicKeyHex,
                amountSmallestUnit: String(payingOutput.sats),
            });
            attested++;
            log(`Attested BTC lock ${utxo.txid}:${utxo.vout} (${payingOutput.sats} sats) - tx ${result.txId}.`);
        } catch (e) {
            log(`Failed to attest ${utxo.txid}:${utxo.vout}: ${e.message || e}`);
        }
    }

    if (attested === 0) log(`Scanned ${scanned} confirmation-safe vault UTXO(s) - nothing new to attest.`);
}
