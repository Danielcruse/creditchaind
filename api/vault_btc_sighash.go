package api

import (
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// This file computes the real BIP143 (segwit v0) signature hash for a
// P2WSH vault input, via btcsuite/btcd's own txscript package rather
// than hand-rolled sighash math (unlike website/js/vendor/btc-tx.js's
// existing hand-rolled BIP143 P2WPKH implementation, which was forced
// by a browser-only constraint - see api/vault_btc.go's file-level doc
// comment). What needs verifying here is "did we drive the library
// correctly", not "did we reinvent BIP143 correctly".

// btcVaultPrevOutFetcher builds a txscript.PrevOutputFetcher covering
// every input a vault release transaction spends. Deliberately uses
// txscript.NewMultiPrevOutFetcher rather than NewCannedPrevOutputFetcher -
// a canned fetcher only knows a single (script, amount) pair and silently
// returns it for every outpoint queried, which would be wrong the moment
// a release spends more than one vault UTXO (the ordinary case once the
// vault has received more than one deposit).
func btcVaultPrevOutFetcher(prevOuts map[wire.OutPoint]*wire.TxOut) *txscript.MultiPrevOutFetcher {
	return txscript.NewMultiPrevOutFetcher(prevOuts)
}

// ComputeBtcVaultWitnessSighash computes the exact 32-byte BIP143 digest
// each bridge validator must independently sign (via BtcVaultSigner.Sign,
// api/vault_btc_signer.go) for input `inputIndex` of a vault release
// transaction. `redeemScript` is the P2WSH "script code" BIP143 requires
// here - the actual OP_M...OP_CHECKMULTISIG script (BuildBtcVaultRedeemScript,
// api/vault_btc.go), NOT the P2WSH scriptPubKey (OP_0 <32-byte-hash>) the
// UTXO itself carries; BIP143 defines script code as the redeem script
// for a P2WSH input with no OP_CODESEPARATOR, which the vault's
// redeem script never contains.
//
// `prevOuts` must contain an entry for every input referenced by `tx`
// (not just `inputIndex`) - BIP143's hashPrevouts/hashSequence midstate
// (computed once by txscript.NewTxSigHashes and reused across every
// input's sighash) depends on every input's amount and pkScript, exactly
// the same "compute once, reuse across N inputs" optimization BIP143
// itself was designed around.
func ComputeBtcVaultWitnessSighash(
	tx *wire.MsgTx,
	inputIndex int,
	redeemScript []byte,
	prevOuts map[wire.OutPoint]*wire.TxOut,
	hashType txscript.SigHashType,
) ([32]byte, error) {
	var out [32]byte

	if inputIndex < 0 || inputIndex >= len(tx.TxIn) {
		return out, fmt.Errorf("ComputeBtcVaultWitnessSighash: input index %d out of range (tx has %d inputs)", inputIndex, len(tx.TxIn))
	}

	prevOut, ok := prevOuts[tx.TxIn[inputIndex].PreviousOutPoint]
	if !ok {
		return out, fmt.Errorf("ComputeBtcVaultWitnessSighash: no prev-output entry for input %d's outpoint %s", inputIndex, tx.TxIn[inputIndex].PreviousOutPoint)
	}

	fetcher := btcVaultPrevOutFetcher(prevOuts)
	sigHashes := txscript.NewTxSigHashes(tx, fetcher)

	digest, err := txscript.CalcWitnessSigHash(redeemScript, sigHashes, hashType, tx, inputIndex, prevOut.Value)
	if err != nil {
		return out, fmt.Errorf("ComputeBtcVaultWitnessSighash: CalcWitnessSigHash: %w", err)
	}
	if len(digest) != 32 {
		return out, fmt.Errorf("ComputeBtcVaultWitnessSighash: unexpected digest length %d, want 32", len(digest))
	}
	copy(out[:], digest)
	return out, nil
}
