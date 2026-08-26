// eth-tx.js - Multi-Chain HD Wallet Phase B: client-side Ethereum
// transaction signing for the SELF-CUSTODIAL Ethereum account derived
// in website/js/hdwallet.js. The private key never leaves the browser -
// this file signs a legacy Ethereum transaction entirely locally and
// hands back the raw RLP-encoded signed hex, which the caller then
// POSTs to /eth-selfcustody/broadcast (a pure relay - see
// api/eth_selfcustody.go - that never sees the key).
//
// SCOPE, DELIBERATELY NARROWER THAN hd-crypto.js: hd-crypto.js is
// scoped to HD derivation math and address encoding (see its own
// header comment and audit/KNOWN_GAPS.md). Transaction SIGNING is a
// distinct, higher-stakes risk category - a bug in HD derivation
// produces a wrong-but-safe address; a bug here (nonce reuse, or a
// weak/predictable per-signature nonce) can leak the private key
// outright. This file gets its own scope statement for that reason.
//
// WHY HAND-IMPLEMENTED: same reasoning as hd-crypto.js - vendoring an
// audited library was blocked (no download capability, no Node/npm in
// the dev environment), so ECDSA signing and RLP encoding are
// hand-written here. Mitigation: every primitive was cross-checked
// against an independent Go oracle (cmd/ethsignoracle, built only from
// go-ethereum functions already used by this project's production KMS
// signer - crypto.Sign, crypto.Ecrecover - deleted after use) across
// multiple RLP edge-case test vectors, with a byte-for-byte match on
// the full raw signed transaction hex (which subsumes both signature
// correctness and RLP-encoding correctness in one comparison) required
// before this code is trusted against any real Sepolia RPC. See the
// Multi-Chain HD Wallet Phase B plan's Verification section and
// audit/KNOWN_GAPS.md for the actual results.
//
// Depends on website/js/vendor/hd-crypto.js being loaded first (reuses
// its secp256k1 point arithmetic, keccak256, and hex/bigint helpers -
// nothing here re-implements curve math or hashing from scratch).

(function (global) {
    "use strict";

    const N = HDCrypto.SECP256K1_N;

    // ---------- RFC 6979 deterministic nonce (k) generation ----------
    //
    // No random k, ever: k must be exactly reproducible from
    // (privateKey, messageHash) per RFC 6979, since a reused or
    // predictable k catastrophically leaks the private key (this is
    // THE classic ECDSA vulnerability class - Sony's PS3 signing key
    // leaked exactly this way). Only the HMAC-SHA256 primitive itself
    // is native (via HDCrypto.hmacSha256); the RFC 6979 loop that
    // orchestrates it has no browser API and is hand-written below,
    // implementing RFC 6979 section 3.2 steps a-h for the case
    // qlen == hlen == 256 (secp256k1 curve order bit length == SHA-256
    // output bit length) - this equality means the "accumulate T until
    // long enough" step in the general algorithm collapses to exactly
    // one HMAC call per candidate, since one HMAC-SHA256 output (32
    // bytes = 256 bits) already equals qlen.

    function bits2octetsModN(hashBytes32) {
        // qlen == hlen here, so bits2int(h1) is just int(h1) directly
        // (no truncation needed) - only the "mod n" reduction from the
        // RFC's bits2octets applies.
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
            // Candidate out of [1, n-1] - astronomically unlikely for a
            // 256-bit curve order this close to 2^256, but RFC 6979
            // requires handling it, not assuming it away: continue the
            // K/V update per step h.3.
            K = await HDCrypto.hmacSha256(K, HDCrypto.concatBytes(V, zero));
            V = await HDCrypto.hmacSha256(K, V);
        }
    }

    // ---------- ECDSA sign ----------
    //
    // r/s arithmetic is mod the curve ORDER n (SECP256K1_N), never mod
    // the field prime p (SECP256K1_P, used only for point coordinate
    // math via HDCrypto.pointAdd/pointMul) - every mod/modInverse call
    // below must be checked against this.

    async function ecdsaSign(privKeyBigInt, hashBytes32) {
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

        // Recovery id: base value from the parity of the ephemeral
        // point's y-coordinate (the "R.x >= n" extra-bit case in the
        // full recovery-id spec is omitted - probability ~2^-128 for a
        // 256-bit key, the same simplifying assumption go-ethereum's
        // own signer effectively makes at this key size).
        let recid = (R.y % 2n === 0n) ? 0 : 1;

        // Low-S normalization (EIP-2 / Ethereum consensus rule):
        // s must be <= n/2, unconditionally - not optional.
        const halfN = N / 2n;
        if (s > halfN) {
            s = N - s;
            recid ^= 1;
        }

        return { r, s, recid };
    }

    // ---------- RLP encoding (BIP-173-adjacent spec, unrelated to
    // Bitcoin - Ethereum's Recursive Length Prefix serialization) ----------

    function numberToMinimalBytes(n) {
        if (n === 0) return new Uint8Array(0);
        let hex = n.toString(16);
        if (hex.length % 2) hex = "0" + hex;
        return HDCrypto.hexToBytes(hex);
    }

    // Integers (nonce, gasPrice, gasLimit, value, v, r, s) must be
    // minimal big-endian bytes with NO leading zero byte, and zero
    // must encode as an EMPTY byte string, not a single 0x00 byte -
    // this is the single most likely off-by-one spot in this whole
    // file, which is why the oracle test vectors deliberately include
    // a zero value and a value needing a leading-zero-byte trim.
    function bigIntToMinimalBytes(x) {
        if (x === 0n) return new Uint8Array(0);
        let hex = x.toString(16);
        if (hex.length % 2) hex = "0" + hex;
        return HDCrypto.hexToBytes(hex);
    }

    function rlpEncodeLength(len, offset) {
        if (len < 56) {
            return new Uint8Array([offset + len]);
        }
        const lenBytes = numberToMinimalBytes(len);
        return HDCrypto.concatBytes(new Uint8Array([offset + 55 + lenBytes.length]), lenBytes);
    }

    // Encodes a single byte string per RLP's string rules.
    function rlpEncodeBytes(bytes) {
        if (bytes.length === 1 && bytes[0] < 0x80) {
            return bytes;
        }
        return HDCrypto.concatBytes(rlpEncodeLength(bytes.length, 0x80), bytes);
    }

    // Wraps a list of ALREADY-RLP-ENCODED items (each already passed
    // through rlpEncodeBytes) as an RLP list.
    function rlpEncodeList(encodedItems) {
        const concatenated = HDCrypto.concatBytes(...encodedItems);
        return HDCrypto.concatBytes(rlpEncodeLength(concatenated.length, 0xc0), concatenated);
    }

    // ---------- legacy (EIP-155) transaction encoding ----------

    const EMPTY_BYTES = new Uint8Array(0);

    function encodeTxFields(fields, v, r, s) {
        return rlpEncodeList([
            rlpEncodeBytes(bigIntToMinimalBytes(fields.nonce)),
            rlpEncodeBytes(bigIntToMinimalBytes(fields.gasPrice)),
            rlpEncodeBytes(bigIntToMinimalBytes(fields.gasLimit)),
            rlpEncodeBytes(fields.toBytes), // 20-byte address - fixed-length, never minimal-trimmed
            rlpEncodeBytes(bigIntToMinimalBytes(fields.value)),
            rlpEncodeBytes(fields.data || EMPTY_BYTES), // empty for a plain transfer, ABI-encoded call data for a contract call (e.g. an ERC-20 transfer)
            rlpEncodeBytes(bigIntToMinimalBytes(v)),
            rlpEncodeBytes(bigIntToMinimalBytes(r)),
            rlpEncodeBytes(bigIntToMinimalBytes(s)),
        ]);
    }

    // EIP-155 signing hash: same field list, but chainId/0/0 stand in
    // for v/r/s (replay protection baked into the hash itself).
    function signingHash(fields) {
        const encoded = encodeTxFields(fields, fields.chainId, 0n, 0n);
        return HDCrypto.keccak256(encoded);
    }

    // ---------- public API ----------

    // signLegacyTx builds, signs, and RLP-encodes a legacy Ethereum
    // transaction entirely client-side. gasPriceWei/valueWei accept
    // decimal strings (matching the wei-as-string convention
    // /eth-selfcustody/balance already returns, avoiding precision loss
    // past Number.MAX_SAFE_INTEGER) or BigInt/number. `data` is optional
    // ABI-encoded calldata (a Uint8Array) for a contract call - e.g. the
    // vault's deposit()/depositERC20()/approve() calls (see
    // website/js/vault.js) - omitted entirely for a plain ETH transfer,
    // matching this function's original (pre-vault) behavior exactly.
    async function signLegacyTx(privateKeyHex, { nonce, gasPriceWei, gasLimit, to, valueWei, chainId, data }) {
        const privKeyBigInt = HDCrypto.bytesToBigInt(HDCrypto.hexToBytes(privateKeyHex.replace(/^0x/i, "")));

        const toHex = to.replace(/^0x/i, "");
        const toBytes = HDCrypto.hexToBytes(toHex);
        if (toBytes.length !== 20) {
            throw new Error("`to` must be a 20-byte (0x-prefixed 40-hex-char) Ethereum address");
        }

        const fields = {
            nonce: BigInt(nonce),
            gasPrice: BigInt(gasPriceWei),
            gasLimit: BigInt(gasLimit),
            toBytes,
            value: BigInt(valueWei),
            chainId: BigInt(chainId),
            data: data || undefined,
        };

        const hash = signingHash(fields);
        const { r, s, recid } = await ecdsaSign(privKeyBigInt, hash);
        const v = fields.chainId * 2n + 35n + BigInt(recid);

        const signedRlp = encodeTxFields(fields, v, r, s);
        const rawTxHex = "0x" + HDCrypto.bytesToHex(signedRlp);
        const txHash = "0x" + HDCrypto.bytesToHex(HDCrypto.keccak256(signedRlp));

        return { rawTxHex, txHash };
    }

    // addressFromPrivateKey derives the Ethereum address for a raw
    // private key - same scalarBaseMult -> keccak256(pubX||pubY)[12:]
    // recipe hdwallet.js's deriveEthAddress already uses, exposed here
    // so a caller holding only a raw hex key (e.g. a pasted-in relayer
    // key, never derived via BIP-32) can learn its own address without
    // going through the HD derivation path.
    function addressFromPrivateKey(privateKeyHex) {
        const privBigInt = HDCrypto.bytesToBigInt(HDCrypto.hexToBytes(privateKeyHex.replace(/^0x/i, "")));
        const pub = HDCrypto.scalarBaseMult(privBigInt);
        const pubXY = HDCrypto.concatBytes(
            HDCrypto.bigIntToBytes(pub.x, 32),
            HDCrypto.bigIntToBytes(pub.y, 32)
        );
        const addrHash = HDCrypto.keccak256(pubXY);
        return "0x" + HDCrypto.bytesToHex(addrHash.slice(12));
    }

    // compressedPublicKeyFromPrivateKey derives the 33-byte compressed
    // secp256k1 public key (66 hex chars, 0x02/0x03 prefix) for a raw
    // private key - reuses the exact same scalarBaseMult/compressPoint
    // primitives addressFromPrivateKey already uses. Both Ethereum and
    // Bitcoin sign on the same secp256k1 curve, just with different
    // address encodings from the same public key - this is what lets a
    // single key serve as a bridge validator's EthSigningAddress AND
    // BtcPublicKeyHex (api/bridge_validator.go's HandleBridgeValidatorRegister)
    // without needing two separate keys.
    function compressedPublicKeyFromPrivateKey(privateKeyHex) {
        const privBigInt = HDCrypto.bytesToBigInt(HDCrypto.hexToBytes(privateKeyHex.replace(/^0x/i, "")));
        const pub = HDCrypto.scalarBaseMult(privBigInt);
        return HDCrypto.bytesToHex(HDCrypto.compressPoint(pub));
    }

    // signDigest exposes the same raw ECDSA-over-a-32-byte-digest
    // primitive signLegacyTx already uses internally (RFC 6979
    // deterministic k, low-s normalized) - needed for signing anything
    // that ISN'T a transaction, e.g. contracts/CreditChainVault.sol's
    // release()/setValidators()/etc digests (keccak256(abi.encode(...)),
    // verified via a direct ecrecover with no EIP-191 prefix - see
    // website/js/vault-lock-attest.js). Returns the standard 65-byte
    // [R(32)||S(32)||V(1)] signature format that contract expects,
    // V already normalized to 27/28.
    async function signDigest(privateKeyHex, digestBytes32) {
        const privKeyBigInt = HDCrypto.bytesToBigInt(HDCrypto.hexToBytes(privateKeyHex.replace(/^0x/i, "")));
        const { r, s, recid } = await ecdsaSign(privKeyBigInt, digestBytes32);
        const v = 27 + recid;
        const sigBytes = HDCrypto.concatBytes(
            HDCrypto.bigIntToBytes(r, 32),
            HDCrypto.bigIntToBytes(s, 32),
            new Uint8Array([v])
        );
        return "0x" + HDCrypto.bytesToHex(sigBytes);
    }

    global.EthTx = { signLegacyTx, addressFromPrivateKey, compressedPublicKeyFromPrivateKey, signDigest };
})(window);
