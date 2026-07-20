// hdwallet.js - Multi-Chain HD Wallet Phase A: BIP-39 mnemonic +
// BIP-32/SLIP-10 derivation, wrapping website/js/vendor/hd-crypto.js
// and website/js/vendor/bip39-wordlist.js into the app-facing API.
//
// One recovery phrase -> one seed -> three independent chain accounts:
//   - CreditChain: SLIP-10 Ed25519, m/44'/9999'/0'/0' (9999 is a
//     self-assigned, unregistered SLIP-44 coin type - see the Phase A
//     plan's A.2 section for why).
//   - Ethereum:    BIP-32 secp256k1, m/44'/60'/0'/0/0 (standard path,
//     same address a hardware wallet or MetaMask would derive).
//   - Bitcoin:     BIP-32 secp256k1, m/84'/1'/0'/0/0 (BIP-84 native
//     segwit, testnet coin type).
// No BIP-39 passphrase in Phase A.
//
// *** The bundled wordlist (vendor/bip39-wordlist.js) is unverified -
// see that file's header. Do not treat mnemonics generated here as
// safe to import into a third-party wallet until that's resolved. ***

(function (global) {
    "use strict";

    const H = global.HDCrypto.HARDENED_OFFSET;

    // ---------- BIP-39 mnemonic ----------

    function assertWordlist() {
        if (!global.BIP39_WORDLIST_EN || global.BIP39_WORDLIST_EN.length !== 2048) {
            throw new Error("BIP-39 wordlist not loaded or wrong length");
        }
    }

    // 128 bits of entropy -> 12-word mnemonic (checksum = first 4 bits
    // of SHA-256(entropy), appended before splitting into 11-bit groups).
    async function generateMnemonic() {
        assertWordlist();
        const entropy = new Uint8Array(16);
        crypto.getRandomValues(entropy);
        return entropyToMnemonic(entropy);
    }

    async function entropyToMnemonic(entropy) {
        assertWordlist();
        const hash = new Uint8Array(await crypto.subtle.digest("SHA-256", entropy));
        const checksumBits = entropy.length / 4; // 16 bytes -> 4 bits

        // Concatenate entropy bits + checksum bits into one bit string.
        let bits = "";
        for (const b of entropy) bits += b.toString(2).padStart(8, "0");
        let checksumStr = "";
        for (const b of hash) checksumStr += b.toString(2).padStart(8, "0");
        bits += checksumStr.slice(0, checksumBits);

        const words = [];
        for (let i = 0; i < bits.length / 11; i++) {
            const idx = parseInt(bits.slice(i * 11, i * 11 + 11), 2);
            words.push(global.BIP39_WORDLIST_EN[idx]);
        }
        return words.join(" ");
    }

    // Reconstructs entropy from the words and re-checks the checksum -
    // does NOT verify the words are the "real" BIP-39 wordlist (that's
    // the separate, still-open verification item), only that this
    // specific phrase is internally self-consistent.
    async function validateMnemonic(mnemonic) {
        assertWordlist();
        const words = mnemonic.trim().split(/\s+/);
        if (words.length !== 12) return false;

        const wordIndex = new Map(global.BIP39_WORDLIST_EN.map((w, i) => [w, i]));
        let bits = "";
        for (const w of words) {
            const idx = wordIndex.get(w);
            if (idx === undefined) return false;
            bits += idx.toString(2).padStart(11, "0");
        }

        const entropyBits = bits.slice(0, 128);
        const checksumBits = bits.slice(128);
        const entropy = new Uint8Array(16);
        for (let i = 0; i < 16; i++) {
            entropy[i] = parseInt(entropyBits.slice(i * 8, i * 8 + 8), 2);
        }
        const hash = new Uint8Array(await crypto.subtle.digest("SHA-256", entropy));
        let expectedChecksum = "";
        for (const b of hash) expectedChecksum += b.toString(2).padStart(8, "0");
        expectedChecksum = expectedChecksum.slice(0, 4);

        return checksumBits === expectedChecksum;
    }

    async function mnemonicToSeed(mnemonic, passphrase) {
        const mnemonicBytes = new TextEncoder().encode(mnemonic.trim().normalize("NFKD"));
        const salt = new TextEncoder().encode("mnemonic" + (passphrase || "").normalize("NFKD"));
        return global.HDCrypto.pbkdf2Sha512(mnemonicBytes, salt, 2048, 64);
    }

    // ---------- per-chain derivation ----------

    async function deriveCreditChainKeypair(seed) {
        const node = await global.HDCrypto.deriveEd25519Path(seed, [44 + H, 9999 + H, 0 + H, 0 + H]);
        const seed32 = node.key;
        const pubKey = await global.HDCrypto.ed25519PublicKeyFromSeed(seed32);
        // Go's ed25519.PrivateKey format: seed(32) || publicKey(32) -
        // matches what website/js/api.js's importSigningKey expects.
        const privateKey = global.HDCrypto.concatBytes(seed32, pubKey);
        const addressHash = new Uint8Array(await crypto.subtle.digest("SHA-256", pubKey));
        const address = global.HDCrypto.bytesToHex(addressHash.slice(0, 20));
        return {
            address,
            publicKey: global.HDCrypto.bytesToHex(pubKey),
            privateKey: global.HDCrypto.bytesToHex(privateKey),
        };
    }

    async function deriveEthAddress(seed) {
        const node = await global.HDCrypto.deriveSecp256k1Path(seed, [44 + H, 60 + H, 0 + H, 0, 0]);
        const priv = global.HDCrypto.bytesToBigInt(node.key);
        const pub = global.HDCrypto.scalarBaseMult(priv);
        const pubXY = global.HDCrypto.concatBytes(
            global.HDCrypto.bigIntToBytes(pub.x, 32),
            global.HDCrypto.bigIntToBytes(pub.y, 32)
        );
        const addrHash = global.HDCrypto.keccak256(pubXY);
        const rawAddr = global.HDCrypto.bytesToHex(addrHash.slice(12));
        const address = await global.HDCrypto.toChecksumAddress(rawAddr);
        return {
            address,
            publicKey: global.HDCrypto.bytesToHex(global.HDCrypto.compressPoint(pub)),
            privateKey: global.HDCrypto.bytesToHex(node.key),
        };
    }

    async function deriveBtcTestnetAddress(seed) {
        const node = await global.HDCrypto.deriveSecp256k1Path(seed, [84 + H, 1 + H, 0 + H, 0, 0]);
        const priv = global.HDCrypto.bytesToBigInt(node.key);
        const pub = global.HDCrypto.scalarBaseMult(priv);
        const pubCompressed = global.HDCrypto.compressPoint(pub);
        const h160 = await global.HDCrypto.hash160(pubCompressed);
        const address = global.HDCrypto.segwitAddrEncode("tb", 0, h160);
        return {
            address,
            publicKey: global.HDCrypto.bytesToHex(pubCompressed),
            privateKey: global.HDCrypto.bytesToHex(node.key),
        };
    }

    // Derives all three chain accounts from one mnemonic in one call -
    // the primary entry point registration/migration flows should use.
    async function deriveAllAccounts(mnemonic, passphrase) {
        const seed = await mnemonicToSeed(mnemonic, passphrase);
        const [creditchain, eth, btc] = await Promise.all([
            deriveCreditChainKeypair(seed),
            deriveEthAddress(seed),
            deriveBtcTestnetAddress(seed),
        ]);
        return { creditchain, eth, btc };
    }

    global.HDWallet = {
        generateMnemonic,
        validateMnemonic,
        mnemonicToSeed,
        deriveCreditChainKeypair,
        deriveEthAddress,
        deriveBtcTestnetAddress,
        deriveAllAccounts,
    };
})(window);
