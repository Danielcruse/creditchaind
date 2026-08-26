package api

import (
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/btcsuite/btcd/txscript"
)

// This file is BTC's equivalent of api/vault_eth_watch.go - detects
// real, confirmation-safe deposits into the shared vault address and
// submits one TxVaultLockAttest per detected deposit, signed by this
// node's own bridge-validator key. See vault_lock_attestation.go's doc
// comment for why this must be genuinely independent per-validator
// verification, not a single node's report.
//
// The one thing ETH/USDC get for free that BTC does not: Solidity's
// deposit()/depositERC20() calls carry the depositor's CreditChain
// address and public key directly as calldata parameters
// (contracts/CreditChainVault.sol). A plain Bitcoin transaction paying
// the vault's P2WSH address has no such field. This is solved the same
// way several existing BTC bridges solve it: the depositor's own
// transaction MUST include a second output that is a provably-
// unspendable OP_RETURN carrying a 52-byte memo - the 20-byte
// CreditChain address followed by the 32-byte CreditChain Ed25519
// public key (see btcVaultDepositMemoLen/decodeBtcVaultDepositMemo
// below). A deposit with no such memo, or a malformed one, is never
// attested - there is no way to credit CCC1 to the right depositor
// without it, and this watch loop never guesses.

const (
	// btcVaultWatchPollInterval mirrors vaultEthWatchPollInterval's and
	// btcBridgeDepositPollInterval's shared reasoning - not latency-
	// sensitive, keep the public Esplora API call volume low.
	btcVaultWatchPollInterval = 30 * time.Second

	// btcVaultDepositMemoLen is 20 (CreditChain address) + 32
	// (CreditChain Ed25519 public key) bytes.
	btcVaultDepositMemoLen = 52
)

// encodeBtcVaultDepositMemo builds the exact 52-byte OP_RETURN payload a
// depositor's wallet must embed in their vault-deposit transaction.
// Exported as a plain byte-building helper (not a full script builder)
// so both a future client-side signer and this file's own tests can
// share the identical layout definition - the authoritative source of
// truth for the memo format, since there is no on-chain schema like
// Solidity's ABI to derive it from.
func encodeBtcVaultDepositMemo(ccAddressHex, publicKeyHex string) ([]byte, error) {
	ccAddr, err := hex.DecodeString(ccAddressHex)
	if err != nil || len(ccAddr) != 20 {
		return nil, fmt.Errorf("encodeBtcVaultDepositMemo: creditChainAddress must be 20 bytes hex")
	}
	pub, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pub) != 32 {
		return nil, fmt.Errorf("encodeBtcVaultDepositMemo: publicKey must be 32 bytes hex (Ed25519)")
	}
	memo := make([]byte, 0, btcVaultDepositMemoLen)
	memo = append(memo, ccAddr...)
	memo = append(memo, pub...)
	return memo, nil
}

// decodeBtcVaultDepositMemo scans a transaction's outputs for a single
// well-formed OP_RETURN memo and splits it back into the CreditChain
// address and public key, both hex-encoded (the exact string shapes
// vaultLockAttestData.DepositorCcAddress/DepositorPublicKey expect - see
// api/vault_lock.go). Returns ok=false (never an error) for any output
// set that doesn't carry exactly one well-formed memo - a missing or
// malformed memo means this deposit is simply never attestable, not a
// caller bug.
func decodeBtcVaultDepositMemo(vout []btcTxOutDetail) (ccAddressHex, publicKeyHex string, ok bool) {
	for _, out := range vout {
		if out.ScriptPubKeyType != "op_return" {
			continue
		}
		script, err := hex.DecodeString(out.ScriptPubKey)
		if err != nil {
			continue
		}
		if txscript.GetScriptClass(script) != txscript.NullDataTy {
			continue
		}
		pushes, err := txscript.PushedData(script)
		if err != nil || len(pushes) != 1 || len(pushes[0]) != btcVaultDepositMemoLen {
			continue
		}
		data := pushes[0]
		return hex.EncodeToString(data[:20]), hex.EncodeToString(data[20:]), true
	}
	return "", "", false
}

// btcVaultDepositOutputIndex finds which output of a vault deposit
// transaction actually pays the shared P2WSH vault address, returning
// its index and value - the ExternalIndex/AmountSmallestUnit a
// TxVaultLockAttest needs. A transaction with no output paying the
// vault address at all isn't a deposit to it (ok=false).
func btcVaultDepositOutputIndex(vout []btcTxOutDetail, vaultAddress string) (index uint32, sats uint64, ok bool) {
	for i, out := range vout {
		if out.ScriptPubKeyAddress == vaultAddress {
			return uint32(i), out.Value, true
		}
	}
	return 0, 0, false
}

// startBtcVaultWatchLoop mirrors startVaultEthWatchLoop's degrade-
// gracefully structure: no-ops entirely if this node isn't a configured
// bridge validator, or if there are no registered bridge validators'
// BTC public keys yet to even derive a shared vault address from.
func startBtcVaultWatchLoop() {
	if _, _, ok := bridgeValidatorSigningKey(); !ok {
		return
	}

	go func() {
		ticker := time.NewTicker(btcVaultWatchPollInterval)
		defer ticker.Stop()
		for range ticker.C {
			scanBtcVaultDepositsOnce()
		}
	}()
	log.Println("BTC vault watch loop started")
}

// scanBtcVaultDepositsOnce re-derives the current shared vault address
// from the live BridgeValidatorStore snapshot every poll (deliberately
// not cached - a validator-set rotation must be picked up automatically,
// exactly like the ETH watch loop always reads the CURRENT contract's
// Locked events, never a stale one), lists its confirmed UTXOs, and
// attests every one that is both deep enough
// (vaultBtcConfirmations()) and carries a well-formed depositor memo.
//
// Unlike the ETH watch loop, this deliberately keeps no persisted
// "last scanned" cursor: it re-examines the vault address's full
// current UTXO set every poll. That's safe (vaultLockAttestationID's
// dedup on chain+txid+vout, plus applyVaultLockAttest's per-validator
// already-attested guard, make a re-submitted attestation a harmless
// no-op) and simpler than tracking a cursor for a small pre-audit UTXO
// set - see the plan's Phase C scope notes for why this simplicity
// trade-off is acceptable at this stage.
func scanBtcVaultDepositsOnce() {
	validators := bridgeValidatorStore.Snapshot()
	vaultAddress, err := btcVaultAddressString(validators)
	if err != nil {
		// Not enough registered BTC-capable validators yet to form a
		// vault address at all - nothing to scan.
		return
	}

	utxos, err := btcAddressUtxos(vaultAddress)
	if err != nil {
		log.Println("BTC vault watch loop: failed to fetch vault UTXOs:", err)
		return
	}

	tip, err := btcChainTipHeight()
	if err != nil {
		log.Println("BTC vault watch loop: failed to fetch chain tip height:", err)
		return
	}
	confirmations := uint32(vaultBtcConfirmations())

	for _, utxo := range utxos {
		if !utxo.Status.Confirmed {
			continue
		}
		if tip < utxo.Status.BlockHeight || tip-utxo.Status.BlockHeight+1 < confirmations {
			continue // not yet at the required confirmation depth
		}

		tx, err := btcTxByID(utxo.Txid)
		if err != nil {
			log.Println("BTC vault watch loop: failed to fetch tx detail for", utxo.Txid, ":", err)
			continue
		}

		ccAddressHex, publicKeyHex, ok := decodeBtcVaultDepositMemo(tx.Vout)
		if !ok {
			// No valid depositor memo - this deposit can never be
			// attributed to a CreditChain wallet, so it is never
			// attested. It stays real, locked BTC that simply has no
			// corresponding CCC1 mint - a stuck-funds edge case
			// intentionally out of scope here (recovery tooling for a
			// memo-less deposit is a separate, disclosed follow-up).
			continue
		}

		voutIndex, sats, ok := btcVaultDepositOutputIndex(tx.Vout, vaultAddress)
		if !ok || voutIndex != utxo.Vout {
			continue
		}

		submitVaultLockAttest(vaultLockAttestData{
			Chain:              "BTC",
			ExternalTxHash:     utxo.Txid,
			ExternalIndex:      utxo.Vout,
			DepositorCcAddress: ccAddressHex,
			DepositorPublicKey: publicKeyHex,
			AmountSmallestUnit: fmt.Sprintf("%d", sats),
		})
	}
}
