// vault.js - shared helper for the lock-and-mint bridge vault
// (ETH_CCC1/USDC_CCC1/BTC_CCC1) - the replacement for the old mirror-
// model bridge (ETHB/BTCB), which auto-credited a ledger balance from a
// bare balance-poll with nothing locked on the real chain. See
// core/vault_lock_attestation.go's doc comment for why a real on-chain
// lock, confirmed independently by multiple bridge validators, is what
// actually closes that gap.
//
// Depends on api.js (apiGet/apiPost/apiPostSignedRaw/hexToBytes/
// bytesToHex/requireWallet) and, for the pieces that need them,
// vendor/hd-crypto.js + vendor/eth-tx.js + vendor/btc-tx.js.

const VAULT_CHAIN_INFO = {
    ETH:   { ccc1Symbol: "ETH_CCC1",   decimals: 18 },
    USDC:  { ccc1Symbol: "USDC_CCC1",  decimals: 6 },
    BTC:   { ccc1Symbol: "BTC_CCC1",   decimals: 8 },
    STETH: { ccc1Symbol: "STETH_CCC1", decimals: 18 },
};

// decimalToSmallest/smallestToDecimal are cctToSmallest/cctFromSmallest
// (api.js) generalized to an arbitrary decimal count - ETH_CCC1 (18,
// like CCT), USDC_CCC1 (6), BTC_CCC1 (8, though BTC_CCC1 balances are
// actually returned as a plain sats integer server-side, never a decimal
// string - see fetchCcc1Balance).
function decimalToSmallest(displayStr, decimals) {
    const s = String(displayStr).trim();
    const [wholePart, fracPartRaw = ""] = s.split(".");
    const fracPart = (fracPartRaw + "0".repeat(decimals)).slice(0, decimals);
    const whole = BigInt(wholePart || "0");
    const frac = BigInt(fracPart || "0");
    return (whole * (10n ** BigInt(decimals)) + frac).toString();
}

function smallestToDecimal(smallestStr, decimals) {
    if (smallestStr === undefined || smallestStr === null || smallestStr === "") return "0";
    const base = 10n ** BigInt(decimals);
    const n = BigInt(smallestStr);
    const whole = n / base;
    const frac = (n % base).toString().padStart(decimals, "0").replace(/0+$/, "") || "0";
    return `${whole}.${frac}`;
}

// --- balance address resolution (mirrors api/subaddress.go) ----------

const BTC_CCC1_ADDRESS_TAG = "BTC_CCC1-SUBADDRESS";

// vaultBalanceAddress mirrors resolveBalanceAddress's ETH_CCC1/
// USDC_CCC1/BTC_CCC1 branches exactly: ETH_CCC1/USDC_CCC1 unify with the
// wallet's real self-custodial ETH address (a vault-backed balance
// corresponds 1:1 to funds locked FROM that real address), BTC_CCC1
// follows BTCB's derived-sub-address precedent.
async function vaultBalanceAddress(wallet, chain) {
    if (chain === "ETH" || chain === "USDC" || chain === "STETH") {
        // STETH_CCC1 unifies with the same real self-custodial ETH
        // address as ETH_CCC1/USDC_CCC1 - stETH is an ERC-20 held at
        // whatever address holds it, not a separate derived one (see
        // api/subaddress.go's resolveBalanceAddress, the server-side
        // mirror of this exact resolution).
        return getEthbAddress(wallet); // reuses api.js's real self-custodial resolution
    }
    return deriveTokenAddress(wallet.publicKey, BTC_CCC1_ADDRESS_TAG);
}

async function fetchCcc1Balance(wallet, chain) {
    const info = VAULT_CHAIN_INFO[chain];
    const address = await vaultBalanceAddress(wallet, chain);
    const result = await apiGet(`/vault/balance?symbol=${info.ccc1Symbol}&address=${address}`);
    return { address, balance: result.balance };
}

// --- vault info / status polling --------------------------------------

function fetchVaultInfo(chain) {
    return apiGet(`/vault/info?chain=${chain}`);
}

// depositor is optional ({ ccAddress, publicKey, amountSmallestUnit }) -
// needed to locate a genuinely NEW deposit's status (see
// api/vault_lock.go's HandleVaultLockStatus doc comment, audit finding
// C-05's full fix) - omit it only when polling an event that already
// existed before that fix shipped.
function fetchLockStatus(chain, externalTxHash, externalIndex, depositor) {
    let url = `/vault/lock-status?chain=${chain}&externalTxHash=${externalTxHash}&externalIndex=${externalIndex || 0}`;
    if (depositor) {
        url +=
            `&depositorCcAddress=${encodeURIComponent(depositor.ccAddress)}` +
            `&depositorPublicKey=${encodeURIComponent(depositor.publicKey)}` +
            `&amountSmallestUnit=${encodeURIComponent(depositor.amountSmallestUnit)}`;
    }
    return apiGet(url);
}

function fetchReleaseStatus(burnTxId) {
    return apiGet(`/vault/release-status?burnTxId=${burnTxId}`);
}

// pollUntil calls fetchFn every intervalMs until it returns a value for
// which isDone(value) is true, or maxAttempts is reached - the shared
// shape every vault status page needs (lock confirmation count, release
// confirmation count), instead of each page re-implementing its own
// setInterval loop.
function pollUntil(fetchFn, isDone, onUpdate, { intervalMs = 5000, maxAttempts = 120 } = {}) {
    let attempts = 0;
    const handle = setInterval(async () => {
        attempts++;
        try {
            const value = await fetchFn();
            onUpdate(value);
            if (isDone(value) || attempts >= maxAttempts) {
                clearInterval(handle);
            }
        } catch (e) {
            // A transient fetch failure shouldn't kill the poll - just
            // skip this tick and try again next interval.
            if (attempts >= maxAttempts) clearInterval(handle);
        }
    }, intervalMs);
    return handle;
}

// --- ETH/USDC vault ABI calldata (deposit/depositERC20) ---------------
//
// Hand-encoded per Solidity's ABI encoding rules - the exact same
// function signatures api/vault_contract_abi.go's vaultContractABIJSON
// declares, so the selector this computes at runtime (keccak256 of the
// signature string, first 4 bytes) matches go-ethereum's own Pack()
// call byte-for-byte without needing to hardcode and separately verify a
// constant.

function abiSelector(signature) {
    const sigBytes = new TextEncoder().encode(signature);
    return HDCrypto.keccak256(sigBytes).slice(0, 4);
}

// abiEncodeBytesN right-pads (left-aligns) fixed-size bytesN types
// within their 32-byte slot - the opposite convention from
// address/uint, which are left-padded (right-aligned). Getting this
// backwards is a classic, silent ABI-encoding bug.
function abiEncodeBytesN(bytes, n) {
    if (bytes.length !== n) throw new Error(`expected exactly ${n} bytes, got ${bytes.length}`);
    const out = new Uint8Array(32);
    out.set(bytes, 0);
    return out;
}

function abiEncodeAddress(addressBytes20) {
    if (addressBytes20.length !== 20) throw new Error("address must be 20 bytes");
    const out = new Uint8Array(32);
    out.set(addressBytes20, 12);
    return out;
}

function abiEncodeUint256(value) {
    let v = BigInt(value);
    const out = new Uint8Array(32);
    for (let i = 31; i >= 0; i--) {
        out[i] = Number(v & 0xffn);
        v >>= 8n;
    }
    return out;
}

// buildDepositCalldata builds calldata for deposit(bytes20,bytes32) -
// plain ETH deposits. ccAddressHex/publicKeyHex are the depositor's own
// CreditChain wallet address (20 bytes) and Ed25519 public key (32
// bytes), hex-encoded, exactly as contracts/CreditChainVault.sol expects
// them.
function buildDepositCalldata(ccAddressHex, publicKeyHex) {
    const selector = abiSelector("deposit(bytes20,bytes32)");
    const ccBytes = hexToBytes(ccAddressHex);
    const pubBytes = hexToBytes(publicKeyHex);
    return concatUint8Arrays(selector, abiEncodeBytesN(ccBytes, 20), abiEncodeBytesN(pubBytes, 32));
}

// buildDepositERC20Calldata builds calldata for
// depositERC20(address,uint256,bytes20,bytes32) - USDC deposits. The
// caller must have ALREADY sent a separate ERC-20 approve() transaction
// for at least `amountSmallestUnit` beforehand (standard approve+
// transferFrom, matching contracts/CreditChainVault.sol's own doc
// comment on why this isn't EIP-2612 permit).
function buildDepositERC20Calldata(tokenAddressHex, amountSmallestUnit, ccAddressHex, publicKeyHex) {
    const selector = abiSelector("depositERC20(address,uint256,bytes20,bytes32)");
    const tokenBytes = hexToBytes(tokenAddressHex.replace(/^0x/i, ""));
    const ccBytes = hexToBytes(ccAddressHex);
    const pubBytes = hexToBytes(publicKeyHex);
    return concatUint8Arrays(
        selector,
        abiEncodeAddress(tokenBytes),
        abiEncodeUint256(amountSmallestUnit),
        abiEncodeBytesN(ccBytes, 20),
        abiEncodeBytesN(pubBytes, 32)
    );
}

// buildErc20ApproveCalldata builds calldata for the standard
// approve(address,uint256) call every ERC-20 exposes - needed once
// before the first depositERC20() call (or whenever a larger deposit
// exceeds the previously-approved allowance).
function buildErc20ApproveCalldata(spenderAddressHex, amountSmallestUnit) {
    const selector = abiSelector("approve(address,uint256)");
    const spenderBytes = hexToBytes(spenderAddressHex.replace(/^0x/i, ""));
    return concatUint8Arrays(selector, abiEncodeAddress(spenderBytes), abiEncodeUint256(amountSmallestUnit));
}

// buildLidoSubmitCalldata builds calldata for Lido's real
// submit(address) payable function - the ETH liquid-staking flow's
// second step (see website/js/lido-stake.js): a plain ETH transfer to
// Lido's contract with this calldata attached, value = the amount of
// ETH being staked. _referral is optional (Lido's own referral-
// program address, zero address = none) - always the zero address
// here, this app has no referral program integration.
const LIDO_ZERO_REFERRAL = "0x0000000000000000000000000000000000000000";
function buildLidoSubmitCalldata(referralAddressHex) {
    const selector = abiSelector("submit(address)");
    const referralBytes = hexToBytes((referralAddressHex || LIDO_ZERO_REFERRAL).replace(/^0x/i, ""));
    return concatUint8Arrays(selector, abiEncodeAddress(referralBytes));
}

// buildReleaseApprovalDigest reproduces contracts/CreditChainVault.sol's
// release() digest EXACTLY: keccak256(abi.encode(to, token, amount,
// releaseId, address(this), block.chainid)) - a bridge validator signs
// this raw digest (no EIP-191 prefix, see _recoverSigner's doc comment
// in the contract) with their own relayer key (EthTx.signDigest) to
// produce the signatureHex TxVaultReleaseApprove needs. `to`/`token`/
// `vaultContractAddressHex` are 0x-prefixed 20-byte addresses; token is
// "0x0000000000000000000000000000000000000000" for ETH. Any mismatch
// here (wrong chainId, wrong vault address, wrong releaseId) produces a
// signature that simply fails ecrecover verification on-chain - it can
// never authorize a release for anything other than the exact
// (to, token, amount, releaseId) it was computed for.
function buildReleaseApprovalDigest(toAddressHex, tokenAddressHex, amountSmallestUnit, releaseId, vaultContractAddressHex, chainId) {
    const toBytes = hexToBytes(toAddressHex.replace(/^0x/i, ""));
    const tokenBytes = hexToBytes(tokenAddressHex.replace(/^0x/i, ""));
    const vaultBytes = hexToBytes(vaultContractAddressHex.replace(/^0x/i, ""));
    const encoded = concatUint8Arrays(
        abiEncodeAddress(toBytes),
        abiEncodeAddress(tokenBytes),
        abiEncodeUint256(amountSmallestUnit),
        abiEncodeUint256(releaseId),
        abiEncodeAddress(vaultBytes),
        abiEncodeUint256(chainId)
    );
    return HDCrypto.keccak256(encoded);
}

// abiEncodeStringTail/abiEncodeAddressArrayTail build the TAIL-section
// bytes for a dynamic `string`/`address[]` argument to abi.encode - a
// 32-byte length prefix followed by the data (string data is
// right-padded to the next 32-byte word, same as Solidity's own
// `bytes`/`string` ABI encoding; an address[] tail is just N
// consecutive 32-byte left-padded addresses, no extra padding needed
// since each entry is already word-sized).
function abiEncodeStringTail(str) {
    const strBytes = new TextEncoder().encode(str);
    const padded = new Uint8Array(Math.ceil(strBytes.length / 32) * 32);
    padded.set(strBytes, 0);
    return concatUint8Arrays(abiEncodeUint256(strBytes.length), padded);
}

function abiEncodeAddressArrayTail(addressHexArray) {
    const parts = [abiEncodeUint256(addressHexArray.length)];
    for (const addrHex of addressHexArray) {
        parts.push(abiEncodeAddress(hexToBytes(addrHex.replace(/^0x/i, ""))));
    }
    return concatUint8Arrays(...parts);
}

// buildSetValidatorsDigest mirrors contracts/CreditChainVault.sol's
// setValidators digest exactly: keccak256(abi.encode("SET_VALIDATORS",
// newValidators, newThreshold, actionNonce, address(this),
// block.chainid)) - unlike buildReleaseApprovalDigest's all-static
// fields, the string and address[] arguments here are DYNAMIC types, so
// abi.encode's head section holds OFFSETS into a tail section for
// those two, not their values directly (see the ABI spec's
// head/tail encoding for dynamic types) - see
// api/vault_set_validators_relay.go's own doc comment for the Go-side
// twin this must stay byte-for-byte identical to.
function buildSetValidatorsDigest(newValidatorsHexArray, newThreshold, actionNonce, vaultContractAddressHex, chainId) {
    const stringTail = abiEncodeStringTail("SET_VALIDATORS");
    const arrayTail = abiEncodeAddressArrayTail(newValidatorsHexArray);

    const headSlotCount = 6;
    const stringOffset = headSlotCount * 32;
    const arrayOffset = stringOffset + stringTail.length;

    const vaultBytes = hexToBytes(vaultContractAddressHex.replace(/^0x/i, ""));
    const head = concatUint8Arrays(
        abiEncodeUint256(stringOffset),
        abiEncodeUint256(arrayOffset),
        abiEncodeUint256(newThreshold),
        abiEncodeUint256(actionNonce),
        abiEncodeAddress(vaultBytes),
        abiEncodeUint256(chainId)
    );

    return HDCrypto.keccak256(concatUint8Arrays(head, stringTail, arrayTail));
}

// buildSetTokenAllowedDigest/buildSetPerDepositorCapsDigest mirror
// api/vault_set_token_allowed_relay.go's buildSetTokenAllowedDigestGo/
// buildSetPerDepositorCapsDigestGo exactly - see that file's own doc
// comment for why both exist (enabling Lido's real stETH as a
// depositable vault token, and disabling the per-depositor USDC-scaled
// cap that can't correctly evaluate an 18-decimal token). Unlike
// buildSetValidatorsDigest, every non-string argument here is a
// STATIC type (address/bool/uint256, no address[]) - simpler head/tail
// layout, only the leading string literal needs a tail at all.
function buildSetTokenAllowedDigest(tokenAddressHex, allowed, actionNonce, vaultContractAddressHex, chainId) {
    const stringTail = abiEncodeStringTail("SET_TOKEN_ALLOWED");
    const headSlotCount = 6;
    const stringOffset = headSlotCount * 32;

    const tokenBytes = hexToBytes(tokenAddressHex.replace(/^0x/i, ""));
    const vaultBytes = hexToBytes(vaultContractAddressHex.replace(/^0x/i, ""));
    const head = concatUint8Arrays(
        abiEncodeUint256(stringOffset),
        abiEncodeAddress(tokenBytes),
        abiEncodeUint256(allowed ? 1 : 0),
        abiEncodeUint256(actionNonce),
        abiEncodeAddress(vaultBytes),
        abiEncodeUint256(chainId)
    );

    return HDCrypto.keccak256(concatUint8Arrays(head, stringTail));
}

function buildSetPerDepositorCapsDigest(newCapWei, newCapUsdc, actionNonce, vaultContractAddressHex, chainId) {
    const stringTail = abiEncodeStringTail("SET_PER_DEPOSITOR_CAPS");
    const headSlotCount = 6;
    const stringOffset = headSlotCount * 32;

    const vaultBytes = hexToBytes(vaultContractAddressHex.replace(/^0x/i, ""));
    const head = concatUint8Arrays(
        abiEncodeUint256(stringOffset),
        abiEncodeUint256(newCapWei),
        abiEncodeUint256(newCapUsdc),
        abiEncodeUint256(actionNonce),
        abiEncodeAddress(vaultBytes),
        abiEncodeUint256(chainId)
    );

    return HDCrypto.keccak256(concatUint8Arrays(head, stringTail));
}

function concatUint8Arrays(...arrays) {
    const total = arrays.reduce((n, a) => n + a.length, 0);
    const out = new Uint8Array(total);
    let offset = 0;
    for (const a of arrays) {
        out.set(a, offset);
        offset += a.length;
    }
    return out;
}

// --- BTC vault deposit (P2WSH address + OP_RETURN depositor memo) -----
//
// Reuses BtcTx._internal's already-BIP143-test-vector-verified signing
// primitives (website/js/vendor/btc-tx.js) rather than duplicating them -
// only the OUTPUT SET differs from a plain send (a P2WSH destination
// instead of P2WPKH, plus an extra OP_RETURN memo output), input signing
// is byte-for-byte the same P2WPKH BIP143 flow BtcTx.signAndBuildTx
// already uses. See api/vault_btc_watch.go's doc comment for exactly why
// this memo is required: a plain Bitcoin transaction has no field to
// carry the depositor's CreditChain identity the way Solidity's
// deposit() calldata does for free.

// buildBtcVaultDepositMemo mirrors api/vault_btc_watch.go's
// encodeBtcVaultDepositMemo exactly - 20-byte CreditChain address + 32-
// byte CreditChain Ed25519 public key, 52 bytes total.
function buildBtcVaultDepositMemo(ccAddressHex, publicKeyHex) {
    const ccBytes = hexToBytes(ccAddressHex);
    const pubBytes = hexToBytes(publicKeyHex);
    if (ccBytes.length !== 20) throw new Error("creditChainAddress must be 20 bytes");
    if (pubBytes.length !== 32) throw new Error("publicKey must be 32 bytes (Ed25519)");
    return concatUint8Arrays(ccBytes, pubBytes);
}

// opReturnScriptPubKey builds OP_RETURN <len> <data> - data.length must
// be under 76 bytes for the single-byte direct-push encoding used here
// (the 52-byte memo always is).
function opReturnScriptPubKey(data) {
    if (data.length === 0 || data.length >= 76) throw new Error("OP_RETURN data must be 1-75 bytes for this direct-push encoding");
    return concatUint8Arrays(new Uint8Array([0x6a, data.length]), data);
}

// p2wshScriptPubKey builds OP_0 <32-byte-hash> - the vault's P2WSH
// destination script, distinct from BtcTx's own p2wpkhScriptPubKey
// (which is 20-byte-program only and not exposed for reuse here anyway
// since this is a different script shape).
function p2wshScriptPubKey(witnessProgram32) {
    if (witnessProgram32.length !== 32) throw new Error("P2WSH witness program must be 32 bytes");
    return concatUint8Arrays(new Uint8Array([0x00, 0x20]), witnessProgram32);
}

// buildAndSignBtcVaultDepositTx builds, signs, and serializes a
// complete BTC vault deposit transaction: one output paying the vault's
// P2WSH address, one OP_RETURN output carrying the depositor memo, and
// (if there's change) one P2WPKH change output back to the sender's own
// address. Inputs are the sender's own P2WPKH UTXOs, signed exactly like
// BtcTx.signAndBuildTx's own loop (BIP143, SIGHASH_ALL).
async function buildAndSignBtcVaultDepositTx(privateKeyHex, { utxos, vaultAddress, amountSats, feeSats, changeAddress, ccAddressHex, publicKeyHex }) {
    if (amountSats <= 0) throw new Error("amountSats must be positive");
    if (feeSats < 0) throw new Error("feeSats must not be negative");

    const internal = BtcTx._internal;

    const vaultDecoded = HDCrypto.segwitAddrDecode(vaultAddress);
    if (vaultDecoded.witver !== 0 || vaultDecoded.witprogBytes.length !== 32) {
        throw new Error("vault address must be a P2WSH (32-byte witness program) address");
    }
    const changeDecoded = HDCrypto.segwitAddrDecode(changeAddress);

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
    // Bitcoin's own mempool/relay policy rejects any output below the
    // dust threshold unless the WHOLE transaction pays zero fee
    // ("sendrawtransaction RPC error -26: dust, tx with dust output
    // must be 0-fee") - a real broadcast failure this codebase already
    // guards against server-side for releases (see
    // api/vault_btc_release_relay.go's vaultBtcDustThresholdSats) but
    // was missing here for deposits. A too-small leftover is simply
    // absorbed into the fee (no change output at all) rather than
    // creating an output real Bitcoin nodes won't relay.
    const changeSatsRaw = total - target;
    const changeSats = changeSatsRaw >= 546n ? changeSatsRaw : 0n;

    const privKeyBigInt = HDCrypto.bytesToBigInt(hexToBytes(privateKeyHex.replace(/^0x/i, "")));
    const pubPoint = HDCrypto.scalarBaseMult(privKeyBigInt);
    const pubCompressed = HDCrypto.compressPoint(pubPoint);
    const senderHash160 = await HDCrypto.hash160(pubCompressed);
    const scriptCode = internal.p2wpkhScriptCode(senderHash160);

    const version = 2;
    const locktime = 0;
    const inputs = selected.map((u) => ({ txid: u.txid, vout: u.vout, sequence: 0xffffffff, valueSats: u.value }));

    const memo = buildBtcVaultDepositMemo(ccAddressHex, publicKeyHex);
    const outputs = [
        { valueSats: amountSats, scriptPubKey: p2wshScriptPubKey(vaultDecoded.witprogBytes) },
        { valueSats: 0, scriptPubKey: opReturnScriptPubKey(memo) },
    ];
    if (changeSats > 0n) {
        outputs.push({ valueSats: Number(changeSats), scriptPubKey: internal.p2wpkhScriptPubKey(changeDecoded.witprogBytes) });
    }

    const witnesses = [];
    for (let i = 0; i < inputs.length; i++) {
        const { sighash } = await internal.bip143Sighash({
            version,
            inputs,
            inputIndex: i,
            scriptCode,
            amountSats: inputs[i].valueSats,
            outputs,
            locktime,
        });
        const { r, s } = await internal.ecdsaSignBtc(privKeyBigInt, sighash);
        const derSig = internal.derEncodeSignature(r, s);
        const sigWithHashType = concatUint8Arrays(derSig, new Uint8Array([1])); // SIGHASH_ALL
        witnesses.push([sigWithHashType, pubCompressed]);
    }

    const signedBytes = internal.serializeSignedTx({ version, inputs, outputs, witnesses, locktime });
    const rawTxHex = HDCrypto.bytesToHex(signedBytes);

    const legacyBytes = internal.legacySerializeForTxid({ version, inputs, outputs, locktime });
    const txidBytes = internal.reverseBytes(await internal.dsha256(legacyBytes));
    const txid = HDCrypto.bytesToHex(txidBytes);

    return { rawTxHex, txid };
}

// --- withdrawal (burn CCC1 -> real-chain release) ----------------------

// burnForRelease submits a signed TxVaultBurn - the user's own
// CreditChain wallet signature authorizing the burn (standard
// apiPostSignedRaw pattern), not a real external-chain signature (that
// comes from bridge validators separately, see applyVaultReleaseApprove).
async function burnForRelease(chain, destinationExtAddress, amountSmallestUnit) {
    const payloadJSON = `{"chain":${JSON.stringify(chain)},"destinationExtAddress":${JSON.stringify(destinationExtAddress)},"amountSmallestUnit":${JSON.stringify(amountSmallestUnit)}}`;
    return apiPostSignedRaw("/vault/burn", payloadJSON);
}
