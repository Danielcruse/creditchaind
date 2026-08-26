// vault-btc-release-sign.js - browser-only BTC release-approval signer.
// BTC's sibling to admin-vault-keys.js's ETH/USDC signApproval - produces
// the DER signature a bridge validator submits via TxVaultReleaseApprove
// for a pending BTC withdrawal, using the SAME relayer key already saved
// in this page's key boxes (vault-relay.js's saveVaultRelayerKey).
//
// DISCLOSED PROTOCOL CONSTRAINT (see api/vault_btc_release_relay.go's
// own top doc comment, which this file mirrors exactly): a Bitcoin
// release signature commits to the EXACT unsigned transaction via
// BIP143 - there is no ecrecover-style "sign an abstract digest"
// shortcut like the ETH contract has. This code MUST independently
// reconstruct the IDENTICAL unsigned transaction the server-side relay
// (api/vault_btc_release_relay.go) builds: same UTXO selection, same
// fee, same outputs, in the same order. Any mismatch produces a
// signature that simply fails to verify against what actually gets
// broadcast - never a fund-safety issue, just a wasted signature.

// vaultReleaseTxVsizeJs is a byte-for-byte port of
// api/vault_btc_release_relay.go's vaultReleaseTxVsize - every signer
// (server-side relay, this browser tool, any future validator's own
// tooling) must compute the identical estimate for a given
// (threshold, redeemScriptLen, hasChange), which a fixed formula
// guarantees.
function vaultReleaseTxVsizeJs(threshold, redeemScriptLen, hasChange) {
    const baseVsize = 11;
    const perInputNonWitness = 41;
    const perOutputVsize = 31;
    const perSigWitnessBytes = 73;
    const dummyWitnessBytes = 2;

    const witnessBytes = dummyWitnessBytes + threshold * perSigWitnessBytes + redeemScriptLen + 3;
    const witnessVsize = Math.ceil((witnessBytes + 3) / 4);

    let vsize = baseVsize + perInputNonWitness + witnessVsize + perOutputVsize;
    if (hasChange) vsize += perOutputVsize;
    return vsize;
}

// vaultBtcDustThresholdSatsJs mirrors the Go constant of the same
// meaning (vaultBtcDustThresholdSats).
const VAULT_BTC_DUST_THRESHOLD_SATS = 546;

// selectVaultReleaseUtxoJs is a byte-for-byte port of
// api/vault_btc_release_relay.go's selectVaultReleaseUtxo - same
// deterministic sort (value asc, then txid, then vout) and same
// smallest-sufficient-UTXO selection rule, so every signer reaches the
// identical answer given the identical UTXO set.
function selectVaultReleaseUtxoJs(utxos, amountSats, threshold, redeemScriptLen, feeRateSatsPerVByte) {
    const confirmed = utxos.filter((u) => u.status && u.status.confirmed);
    confirmed.sort((a, b) => {
        if (a.value !== b.value) return a.value - b.value;
        if (a.txid !== b.txid) return a.txid < b.txid ? -1 : 1;
        return a.vout - b.vout;
    });

    const feeWithChange = vaultReleaseTxVsizeJs(threshold, redeemScriptLen, true) * feeRateSatsPerVByte;
    for (const u of confirmed) {
        if (u.value < amountSats) continue;
        const remainder = u.value - amountSats;
        if (remainder === 0 || remainder < feeWithChange + VAULT_BTC_DUST_THRESHOLD_SATS) {
            const feeNoChange = vaultReleaseTxVsizeJs(threshold, redeemScriptLen, false) * feeRateSatsPerVByte;
            if (u.value >= amountSats + feeNoChange) {
                return { utxo: u, feeSats: feeNoChange };
            }
            continue;
        }
        return { utxo: u, feeSats: feeWithChange };
    }
    return null;
}

// sortedBtcVaultPublicKeysJs mirrors api/vault_btc.go's
// sortedBtcVaultPublicKeys - deterministic ascending-hex order across
// every registered validator's compressed BTC pubkey, so every signer
// (and the server) reconstructs the identical redeem script.
function sortedBtcVaultPublicKeysJs(validatorsSnapshot) {
    const raws = [];
    for (const addr in validatorsSnapshot) {
        const v = validatorsSnapshot[addr];
        if (!v.BtcPublicKeyHex) continue;
        raws.push(v.BtcPublicKeyHex.toLowerCase());
    }
    raws.sort();
    return raws.map((hex) => hexToBytes(hex));
}

// buildBtcVaultRedeemScriptJs mirrors api/vault_btc.go's
// BuildBtcVaultRedeemScript: OP_M <pubkey1>...<pubkeyN> OP_N
// OP_CHECKMULTISIG. OP_1..OP_16 are opcodes 0x51..0x60; every pubkey
// here is always the standard 33-byte compressed form, so its push
// opcode is always the single literal-length byte 0x21 (33) - no
// OP_PUSHDATA1/2 branch needed at this size.
function buildBtcVaultRedeemScriptJs(sortedPubKeys, threshold) {
    if (threshold < 1 || threshold > sortedPubKeys.length || sortedPubKeys.length > 16) {
        throw new Error("buildBtcVaultRedeemScriptJs: threshold/pubkey count out of the OP_1..OP_16 range this builder supports");
    }
    const parts = [new Uint8Array([0x50 + threshold])];
    for (const pk of sortedPubKeys) {
        if (pk.length !== 33) throw new Error("buildBtcVaultRedeemScriptJs: every validator BTC pubkey must be 33 bytes (compressed)");
        parts.push(new Uint8Array([0x21]), pk);
    }
    parts.push(new Uint8Array([0x50 + sortedPubKeys.length, 0xae])); // OP_N, OP_CHECKMULTISIG
    return concatUint8Arrays(...parts);
}

// btcVaultWitnessProgramJs mirrors api/vault_btc.go's
// BtcVaultWitnessProgram - plain single SHA-256 of the redeem script
// (NOT hash160/double-SHA256 - P2WSH's witness program is defined as
// SHA256(script) alone, confirmed against btcsuite's own
// NewAddressWitnessScriptHash, which hard-rejects anything but an exact
// 32-byte program).
async function btcVaultWitnessProgramJs(redeemScript) {
    return new Uint8Array(await crypto.subtle.digest("SHA-256", redeemScript));
}

// buildAndSignBtcVaultRelease reconstructs the exact unsigned release
// transaction api/vault_btc_release_relay.go's submitBtcVaultRelease
// builds for this approval, computes its BIP143 sighash, and signs it
// with the relayer key - returns the DER signature hex WITHOUT a
// trailing sighash-type byte (AssembleBtcVaultWitness, api/
// vault_btc_witness.go, appends that itself once enough signatures are
// collected).
async function buildAndSignBtcVaultRelease(relayerPrivateKeyHex, approval) {
    const vaultInfo = await fetchVaultInfo("BTC");
    if (!vaultInfo.configured) throw new Error("BTC vault address not derivable yet.");
    const threshold = vaultInfo.requiredThreshold || 1;
    const feeRate = vaultInfo.releaseFeeRateSatsPerVByte || 10;

    const validatorsSnapshot = await apiGet("/bridge-validator/list");
    const sortedPubKeys = sortedBtcVaultPublicKeysJs(validatorsSnapshot);
    if (sortedPubKeys.length === 0) throw new Error("No registered BTC-capable bridge validators.");

    const redeemScript = buildBtcVaultRedeemScriptJs(sortedPubKeys, threshold);
    const witnessProgram = await btcVaultWitnessProgramJs(redeemScript);

    const amountSats = Number(approval.amountSmallestUnit);
    if (!Number.isInteger(amountSats) || amountSats <= 0) throw new Error("Invalid BTC release amount.");

    const utxos = await btcVaultApiGet(`/address/${vaultInfo.depositAddress}/utxo`);
    const selection = selectVaultReleaseUtxoJs(utxos, amountSats, threshold, redeemScript.length, feeRate);
    if (!selection) throw new Error("No single vault UTXO covers this release's amount + fee (needs a larger deposit to consolidate against).");

    const changeSats = selection.utxo.value - amountSats - selection.feeSats;
    const hasChange = changeSats >= VAULT_BTC_DUST_THRESHOLD_SATS;

    const destDecoded = HDCrypto.segwitAddrDecode(approval.destinationExtAddress);
    if (destDecoded.witver !== 0 || destDecoded.witprogBytes.length !== 20) {
        throw new Error("Destination must be a P2WPKH (native segwit) address.");
    }

    const outputs = [{ valueSats: amountSats, scriptPubKey: BtcTx._internal.p2wpkhScriptPubKey(destDecoded.witprogBytes) }];
    if (hasChange) {
        outputs.push({ valueSats: changeSats, scriptPubKey: p2wshScriptPubKey(witnessProgram) });
    }

    const version = 2;
    const locktime = 0;
    const inputs = [{ txid: selection.utxo.txid, vout: selection.utxo.vout, sequence: 0xffffffff, valueSats: selection.utxo.value }];

    const { sighash } = await BtcTx._internal.bip143Sighash({
        version,
        inputs,
        inputIndex: 0,
        scriptCode: redeemScript,
        amountSats: selection.utxo.value,
        outputs,
        locktime,
    });

    const privKeyBigInt = HDCrypto.bytesToBigInt(hexToBytes(relayerPrivateKeyHex.replace(/^0x/i, "")));
    const { r, s } = await BtcTx._internal.ecdsaSignBtc(privKeyBigInt, sighash);
    const derSig = BtcTx._internal.derEncodeSignature(r, s);

    return HDCrypto.bytesToHex(derSig);
}
