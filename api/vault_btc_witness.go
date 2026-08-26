package api

import (
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// This file assembles the final P2WSH witness stack for a signed vault
// release input, once enough bridge-validator signatures exist. The one
// thing that MUST be gotten exactly right here, flagged during Phase C
// research: OP_CHECKMULTISIG's witness verification walks the supplied
// signatures and the redeem script's pubkeys IN LOCKSTEP, advancing past
// a pubkey the moment one signature fails to verify against it. That
// means signatures must be ordered to match the exact order their
// pubkeys appear in the redeem script - NOT the order validators
// happened to submit their TxVaultReleaseApprove transactions in.
// Getting this wrong doesn't throw a clear error - it just makes the
// witness invalid (a node will reject the broadcast, or worse, a
// signature silently fails to be checked against the pubkey a naive
// ordering assumed).

// btcVaultSigHashTypeByte is the single SIGHASH type byte appended to
// every DER-encoded ECDSA signature before it goes into a witness stack -
// standard Bitcoin script signature encoding, distinct from the DER
// bytes BtcVaultSigner.Sign (api/vault_btc_signer.go) returns on their
// own.
func btcVaultSigHashTypeByte(hashType txscript.SigHashType) byte {
	return byte(hashType)
}

// AssembleBtcVaultWitness builds the ordered witness stack for spending
// one P2WSH multisig vault input.
//
//   - sortedPubKeys must be the EXACT slice (same order) that was passed
//     to txscript.MultiSigScript when redeemScript was built
//     (BuildBtcVaultRedeemScript, api/vault_btc.go's
//     sortedBtcVaultPublicKeys) - this is what makes "script order"
//     well-defined and reproducible by every node.
//   - derSigsByPubKeyHex maps a compressed-pubkey hex string to that
//     signer's DER signature (WITHOUT the trailing sighash-type byte -
//     exactly what BtcVaultSigner.Sign returns). Deliberately keyed by
//     pubkey, not by bridge-validator CreditChainAddress - ordering
//     against the redeem script only cares about pubkeys; translating
//     from core.VaultReleaseApproval.Signatures' CreditChainAddress keys
//     to pubkey-hex keys is the caller's job (the release-relay, a
//     separate deferred follow-up per the plan's Phase C notes), via
//     core.BridgeValidator.BtcPublicKeyHex.
//   - threshold is the exact M from the OP_M...OP_N OP_CHECKMULTISIG
//     redeem script. If more than `threshold` signatures are available
//     (e.g. a validator signed after threshold was already reached),
//     only the first `threshold` of them IN SCRIPT ORDER are used -
//     OP_CHECKMULTISIG requires exactly M signature items, not "at
//     least M".
func AssembleBtcVaultWitness(
	redeemScript []byte,
	sortedPubKeys [][]byte,
	derSigsByPubKeyHex map[string][]byte,
	threshold int,
	hashType txscript.SigHashType,
) (wire.TxWitness, error) {
	if threshold < 1 {
		return nil, fmt.Errorf("AssembleBtcVaultWitness: threshold must be at least 1, got %d", threshold)
	}

	orderedSigs := make([][]byte, 0, threshold)
	for _, pubKey := range sortedPubKeys {
		if len(orderedSigs) == threshold {
			break
		}
		derSig, ok := derSigsByPubKeyHex[hex.EncodeToString(pubKey)]
		if !ok {
			continue
		}
		sigWithType := make([]byte, 0, len(derSig)+1)
		sigWithType = append(sigWithType, derSig...)
		sigWithType = append(sigWithType, btcVaultSigHashTypeByte(hashType))
		orderedSigs = append(orderedSigs, sigWithType)
	}

	if len(orderedSigs) < threshold {
		return nil, fmt.Errorf("AssembleBtcVaultWitness: only %d of the required %d signatures matched a pubkey in the redeem script", len(orderedSigs), threshold)
	}

	// Witness stack shape for P2WSH OP_CHECKMULTISIG: [ emptyDummy, sig1,
	// ..., sigM, redeemScript ]. The leading empty element compensates
	// for OP_CHECKMULTISIG's own historical off-by-one stack-pop bug -
	// every standard multisig script (P2WSH or legacy) still requires
	// it, even though the bug itself is otherwise inert here.
	witness := make(wire.TxWitness, 0, len(orderedSigs)+2)
	witness = append(witness, []byte{})
	witness = append(witness, orderedSigs...)
	witness = append(witness, redeemScript)
	return witness, nil
}
