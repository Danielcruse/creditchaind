// btc-tx.js - BTC bridge: client-side BIP143 (native segwit, P2WPKH)
// Bitcoin transaction signing for the self-custodial BTC testnet
// account derived in website/js/hdwallet.js. The private key never
// leaves the browser - this file builds, signs, and serializes a
// signed raw transaction entirely locally, handed back as hex for the
// caller to POST to a broadcast endpoint that never sees the key.
//
// SCOPE, same "higher-stakes than pure derivation" framing as
// eth-tx.js: a bug in HD derivation (hd-crypto.js) produces a
// wrong-but-safe address; a bug in transaction SIGNING (this file) can
// leak the private key outright (nonce reuse) or lose real funds
// (a wrong sighash preimage produces a signature that either fails to
// verify - safe but broken - or, far worse, that a subtly-wrong
// implementation could be tricked into signing something other than
// what the user intended). Every primitive here is verified against
// BIP143's own OFFICIAL published test vectors (byte-for-byte, not a
// second self-written implementation that could share the same
// misunderstanding) before this file is trusted against any real
// broadcast - see the Section B plan's B.9 blocking gate and
// audit/KNOWN_GAPS.md for the actual results.
//
// WHY HAND-IMPLEMENTED: same reasoning as hd-crypto.js/eth-tx.js -
// vendoring an audited library was blocked (no download capability, no
// Node/npm in the dev environment).
//
// REUSED, NOT REIMPLEMENTED: the hardest part - correct secp256k1
// ECDSA signing math (RFC 6979 deterministic k, point arithmetic,
// low-S normalization) - is the exact same curve and exact same
// modular arithmetic Ethereum uses (secp256k1 is shared), so this file
// re-runs the identical RFC6979/ECDSA-sign algorithm eth-tx.js already
// implements and this project's oracle-verified elsewhere, just fed a
// double-SHA256 message hash instead of Keccak256, and DER-encoded
// (Bitcoin's signature format) instead of packed into an r/s/v triple.
// Only the BIP143 sighash preimage construction, DER encoding, varint/
// tx serialization, and witness assembly below are new, Bitcoin-
// specific code.
//
// DELIBERATE SCOPE NARROWING: only sends to native segwit (bech32,
// tb1.../bc1...) recipient addresses in this pass - a legacy base58
// address is rejected with a clear error rather than attempting
// legacy P2PKH scriptSig construction (a materially different,
// additional signing code path, named as a small bounded follow-up).
// Coin selection is a naive greedy accumulate-until-covered - not
// privacy- or dust-optimized, disclosed rather than silently assumed
// production-grade.
//
// Depends on website/js/vendor/hd-crypto.js being loaded first (reuses
// its secp256k1 point arithmetic, hash160, bech32 codec, and hex/
// bigint helpers - nothing here re-implements curve math from scratch).

(function (global) {
    "use strict";

    const N = HDCrypto.SECP256K1_N;
    const SIGHASH_ALL = 1;

    // ---------- hashing ----------

    async function sha256(bytes) {
        return new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
    }

    // dsha256 (double SHA-256) is Bitcoin's standard hashing primitive -
    // used for the transaction id, the BIP143 sighash, and every
    // intermediate hash below. Never confused with hash160 (SHA-256
    // then RIPEMD-160, HDCrypto.hash160), which is only for
    // pubkey-hash/script-hash derivation, not signing.
    async function dsha256(bytes) {
        return await sha256(await sha256(bytes));
    }

    // ---------- RFC 6979 deterministic nonce (k) + ECDSA sign ----------
    //
    // Byte-for-byte the same algorithm as eth-tx.js's rfc6979K/
    // ecdsaSign (see that file's own extensive comment on why no
    // random k is ever used) - secp256k1's curve order N is identical
    // for both chains, so this is a direct reuse of the same math, not
    // a re-derivation. The only difference from eth-tx.js: no
    // Ethereum-style recovery id is computed (Bitcoin's witness
    // includes the public key directly, so recovery is never needed),
    // and the result is DER-encoded further down instead of packed
    // into r/s/v.

    function bits2octetsModN(hashBytes32) {
        const z = HDCrypto.mod(HDCrypto.bytesToBigInt(hashBytes32), N);
        return HDCrypto.bigIntToBytes(z, 32);
    }

    async function rfc6979K(privKeyBytes32, hashBytes32) {
        const zero = new Uint8Array([0x00]);
        const one = new Uint8Array([0x01]);
        const h1Octets = bits2octetsModN(hashBytes32);

        let V = new Uint8Array(32).fill(0x01);
        let K = new Uint8Array(32).fill(0x00);

        K = await HDCrypto.hmacSha256(K, HDCrypto.concatBytes(V, zero, privKeyBytes32, h1Octets));
        V = await HDCrypto.hmacSha256(K, V);
        K = await HDCrypto.hmacSha256(K, HDCrypto.concatBytes(V, one, privKeyBytes32, h1Octets));
        V = await HDCrypto.hmacSha256(K, V);

        // eslint-disable-next-line no-constant-condition
        while (true) {
            V = await HDCrypto.hmacSha256(K, V);
            const k = HDCrypto.bytesToBigInt(V);
            if (k >= 1n && k < N) {
                return k;
            }
            K = await HDCrypto.hmacSha256(K, HDCrypto.concatBytes(V, zero));
            V = await HDCrypto.hmacSha256(K, V);
        }
    }

    async function ecdsaSignBtc(privKeyBigInt, hashBytes32) {
        const privKeyBytes32 = HDCrypto.bigIntToBytes(privKeyBigInt, 32);
        const h = HDCrypto.mod(HDCrypto.bytesToBigInt(hashBytes32), N);

        const k = await rfc6979K(privKeyBytes32, hashBytes32);
        const R = HDCrypto.scalarBaseMult(k);

        const r = HDCrypto.mod(R.x, N);
        if (r === 0n) {
            throw new Error("ECDSA sign produced r == 0 (astronomically unlikely) - retry with a different message/nonce");
        }

        const kInv = HDCrypto.modInverse(k, N);
        let s = HDCrypto.mod(kInv * HDCrypto.mod(h + r * privKeyBigInt, N), N);
        if (s === 0n) {
            throw new Error("ECDSA sign produced s == 0 (astronomically unlikely) - retry with a different message/nonce");
        }

        // Low-S normalization - a real Bitcoin consensus/standardness
        // rule (BIP62/BIP146), unconditional, same shape as EIP-2's
        // rule in eth-tx.js. No recovery-id flip needed here (unlike
        // Ethereum) since Bitcoin never derives one.
        const halfN = N / 2n;
        if (s > halfN) {
            s = N - s;
        }

        return { r, s };
    }

    // ---------- DER signature encoding ----------
    //
    // Bitcoin signatures are DER SEQUENCE{ INTEGER r, INTEGER s },
    // with the standard DER-integer rule: minimal big-endian bytes, and
    // a leading 0x00 byte prepended whenever the high bit of the first
    // byte would otherwise be set (DER integers are signed two's-
    // complement; Bitcoin's r/s are always positive, so this 0x00 pad
    // is what keeps them unambiguously positive under DER's own rules -
    // omitting it is a real, if subtle, encoding bug, not a style
    // choice).

    function derEncodeInt(x) {
        let hex = x.toString(16);
        if (hex.length % 2) hex = "0" + hex;
        let bytes = HDCrypto.hexToBytes(hex);
        if (bytes.length === 0) bytes = new Uint8Array([0x00]);
        if (bytes[0] & 0x80) {
            bytes = HDCrypto.concatBytes(new Uint8Array([0x00]), bytes);
        }
        return HDCrypto.concatBytes(new Uint8Array([0x02, bytes.length]), bytes);
    }

    function derEncodeSignature(r, s) {
        const rDer = derEncodeInt(r);
        const sDer = derEncodeInt(s);
        const body = HDCrypto.concatBytes(rDer, sDer);
        return HDCrypto.concatBytes(new Uint8Array([0x30, body.length]), body);
    }

    // ---------- varint (Bitcoin CompactSize) ----------

    function encodeVarInt(nIn) {
        const n = BigInt(nIn);
        if (n < 0xfdn) return new Uint8Array([Number(n)]);
        if (n <= 0xffffn) return HDCrypto.concatBytes(new Uint8Array([0xfd]), leBytes(n, 2));
        if (n <= 0xffffffffn) return HDCrypto.concatBytes(new Uint8Array([0xfe]), leBytes(n, 4));
        return HDCrypto.concatBytes(new Uint8Array([0xff]), leBytes(n, 8));
    }

    // leBytes little-endian-encodes n into exactly `len` bytes - used
    // throughout Bitcoin's tx serialization (versions, amounts,
    // sequence numbers, locktime, outpoint indices are ALL
    // little-endian, unlike RLP/Ethereum's big-endian convention in
    // eth-tx.js - this is the single most likely cross-chain copy/paste
    // mistake, flagged here explicitly).
    function leBytes(nIn, len) {
        let n = BigInt(nIn);
        const out = new Uint8Array(len);
        for (let i = 0; i < len; i++) {
            out[i] = Number(n & 0xffn);
            n >>= 8n;
        }
        return out;
    }

    // reverseBytes flips byte order - needed because block-explorer/
    // Esplora API txids are displayed in conventional (big-endian, "RPC
    // byte order") form, but Bitcoin's actual wire/serialization format
    // uses the INTERNAL (little-endian, reversed) byte order for a
    // txid inside an outpoint. Forgetting this reversal is a classic,
    // silent Bitcoin implementation bug (produces a validly-shaped but
    // wrong outpoint, signing/spending the wrong input).
    function reverseBytes(bytes) {
        return new Uint8Array(bytes).reverse();
    }

    // ---------- P2WPKH script helpers ----------

    // p2wpkhScriptPubKey is the actual output script: OP_0 <20-byte-hash>.
    function p2wpkhScriptPubKey(hash160Bytes) {
        return HDCrypto.concatBytes(new Uint8Array([0x00, 0x14]), hash160Bytes);
    }

    // p2wpkhScriptCode is BIP143's defined "scriptCode" for a P2WPKH
    // input - NOT the same bytes as the output scriptPubKey above.
    // Per BIP143: "the scriptCode for P2WKH is 0x1976a914{20-byte-
    // pubkey-hash}88ac" (i.e. the equivalent legacy P2PKH script, with
    // its own 0x19=25 length-prefix byte for use inside the sighash
    // preimage only - never broadcast as-is).
    function p2wpkhScriptCode(hash160Bytes) {
        return HDCrypto.concatBytes(
            new Uint8Array([0x19, 0x76, 0xa9, 0x14]),
            hash160Bytes,
            new Uint8Array([0x88, 0xac])
        );
    }

    // ---------- BIP143 sighash ----------
    //
    // Implements BIP143's SegWit sighash algorithm for SIGHASH_ALL
    // (the only sighash type this bridge ever uses) exactly as
    // specified:
    //   preimage = nVersion
    //            || hashPrevouts
    //            || hashSequence
    //            || outpoint (this input's own txid+vout)
    //            || scriptCode (see p2wpkhScriptCode)
    //            || amount (this input's OWN value, 8-byte LE - the
    //               whole point of BIP143: the amount being spent is
    //               committed into the hash even though it isn't
    //               literally present anywhere else in the tx, closing
    //               the class of bugs/attacks the older non-segwit
    //               sighash had around this)
    //            || nSequence (this input's own)
    //            || hashOutputs
    //            || nLocktime
    //            || nHashType (4-byte LE)
    //   sighash = dsha256(preimage)
    async function bip143Sighash({ version, inputs, inputIndex, scriptCode, amountSats, outputs, locktime }) {
        const prevoutsConcat = HDCrypto.concatBytes(
            ...inputs.map((inp) => HDCrypto.concatBytes(reverseBytes(HDCrypto.hexToBytes(inp.txid)), leBytes(inp.vout, 4)))
        );
        const hashPrevouts = await dsha256(prevoutsConcat);

        const sequenceConcat = HDCrypto.concatBytes(...inputs.map((inp) => leBytes(inp.sequence, 4)));
        const hashSequence = await dsha256(sequenceConcat);

        const outputsConcat = HDCrypto.concatBytes(
            ...outputs.map((o) => HDCrypto.concatBytes(leBytes(o.valueSats, 8), encodeVarInt(o.scriptPubKey.length), o.scriptPubKey))
        );
        const hashOutputs = await dsha256(outputsConcat);

        const thisInput = inputs[inputIndex];
        const outpoint = HDCrypto.concatBytes(reverseBytes(HDCrypto.hexToBytes(thisInput.txid)), leBytes(thisInput.vout, 4));

        const preimage = HDCrypto.concatBytes(
            leBytes(version, 4),
            hashPrevouts,
            hashSequence,
            outpoint,
            scriptCode,
            leBytes(amountSats, 8),
            leBytes(thisInput.sequence, 4),
            hashOutputs,
            leBytes(locktime, 4),
            leBytes(SIGHASH_ALL, 4)
        );

        return { sighash: await dsha256(preimage), preimage };
    }

    // ---------- full transaction serialization (segwit) ----------
    //
    // version(4LE) | marker(0x00) | flag(0x01) | txin_count(varint) |
    // inputs(outpoint(36) + scriptSig_len(0x00, empty for P2WPKH) +
    // sequence(4LE)) | txout_count(varint) | outputs(value(8LE) +
    // scriptPubKey) | witness (per input: item_count(varint) + each
    // item: len(varint)+bytes) | locktime(4LE). The marker+flag bytes
    // are what signal "this is a segwit-serialized transaction" to any
    // parser - a non-segwit transaction has no marker/flag and no
    // witness section at all.
    function serializeSignedTx({ version, inputs, outputs, witnesses, locktime }) {
        const parts = [];
        parts.push(leBytes(version, 4));
        parts.push(new Uint8Array([0x00, 0x01])); // segwit marker + flag
        parts.push(encodeVarInt(inputs.length));
        for (const inp of inputs) {
            parts.push(reverseBytes(HDCrypto.hexToBytes(inp.txid)));
            parts.push(leBytes(inp.vout, 4));
            parts.push(encodeVarInt(0)); // empty scriptSig - P2WPKH spends via witness only
            parts.push(leBytes(inp.sequence, 4));
        }
        parts.push(encodeVarInt(outputs.length));
        for (const o of outputs) {
            parts.push(leBytes(o.valueSats, 8));
            parts.push(encodeVarInt(o.scriptPubKey.length));
            parts.push(o.scriptPubKey);
        }
        for (const w of witnesses) {
            parts.push(encodeVarInt(w.length));
            for (const item of w) {
                parts.push(encodeVarInt(item.length));
                parts.push(item);
            }
        }
        parts.push(leBytes(locktime, 4));
        return HDCrypto.concatBytes(...parts);
    }

    // legacySerializeForTxid builds the NON-segwit serialization (no
    // marker/flag/witness section) - Bitcoin's txid is defined as
    // dsha256 of this legacy-shaped serialization even for a segwit
    // transaction (the segwit "wtxid" that includes witness data is a
    // distinct, separate identifier this bridge doesn't need).
    function legacySerializeForTxid({ version, inputs, outputs, locktime }) {
        const parts = [];
        parts.push(leBytes(version, 4));
        parts.push(encodeVarInt(inputs.length));
        for (const inp of inputs) {
            parts.push(reverseBytes(HDCrypto.hexToBytes(inp.txid)));
            parts.push(leBytes(inp.vout, 4));
            parts.push(encodeVarInt(0));
            parts.push(leBytes(inp.sequence, 4));
        }
        parts.push(encodeVarInt(outputs.length));
        for (const o of outputs) {
            parts.push(leBytes(o.valueSats, 8));
            parts.push(encodeVarInt(o.scriptPubKey.length));
            parts.push(o.scriptPubKey);
        }
        parts.push(leBytes(locktime, 4));
        return HDCrypto.concatBytes(...parts);
    }

    // ---------- public API ----------

    // signAndBuildTx builds, signs (BIP143, P2WPKH, SIGHASH_ALL), and
    // serializes a complete signed segwit transaction entirely
    // client-side. utxos are the sender's own OWN spendable UTXOs
    // (from GET /btc-bridge/utxos); feeSats is the caller's already-
    // decided total fee budget (see send-bridged-btc.js, which derives
    // this from the live fee-estimate endpoint and an estimated
    // transaction size). Coin selection is a naive greedy accumulate -
    // disclosed as unoptimized (see this file's header comment), not a
    // correctness concern for a demo send flow.
    async function signAndBuildTx(privateKeyHex, { utxos, toAddress, amountSats, feeSats, changeAddress }) {
        if (amountSats <= 0) throw new Error("amountSats must be positive");
        if (feeSats < 0) throw new Error("feeSats must not be negative");

        let toDecoded;
        try {
            toDecoded = HDCrypto.segwitAddrDecode(toAddress);
        } catch (e) {
            throw new Error("only native segwit (tb1.../bc1...) addresses are supported for sending right now: " + e.message);
        }
        if (toDecoded.witver !== 0 || toDecoded.witprogBytes.length !== 20) {
            throw new Error("only P2WPKH (witness v0, 20-byte program) recipient addresses are supported right now");
        }

        const changeDecoded = HDCrypto.segwitAddrDecode(changeAddress);

        // Naive greedy coin selection.
        const selected = [];
        let total = 0n;
        const target = BigInt(amountSats) + BigInt(feeSats);
        for (const u of utxos) {
            if (total >= target) break;
            selected.push(u);
            total += BigInt(u.value);
        }
        if (total < target) {
            throw new Error("insufficient confirmed BTC balance to cover amount + fee");
        }
        // Bitcoin's own mempool/relay policy rejects any output below
        // the dust threshold unless the WHOLE transaction pays zero fee
        // ("sendrawtransaction RPC error -26: dust, tx with dust output
        // must be 0-fee") - a too-small leftover is simply absorbed
        // into the fee (no change output at all) rather than creating
        // an output real Bitcoin nodes won't relay. See
        // website/js/vault.js's buildAndSignBtcVaultDepositTx for the
        // same fix applied to the vault deposit flow.
        const changeSatsRaw = total - target;
        const changeSats = changeSatsRaw >= 546n ? changeSatsRaw : 0n;

        const privKeyBigInt = HDCrypto.bytesToBigInt(HDCrypto.hexToBytes(privateKeyHex.replace(/^0x/i, "")));
        const pubPoint = HDCrypto.scalarBaseMult(privKeyBigInt);
        const pubCompressed = HDCrypto.compressPoint(pubPoint);
        const senderHash160 = await HDCrypto.hash160(pubCompressed);
        const scriptCode = p2wpkhScriptCode(senderHash160);

        const version = 2;
        const locktime = 0;
        const inputs = selected.map((u) => ({ txid: u.txid, vout: u.vout, sequence: 0xffffffff, valueSats: u.value }));

        const outputs = [{ valueSats: amountSats, scriptPubKey: p2wpkhScriptPubKey(toDecoded.witprogBytes) }];
        if (changeSats > 0n) {
            outputs.push({ valueSats: Number(changeSats), scriptPubKey: p2wpkhScriptPubKey(changeDecoded.witprogBytes) });
        }

        const witnesses = [];
        for (let i = 0; i < inputs.length; i++) {
            const { sighash } = await bip143Sighash({
                version,
                inputs,
                inputIndex: i,
                scriptCode,
                amountSats: inputs[i].valueSats,
                outputs,
                locktime,
            });
            const { r, s } = await ecdsaSignBtc(privKeyBigInt, sighash);
            const derSig = derEncodeSignature(r, s);
            const sigWithHashType = HDCrypto.concatBytes(derSig, new Uint8Array([SIGHASH_ALL]));
            witnesses.push([sigWithHashType, pubCompressed]);
        }

        const signedBytes = serializeSignedTx({ version, inputs, outputs, witnesses, locktime });
        const rawTxHex = HDCrypto.bytesToHex(signedBytes);

        const legacyBytes = legacySerializeForTxid({ version, inputs, outputs, locktime });
        const txidBytes = reverseBytes(await dsha256(legacyBytes)); // txid is displayed in reversed (RPC) byte order
        const txid = HDCrypto.bytesToHex(txidBytes);

        return { rawTxHex, txid };
    }

    global.BtcTx = {
        signAndBuildTx,
        // Exposed for the BIP143 official-test-vector parity check
        // (B.9's blocking gate) - not meant for use outside tests.
        _internal: { dsha256, bip143Sighash, derEncodeSignature, ecdsaSignBtc, p2wpkhScriptCode, p2wpkhScriptPubKey, serializeSignedTx, legacySerializeForTxid, encodeVarInt, leBytes, reverseBytes },
    };
})(window);
