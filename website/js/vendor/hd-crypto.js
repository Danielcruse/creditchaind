// hd-crypto.js - hand-implemented BIP-32 (secp256k1) + Keccak-256 +
// RIPEMD-160 + Bech32 primitives for the HD wallet (website/js/hdwallet.js).
//
// WHY HAND-IMPLEMENTED INSTEAD OF A VENDORED AUDITED LIBRARY (per the
// approved Phase A plan's own A.1 recommendation, which explicitly
// preferred vendoring @scure/bip32 + @noble/curves): downloading
// third-party code to vendor is blocked by this project's operating
// policy regardless of authorization, and no Node/npm exists in the
// dev environment to fetch+bundle packages either way. Given a real
// choice between the user manually sourcing the packages or hand-
// implementing from spec, the user explicitly chose hand-
// implementation, accepting the risk that a subtle bug here produces
// a silently-wrong-but-valid-looking key rather than a loud error.
//
// Mitigation actually taken: every primitive in this file was
// developed against a throwaway local Go reference implementation
// (cmd/hdoracle, built ONLY from dependencies already shipped in this
// codebase - golang.org/x/crypto and go-ethereum's own secp256k1 curve,
// both already used by the production api/eth_signer.go Sepolia
// signer - deleted after use, not part of the shipped app) and
// cross-checked against well-known, widely-published BIP-32/BIP-39
// test vectors. See the Phase A plan's Verification section item 2/2a
// for the additional, still-required cross-tool parity check before
// this reaches any real user.
//
// Ed25519 (CreditChain's own derivation) deliberately does NOT need
// any hand-rolled curve math here - see website/js/hdwallet.js's
// deriveCreditChainKeypair, which derives the public key via native
// crypto.subtle Ed25519 JWK export instead.

(function (global) {
    "use strict";

    // ---------- byte/hex helpers ----------

    function hexToBytes(hex) {
        const bytes = new Uint8Array(hex.length / 2);
        for (let i = 0; i < bytes.length; i++) {
            bytes[i] = parseInt(hex.substr(i * 2, 2), 16);
        }
        return bytes;
    }

    function bytesToHex(bytes) {
        return Array.from(bytes).map((b) => b.toString(16).padStart(2, "0")).join("");
    }

    function bytesToBigInt(bytes) {
        let hex = "0x" + (bytes.length ? bytesToHex(bytes) : "0");
        return BigInt(hex);
    }

    function bigIntToBytes(n, len) {
        let hex = n.toString(16);
        if (hex.length % 2) hex = "0" + hex;
        let bytes = hexToBytes(hex);
        if (bytes.length < len) {
            const padded = new Uint8Array(len);
            padded.set(bytes, len - bytes.length);
            bytes = padded;
        } else if (bytes.length > len) {
            bytes = bytes.slice(bytes.length - len);
        }
        return bytes;
    }

    function concatBytes(...arrs) {
        let total = 0;
        for (const a of arrs) total += a.length;
        const out = new Uint8Array(total);
        let offset = 0;
        for (const a of arrs) {
            out.set(a, offset);
            offset += a.length;
        }
        return out;
    }

    // ---------- HMAC-SHA512 (native WebCrypto) ----------

    async function hmacSha512(keyBytes, dataBytes) {
        const key = await crypto.subtle.importKey(
            "raw", keyBytes, { name: "HMAC", hash: "SHA-512" }, false, ["sign"]
        );
        const sig = await crypto.subtle.sign("HMAC", key, dataBytes);
        return new Uint8Array(sig);
    }

    // ---------- HMAC-SHA256 (native WebCrypto) ----------
    //
    // Used by website/js/vendor/eth-tx.js's RFC 6979 deterministic-k
    // orchestration (Multi-Chain HD Wallet Phase B) - only the HMAC
    // primitive itself is native here, the RFC 6979 loop that calls it
    // repeatedly is hand-written in eth-tx.js, since no browser API
    // implements RFC 6979 directly.

    async function hmacSha256(keyBytes, dataBytes) {
        const key = await crypto.subtle.importKey(
            "raw", keyBytes, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]
        );
        const sig = await crypto.subtle.sign("HMAC", key, dataBytes);
        return new Uint8Array(sig);
    }

    // ---------- PBKDF2-HMAC-SHA512 (native WebCrypto), for BIP-39 seed ----------

    async function pbkdf2Sha512(passwordBytes, saltBytes, iterations, keyLenBytes) {
        const key = await crypto.subtle.importKey("raw", passwordBytes, "PBKDF2", false, ["deriveBits"]);
        const bits = await crypto.subtle.deriveBits(
            { name: "PBKDF2", salt: saltBytes, iterations: iterations, hash: "SHA-512" },
            key,
            keyLenBytes * 8
        );
        return new Uint8Array(bits);
    }

    // ---------- secp256k1 field/curve arithmetic (BigInt, affine coords) ----------

    const SECP256K1_P = BigInt("0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F");
    const SECP256K1_N = BigInt("0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141");
    const SECP256K1_GX = BigInt("0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798");
    const SECP256K1_GY = BigInt("0x483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8");

    function mod(a, m) {
        const r = a % m;
        return r >= 0n ? r : r + m;
    }

    // Modular inverse via the extended Euclidean algorithm - p is
    // prime so this always succeeds for a != 0 mod p.
    function modInverse(a, m) {
        a = mod(a, m);
        let [old_r, r] = [a, m];
        let [old_s, s] = [1n, 0n];
        while (r !== 0n) {
            const q = old_r / r;
            [old_r, r] = [r, old_r - q * r];
            [old_s, s] = [s, old_s - q * s];
        }
        return mod(old_s, m);
    }

    // Points are {x, y} in affine coordinates, or null for the point
    // at infinity.
    function pointDouble(P) {
        if (P === null) return null;
        if (P.y === 0n) return null;
        const lambda = mod((3n * P.x * P.x) * modInverse(2n * P.y, SECP256K1_P), SECP256K1_P);
        const x3 = mod(lambda * lambda - 2n * P.x, SECP256K1_P);
        const y3 = mod(lambda * (P.x - x3) - P.y, SECP256K1_P);
        return { x: x3, y: y3 };
    }

    function pointAdd(P1, P2) {
        if (P1 === null) return P2;
        if (P2 === null) return P1;
        if (P1.x === P2.x) {
            if (mod(P1.y + P2.y, SECP256K1_P) === 0n) return null;
            return pointDouble(P1);
        }
        const lambda = mod((P2.y - P1.y) * modInverse(P2.x - P1.x, SECP256K1_P), SECP256K1_P);
        const x3 = mod(lambda * lambda - P1.x - P2.x, SECP256K1_P);
        const y3 = mod(lambda * (P1.x - x3) - P1.y, SECP256K1_P);
        return { x: x3, y: y3 };
    }

    // Scalar multiplication via double-and-add. Simplicity over
    // constant-time execution is an accepted tradeoff here - this
    // runs once per key derivation in the browser, not in a hot loop
    // or a context where timing side-channels are the realistic
    // threat model (see the QR encoder / native-Ed25519-signing
    // precedent this file's header already discusses).
    function pointMul(k, P) {
        let result = null;
        let addend = P;
        let n = k;
        while (n > 0n) {
            if (n & 1n) {
                result = pointAdd(result, addend);
            }
            addend = pointDouble(addend);
            n >>= 1n;
        }
        return result;
    }

    function scalarBaseMult(k) {
        return pointMul(k, { x: SECP256K1_GX, y: SECP256K1_GY });
    }

    function compressPoint(P) {
        const prefix = (P.y & 1n) === 0n ? 0x02 : 0x03;
        const out = new Uint8Array(33);
        out[0] = prefix;
        out.set(bigIntToBytes(P.x, 32), 1);
        return out;
    }

    // ---------- BIP-32 HD derivation (secp256k1) ----------

    function ser32(i) {
        const out = new Uint8Array(4);
        out[0] = (i >>> 24) & 0xff;
        out[1] = (i >>> 16) & 0xff;
        out[2] = (i >>> 8) & 0xff;
        out[3] = i & 0xff;
        return out;
    }

    const HARDENED_OFFSET = 0x80000000;

    async function masterKeyFromSeed(seedBytes) {
        const key = new TextEncoder().encode("Bitcoin seed");
        const I = await hmacSha512(key, seedBytes);
        return { key: I.slice(0, 32), chainCode: I.slice(32, 64) };
    }

    // CKDpriv per BIP-32. index >= HARDENED_OFFSET means hardened.
    async function ckdPriv(node, index) {
        let data;
        if (index >= HARDENED_OFFSET) {
            data = concatBytes(new Uint8Array([0x00]), node.key, ser32(index));
        } else {
            const kPar = bytesToBigInt(node.key);
            const P = scalarBaseMult(kPar);
            data = concatBytes(compressPoint(P), ser32(index));
        }
        const I = await hmacSha512(node.chainCode, data);
        const IL = I.slice(0, 32);
        const IR = I.slice(32, 64);

        const il = bytesToBigInt(IL);
        if (il >= SECP256K1_N) {
            throw new Error("invalid child key (IL >= n) - astronomically unlikely, caller should try next index");
        }
        const kPar = bytesToBigInt(node.key);
        const ki = mod(il + kPar, SECP256K1_N);
        if (ki === 0n) {
            throw new Error("invalid child key (ki == 0)");
        }

        return { key: bigIntToBytes(ki, 32), chainCode: IR };
    }

    async function deriveSecp256k1Path(seedBytes, path) {
        let node = await masterKeyFromSeed(seedBytes);
        for (const index of path) {
            node = await ckdPriv(node, index);
        }
        return node;
    }

    // ---------- Ed25519 point arithmetic (RFC 8032) ----------
    //
    // Originally hoped to avoid this entirely by importing the derived
    // seed into native crypto.subtle as an Ed25519 JWK and exporting
    // the public key back out - WebCrypto's own audited implementation
    // would have computed it, zero hand-rolled curve math needed. That
    // does NOT work in practice: this browser's Ed25519 JWK import
    // requires "x" (the public key) to already be present, it will not
    // compute and return it from "d" (the seed) alone - confirmed via
    // a live DataError while building this file, not assumed. Native
    // WebCrypto Ed25519 is still used for real *signing* everywhere
    // else in this app (importSigningKey/apiPostSigned, unchanged) -
    // this hand-rolled path exists ONLY to compute a public key from a
    // freshly-derived seed before that key is ever used to sign
    // anything for real.

    const ED25519_P = (1n << 255n) - 19n;
    const ED25519_D = mod(-121665n * modInverse(121666n, ED25519_P), ED25519_P);
    const ED25519_BX = BigInt("15112221349535400772501151409588531511454012693041857206046113283949847762202");
    const ED25519_BY = BigInt("46316835694926478169428394003475163141307993866256225615783033603165251855960");

    // Unified twisted-Edwards addition law (also correct for doubling,
    // P1===P2) - avoids the separate-doubling-formula special case
    // secp256k1's Weierstrass form needed above.
    function ed25519PointAdd(P1, P2) {
        const x1y2 = P1.x * P2.y, y1x2 = P1.y * P2.x, y1y2 = P1.y * P2.y, x1x2 = P1.x * P2.x;
        const dxxyy = mod(ED25519_D * x1x2 * y1y2, ED25519_P);
        const x3 = mod((x1y2 + y1x2) * modInverse(mod(1n + dxxyy, ED25519_P), ED25519_P), ED25519_P);
        const y3 = mod((y1y2 + x1x2) * modInverse(mod(1n - dxxyy, ED25519_P), ED25519_P), ED25519_P);
        return { x: x3, y: y3 };
    }

    function ed25519ScalarMult(k, P) {
        let result = { x: 0n, y: 1n }; // identity element
        let addend = P;
        let n = k;
        while (n > 0n) {
            if (n & 1n) result = ed25519PointAdd(result, addend);
            addend = ed25519PointAdd(addend, addend);
            n >>= 1n;
        }
        return result;
    }

    // Standard Ed25519 point encoding: y as 32 little-endian bytes,
    // with bit 255 (top bit of the last byte) set to the parity of x.
    function ed25519EncodePoint(P) {
        const bytes = bigIntToBytes(P.y, 32).reverse(); // to little-endian
        if (P.x & 1n) {
            bytes[31] |= 0x80;
        }
        return bytes;
    }

    // RFC 8032 public-key derivation from a 32-byte seed: SHA-512(seed),
    // clamp the low half into a scalar, multiply by the base point,
    // encode. This is the ONLY thing this codebase needs Ed25519 point
    // math for - actual signing stays on native WebCrypto everywhere
    // else, using this same clamped-scalar convention internally
    // (that part of RFC 8032 is standardized specifically so any two
    // compliant implementations - this hand-rolled derivation and the
    // browser's native signer - agree on the same public key for the
    // same seed).
    async function ed25519PublicKeyFromSeed(seed32) {
        const h = new Uint8Array(await crypto.subtle.digest("SHA-512", seed32));
        const a = h.slice(0, 32);
        a[0] &= 248;
        a[31] &= 127;
        a[31] |= 64;
        // a is little-endian per RFC 8032 - reverse before treating as
        // a big-endian BigInt (bytesToBigInt assumes big-endian input).
        const scalar = bytesToBigInt(Uint8Array.from(a).reverse());
        const A = ed25519ScalarMult(scalar, { x: ED25519_BX, y: ED25519_BY });
        return ed25519EncodePoint(A);
    }

    // ---------- SLIP-10 Ed25519 (hardened-only, CreditChain's own path) ----------
    //
    // Only HMAC-SHA512 chaining here (native WebCrypto) - the actual
    // public key comes from ed25519PublicKeyFromSeed above.

    async function slip10MasterKeyFromSeed(seedBytes) {
        const key = new TextEncoder().encode("ed25519 seed");
        const I = await hmacSha512(key, seedBytes);
        return { key: I.slice(0, 32), chainCode: I.slice(32, 64) };
    }

    // index must already have HARDENED_OFFSET added by the caller -
    // SLIP-10 Ed25519 has no non-hardened derivation.
    async function slip10CkdPriv(node, index) {
        const data = concatBytes(new Uint8Array([0x00]), node.key, ser32(index));
        const I = await hmacSha512(node.chainCode, data);
        return { key: I.slice(0, 32), chainCode: I.slice(32, 64) };
    }

    async function deriveEd25519Path(seedBytes, path) {
        let node = await slip10MasterKeyFromSeed(seedBytes);
        for (const index of path) {
            node = await slip10CkdPriv(node, index);
        }
        return node; // node.key is the 32-byte Ed25519 seed
    }

    // ---------- Keccak-256 (NOT NIST SHA3 - Ethereum uses the original
    // Keccak padding, domain byte 0x01, not SHA3's finalized 0x06) ----------

    const KECCAK_ROUND_CONSTANTS = [
        0x0000000000000001n, 0x0000000000008082n, 0x800000000000808an, 0x8000000080008000n,
        0x000000000000808bn, 0x0000000080000001n, 0x8000000080008081n, 0x8000000000008009n,
        0x000000000000008an, 0x0000000000000088n, 0x0000000080008009n, 0x000000008000000an,
        0x000000008000808bn, 0x800000000000008bn, 0x8000000000008089n, 0x8000000000008003n,
        0x8000000000008002n, 0x8000000000000080n, 0x000000000000800an, 0x800000008000000an,
        0x8000000080008081n, 0x8000000000008080n, 0x0000000080000001n, 0x8000000080008008n,
    ];
    const KECCAK_ROTATION_OFFSETS = [
        [0, 36, 3, 41, 18],
        [1, 44, 10, 45, 2],
        [62, 6, 43, 15, 61],
        [28, 55, 25, 21, 56],
        [27, 20, 39, 8, 14],
    ];
    const MASK64 = (1n << 64n) - 1n;

    function rotl64(x, n) {
        n = BigInt(n) % 64n;
        if (n === 0n) return x & MASK64;
        return ((x << n) | (x >> (64n - n))) & MASK64;
    }

    // state is a 5x5 array of BigInt lanes, state[x][y].
    function keccakF1600(state) {
        for (let round = 0; round < 24; round++) {
            // theta
            const C = new Array(5);
            for (let x = 0; x < 5; x++) {
                C[x] = state[x][0] ^ state[x][1] ^ state[x][2] ^ state[x][3] ^ state[x][4];
            }
            const D = new Array(5);
            for (let x = 0; x < 5; x++) {
                D[x] = C[(x + 4) % 5] ^ rotl64(C[(x + 1) % 5], 1);
            }
            for (let x = 0; x < 5; x++) {
                for (let y = 0; y < 5; y++) {
                    state[x][y] ^= D[x];
                }
            }

            // rho + pi
            const B = [[0n, 0n, 0n, 0n, 0n], [0n, 0n, 0n, 0n, 0n], [0n, 0n, 0n, 0n, 0n], [0n, 0n, 0n, 0n, 0n], [0n, 0n, 0n, 0n, 0n]];
            for (let x = 0; x < 5; x++) {
                for (let y = 0; y < 5; y++) {
                    const newX = y;
                    const newY = (2 * x + 3 * y) % 5;
                    B[newX][newY] = rotl64(state[x][y], KECCAK_ROTATION_OFFSETS[x][y]);
                }
            }

            // chi
            for (let x = 0; x < 5; x++) {
                for (let y = 0; y < 5; y++) {
                    state[x][y] = (B[x][y] ^ ((~B[(x + 1) % 5][y]) & MASK64 & B[(x + 2) % 5][y])) & MASK64;
                }
            }

            // iota
            state[0][0] ^= KECCAK_ROUND_CONSTANTS[round];
        }
        return state;
    }

    function keccak256(messageBytes) {
        const rateBytes = 136; // 1088 bits, for the 256-bit variant (capacity 512 bits)
        const outputBytes = 32;

        // pad10*1 with the Keccak (not SHA3) domain separator 0x01.
        const msgLen = messageBytes.length;
        const padLen = rateBytes - (msgLen % rateBytes);
        const padded = new Uint8Array(msgLen + padLen);
        padded.set(messageBytes);
        padded[msgLen] = 0x01;
        padded[padded.length - 1] |= 0x80;

        const state = [[0n, 0n, 0n, 0n, 0n], [0n, 0n, 0n, 0n, 0n], [0n, 0n, 0n, 0n, 0n], [0n, 0n, 0n, 0n, 0n], [0n, 0n, 0n, 0n, 0n]];

        for (let offset = 0; offset < padded.length; offset += rateBytes) {
            for (let i = 0; i < rateBytes / 8; i++) {
                const x = i % 5;
                const y = Math.floor(i / 5);
                let lane = 0n;
                for (let b = 7; b >= 0; b--) {
                    lane = (lane << 8n) | BigInt(padded[offset + i * 8 + b]);
                }
                state[x][y] ^= lane;
            }
            keccakF1600(state);
        }

        const out = new Uint8Array(outputBytes);
        let outPos = 0;
        outer:
        for (let y = 0; y < 5; y++) {
            for (let x = 0; x < 5; x++) {
                let lane = state[x][y];
                for (let b = 0; b < 8; b++) {
                    out[outPos++] = Number(lane & 0xffn);
                    lane >>= 8n;
                    if (outPos >= outputBytes) break outer;
                }
            }
        }
        return out;
    }

    // ---------- RIPEMD-160 ----------
    // Standard reference algorithm (Dobbertin/Bosselaers/Preneel), 5
    // rounds x 16 steps, two parallel lines.

    function ripemd160(messageBytes) {
        function rotl32(x, n) {
            return ((x << n) | (x >>> (32 - n))) >>> 0;
        }
        function f(j, x, y, z) {
            if (j < 16) return (x ^ y ^ z) >>> 0;
            if (j < 32) return ((x & y) | (~x & z)) >>> 0;
            if (j < 48) return ((x | ~y) ^ z) >>> 0;
            if (j < 64) return ((x & z) | (y & ~z)) >>> 0;
            return (x ^ (y | ~z)) >>> 0;
        }
        const KL = [0x00000000, 0x5a827999, 0x6ed9eba1, 0x8f1bbcdc, 0xa953fd4e];
        const KR = [0x50a28be6, 0x5c4dd124, 0x6d703ef3, 0x7a6d76e9, 0x00000000];
        const RL = [
            0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
            7, 4, 13, 1, 10, 6, 15, 3, 12, 0, 9, 5, 2, 14, 11, 8,
            3, 10, 14, 4, 9, 15, 8, 1, 2, 7, 0, 6, 13, 11, 5, 12,
            1, 9, 11, 10, 0, 8, 12, 4, 13, 3, 7, 15, 14, 5, 6, 2,
            4, 0, 5, 9, 7, 12, 2, 10, 14, 1, 3, 8, 11, 6, 15, 13,
        ];
        const RR = [
            5, 14, 7, 0, 9, 2, 11, 4, 13, 6, 15, 8, 1, 10, 3, 12,
            6, 11, 3, 7, 0, 13, 5, 10, 14, 15, 8, 12, 4, 9, 1, 2,
            15, 5, 1, 3, 7, 14, 6, 9, 11, 8, 12, 2, 10, 0, 4, 13,
            8, 6, 4, 1, 3, 11, 15, 0, 5, 12, 2, 13, 9, 7, 10, 14,
            12, 15, 10, 4, 1, 5, 8, 7, 6, 2, 13, 14, 0, 3, 9, 11,
        ];
        const SL = [
            11, 14, 15, 12, 5, 8, 7, 9, 11, 13, 14, 15, 6, 7, 9, 8,
            7, 6, 8, 13, 11, 9, 7, 15, 7, 12, 15, 9, 11, 7, 13, 12,
            11, 13, 6, 7, 14, 9, 13, 15, 14, 8, 13, 6, 5, 12, 7, 5,
            11, 12, 14, 15, 14, 15, 9, 8, 9, 14, 5, 6, 8, 6, 5, 12,
            9, 15, 5, 11, 6, 8, 13, 12, 5, 12, 13, 14, 11, 8, 5, 6,
        ];
        const SR = [
            8, 9, 9, 11, 13, 15, 15, 5, 7, 7, 8, 11, 14, 14, 12, 6,
            9, 13, 15, 7, 12, 8, 9, 11, 7, 7, 12, 7, 6, 15, 13, 11,
            9, 7, 15, 11, 8, 6, 6, 14, 12, 13, 5, 14, 13, 13, 7, 5,
            15, 5, 8, 11, 14, 14, 6, 14, 6, 9, 12, 9, 12, 5, 15, 8,
            8, 5, 12, 9, 12, 5, 14, 6, 8, 13, 6, 5, 15, 13, 11, 11,
        ];

        const msgLen = messageBytes.length;
        const bitLen = BigInt(msgLen) * 8n;
        const padLen = ((msgLen % 64) < 56) ? (56 - (msgLen % 64)) : (120 - (msgLen % 64));
        const padded = new Uint8Array(msgLen + padLen + 8);
        padded.set(messageBytes);
        padded[msgLen] = 0x80;
        for (let i = 0; i < 8; i++) {
            padded[padded.length - 8 + i] = Number((bitLen >> BigInt(8 * i)) & 0xffn);
        }

        let h = [0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0];

        for (let offset = 0; offset < padded.length; offset += 64) {
            const X = new Array(16);
            for (let i = 0; i < 16; i++) {
                X[i] = (padded[offset + i * 4] | (padded[offset + i * 4 + 1] << 8) |
                    (padded[offset + i * 4 + 2] << 16) | (padded[offset + i * 4 + 3] << 24)) >>> 0;
            }

            let [al, bl, cl, dl, el] = h;
            let [ar, br, cr, dr, er] = h;

            for (let j = 0; j < 80; j++) {
                const round = Math.floor(j / 16);
                let t = (al + f(j, bl, cl, dl) + X[RL[j]] + KL[round]) >>> 0;
                t = (rotl32(t, SL[j]) + el) >>> 0;
                al = el; el = dl; dl = rotl32(cl, 10); cl = bl; bl = t;

                t = (ar + f(79 - j, br, cr, dr) + X[RR[j]] + KR[round]) >>> 0;
                t = (rotl32(t, SR[j]) + er) >>> 0;
                ar = er; er = dr; dr = rotl32(cr, 10); cr = br; br = t;
            }

            const t = (h[1] + cl + dr) >>> 0;
            h[1] = (h[2] + dl + er) >>> 0;
            h[2] = (h[3] + el + ar) >>> 0;
            h[3] = (h[4] + al + br) >>> 0;
            h[4] = (h[0] + bl + cr) >>> 0;
            h[0] = t;
        }

        const out = new Uint8Array(20);
        for (let i = 0; i < 5; i++) {
            out[i * 4] = h[i] & 0xff;
            out[i * 4 + 1] = (h[i] >>> 8) & 0xff;
            out[i * 4 + 2] = (h[i] >>> 16) & 0xff;
            out[i * 4 + 3] = (h[i] >>> 24) & 0xff;
        }
        return out;
    }

    async function hash160(bytes) {
        const sha = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
        return ripemd160(sha);
    }

    // ---------- Bech32 (BIP-173), for Bitcoin testnet P2WPKH addresses ----------

    const BECH32_CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
    const BECH32_GENERATOR = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];

    function bech32Polymod(values) {
        let chk = 1;
        for (const v of values) {
            const b = chk >>> 25;
            chk = ((chk & 0x1ffffff) << 5) ^ v;
            for (let i = 0; i < 5; i++) {
                if ((b >>> i) & 1) chk ^= BECH32_GENERATOR[i];
            }
        }
        return chk >>> 0;
    }

    function bech32HrpExpand(hrp) {
        const ret = [];
        for (let i = 0; i < hrp.length; i++) ret.push(hrp.charCodeAt(i) >> 5);
        ret.push(0);
        for (let i = 0; i < hrp.length; i++) ret.push(hrp.charCodeAt(i) & 31);
        return ret;
    }

    function bech32CreateChecksum(hrp, data) {
        const values = bech32HrpExpand(hrp).concat(data).concat([0, 0, 0, 0, 0, 0]);
        const polymod = bech32Polymod(values) ^ 1;
        const ret = [];
        for (let i = 0; i < 6; i++) ret.push((polymod >>> (5 * (5 - i))) & 31);
        return ret;
    }

    function convertBits(data, fromBits, toBits, pad) {
        let acc = 0, bits = 0;
        const ret = [];
        const maxv = (1 << toBits) - 1;
        for (const value of data) {
            acc = (acc << fromBits) | value;
            bits += fromBits;
            while (bits >= toBits) {
                bits -= toBits;
                ret.push((acc >> bits) & maxv);
            }
        }
        if (pad && bits > 0) {
            ret.push((acc << (toBits - bits)) & maxv);
        }
        return ret;
    }

    function segwitAddrEncode(hrp, witver, witprogBytes) {
        const data = [witver].concat(convertBits(Array.from(witprogBytes), 8, 5, true));
        const combined = data.concat(bech32CreateChecksum(hrp, data));
        let out = hrp + "1";
        for (const d of combined) out += BECH32_CHARSET[d];
        return out;
    }

    // segwitAddrDecode is segwitAddrEncode's inverse - turns a typed
    // recipient bech32 address (e.g. "tb1q...") back into the raw
    // {hrp, witver, witprogBytes} a send transaction needs to build a
    // scriptPubKey from. Throws on anything malformed rather than
    // returning a best-effort guess, since a wrongly-decoded recipient
    // program here would misdirect real funds - every check below
    // mirrors a real validity rule from BIP-173/BIP-141, not just a
    // loose shape check the way btcRegisterAddressData's server-side
    // validation is (that one only gates which address THIS wallet
    // watches for its own deposits; this one gates where an outbound
    // real send actually goes).
    function segwitAddrDecode(addrStr) {
        // BIP-173: a valid bech32 string is either all-lowercase or
        // all-uppercase, never mixed - reject before any other check,
        // then normalize to lowercase for the rest of decoding.
        if (addrStr !== addrStr.toLowerCase() && addrStr !== addrStr.toUpperCase()) {
            throw new Error("invalid bech32 address: mixed case");
        }
        addrStr = addrStr.toLowerCase();

        const sep = addrStr.lastIndexOf("1");
        if (sep < 1 || sep + 7 > addrStr.length) {
            throw new Error("invalid bech32 address: missing/misplaced separator");
        }
        const hrp = addrStr.slice(0, sep);
        const dataPart = addrStr.slice(sep + 1);

        const data = [];
        for (const ch of dataPart) {
            const v = BECH32_CHARSET.indexOf(ch);
            if (v === -1) {
                throw new Error("invalid bech32 address: bad character '" + ch + "'");
            }
            data.push(v);
        }

        const checksumStart = data.length - 6;
        if (checksumStart < 1) {
            throw new Error("invalid bech32 address: too short");
        }
        const values = bech32HrpExpand(hrp).concat(data);
        if (bech32Polymod(values) !== 1) {
            throw new Error("invalid bech32 address: checksum mismatch");
        }

        const witver = data[0];
        if (witver < 0 || witver > 16) {
            throw new Error("invalid bech32 address: bad witness version " + witver);
        }
        const program5bit = data.slice(1, checksumStart);
        const witprog = convertBits(program5bit, 5, 8, false);
        if (witprog === null || witprog.length < 2 || witprog.length > 40) {
            throw new Error("invalid bech32 address: bad witness program length");
        }
        // BIP-141: version 0 programs must be exactly 20 bytes (P2WPKH)
        // or 32 bytes (P2WSH) - this bridge only ever sends to P2WPKH
        // (see send-bridged-btc.js's own scope note), but the length
        // check itself is a real protocol rule, not a scope choice.
        if (witver === 0 && witprog.length !== 20 && witprog.length !== 32) {
            throw new Error("invalid bech32 address: v0 program must be 20 or 32 bytes");
        }

        return { hrp, witver, witprogBytes: new Uint8Array(witprog) };
    }

    // ---------- EIP-55 checksummed Ethereum address ----------

    async function toChecksumAddress(addressHex) {
        addressHex = addressHex.toLowerCase().replace(/^0x/, "");
        const hashBytes = keccak256(new TextEncoder().encode(addressHex));
        const hashHex = bytesToHex(hashBytes);
        let out = "0x";
        for (let i = 0; i < addressHex.length; i++) {
            out += parseInt(hashHex[i], 16) >= 8 ? addressHex[i].toUpperCase() : addressHex[i];
        }
        return out;
    }

    global.HDCrypto = {
        hexToBytes, bytesToHex, bytesToBigInt, bigIntToBytes, concatBytes,
        hmacSha512, hmacSha256, pbkdf2Sha512,
        scalarBaseMult, compressPoint, mod, modInverse, pointAdd, pointDouble, pointMul,
        SECP256K1_N, SECP256K1_P, SECP256K1_GX, SECP256K1_GY,
        HARDENED_OFFSET, masterKeyFromSeed, ckdPriv, deriveSecp256k1Path,
        deriveEd25519Path, ed25519PublicKeyFromSeed,
        keccak256, ripemd160, hash160,
        segwitAddrEncode, segwitAddrDecode, toChecksumAddress,
    };
})(window);
