package api

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/creditchain/creditchaind/core"
)

// This file is BTC's sibling to api/vault_release_relay.go - the node-
// local relay that actually broadcasts a real BTC release once a
// core.VaultReleaseApproval reaches signature threshold. Unlike ETH/USDC,
// broadcasting needs no funded key at all (Esplora's /tx endpoint is a
// free public relay, see btcBroadcastRawTx) - the transaction is already
// fully authorized the moment enough validator signatures exist; there
// is nothing further to pay for or be trusted to do correctly beyond
// correctly assembling bytes that were already signed.
//
// DISCLOSED PROTOCOL CONSTRAINT (read before touching this file): a real
// Bitcoin release, unlike ETH's release() call, needs each validator to
// sign a BIP143 sighash that commits to the EXACT unsigned transaction
// (which UTXO(s) are spent, in what order, with what outputs) - there is
// no on-chain equivalent of ecrecover-against-an-abstract-digest that
// lets signature validity be independent of transaction shape the way
// contracts/CreditChainVault.sol's release() is. That means every
// bridge validator's own external signing infrastructure (which lives
// entirely outside this codebase - see core/vault_release_approval.go's
// doc comment) MUST independently reconstruct the IDENTICAL unsigned
// transaction this relay builds, with no separate "propose then sign"
// coordination round-trip, or their signature simply won't verify
// against what actually gets broadcast. This file is scoped to a single
// input per release (deterministically selected, see
// selectVaultReleaseUtxo) specifically to keep that reconstruction
// tractable - a release amount exceeding any single available UTXO
// requires either a larger deposit to consolidate against or a future,
// separately-designed multi-input/multi-signature-per-input protocol
// (not built here).

const vaultBtcReleaseRelayPollInterval = 30 * time.Second

// vaultBtcDustThresholdSats mirrors Bitcoin Core's own default dust
// relay threshold for a P2WPKH/P2WSH-shaped output - a change output
// below this would likely be rejected by relay policy or cost more to
// ever spend than it's worth, so it's swept into the fee instead of
// created.
const vaultBtcDustThresholdSats = 546

// startBtcReleaseRelayLoop mirrors startVaultReleaseRelayLoop's
// degrade-gracefully structure.
func startBtcReleaseRelayLoop() {
	if _, _, ok := bridgeValidatorSigningKey(); !ok {
		return
	}

	go func() {
		ticker := time.NewTicker(vaultBtcReleaseRelayPollInterval)
		defer ticker.Stop()
		for range ticker.C {
			scanBtcReleaseApprovalsOnce()
		}
	}()
	log.Println("BTC vault release relay loop started")
}

func scanBtcReleaseApprovalsOnce() {
	threshold := bridgeValidatorThreshold()
	if threshold < 1 {
		return
	}
	validators := bridgeValidatorStore.Snapshot()

	for _, approval := range vaultReleaseApprovalStore.Snapshot() {
		if approval.Chain != "BTC" {
			continue
		}
		if approval.Status != core.VaultReleasePending {
			continue
		}
		if len(approval.Signatures) < threshold {
			continue
		}
		submitBtcVaultRelease(approval, validators, threshold)
	}
}

// vaultReleaseTxVsize estimates a P2WSH-multisig-input release
// transaction's virtual size - a conservative approximation (documented
// as such, same disclosed-approximation posture send-bridged-btc.js's
// own fee estimate already has), NOT a precision fee engine. Every node
// running this relay must compute the SAME estimate for a given
// (threshold, redeemScriptLen, hasChange) input, which a fixed formula
// like this guarantees (unlike a live mempool-based estimator).
func vaultReleaseTxVsize(threshold, redeemScriptLen int, hasChange bool) uint64 {
	const baseVsize = 11                // version(4)+locktime(4)+in-count(1)+out-count(1)+marker/flag amortized
	const perInputNonWitness = 41       // outpoint(36)+scriptSigLen(1,empty)+sequence(4)
	const perOutputVsize = 31           // P2WPKH/P2WSH-ish average output size
	const perSigWitnessBytes = 73       // ~71-72 byte DER sig + 1 sighash-type byte + push-length byte
	const dummyWitnessBytes = 2         // empty push + its length byte

	witnessBytes := dummyWitnessBytes + threshold*perSigWitnessBytes + redeemScriptLen + 3 // +3 for the redeem script's own push-length encoding
	witnessVsize := (uint64(witnessBytes) + 3) / 4 // segwit witness discount, rounded up

	vsize := uint64(baseVsize) + uint64(perInputNonWitness) + witnessVsize + uint64(perOutputVsize)
	if hasChange {
		vsize += perOutputVsize
	}
	return vsize
}

// selectVaultReleaseUtxo deterministically picks ONE confirmed vault
// UTXO to fund a release - every node must reach the identical answer
// given the identical UTXO set, so this sorts (not map-iterates) and
// picks the SMALLEST sufficient UTXO (a stable, simple, reproducible
// rule - not fee-optimal, but determinism matters far more here than
// optimality). Returns ok=false if no single UTXO covers amount + the
// estimated fee - see this file's top doc comment for why a multi-UTXO
// release isn't supported yet.
func selectVaultReleaseUtxo(utxos []BtcUtxo, amountSats uint64, threshold, redeemScriptLen int) (BtcUtxo, uint64, bool) {
	confirmed := make([]BtcUtxo, 0, len(utxos))
	for _, u := range utxos {
		if u.Status.Confirmed {
			confirmed = append(confirmed, u)
		}
	}
	sort.Slice(confirmed, func(i, j int) bool {
		if confirmed[i].Value != confirmed[j].Value {
			return confirmed[i].Value < confirmed[j].Value
		}
		// Tie-break on txid:vout so the ordering (and therefore the
		// selection) is fully deterministic even if two UTXOs share a
		// value.
		if confirmed[i].Txid != confirmed[j].Txid {
			return confirmed[i].Txid < confirmed[j].Txid
		}
		return confirmed[i].Vout < confirmed[j].Vout
	})

	feeWithChange := vaultReleaseTxVsize(threshold, redeemScriptLen, true) * vaultBtcReleaseFeeRateSatsPerVByte()
	for _, u := range confirmed {
		if u.Value < amountSats {
			continue
		}
		remainder := u.Value - amountSats
		if remainder == 0 || remainder < feeWithChange+vaultBtcDustThresholdSats {
			// No room for a change output (or exact match) - use the
			// no-change fee estimate for the final check/return.
			feeNoChange := vaultReleaseTxVsize(threshold, redeemScriptLen, false) * vaultBtcReleaseFeeRateSatsPerVByte()
			if u.Value >= amountSats+feeNoChange {
				return u, feeNoChange, true
			}
			continue
		}
		return u, feeWithChange, true
	}
	return BtcUtxo{}, 0, false
}

func submitBtcVaultRelease(approval *core.VaultReleaseApproval, validators map[string]*core.BridgeValidator, threshold int) {
	vaultAddress, err := btcVaultAddressString(validators)
	if err != nil {
		log.Println("BTC vault release relay: cannot derive vault address, skipping approval", approval.ID, ":", err)
		return
	}
	redeemScript, err := BuildBtcVaultRedeemScript(validators)
	if err != nil {
		log.Println("BTC vault release relay: cannot build redeem script, skipping approval", approval.ID, ":", err)
		return
	}
	sortedPubKeys, err := sortedBtcVaultPublicKeys(validators)
	if err != nil {
		log.Println("BTC vault release relay: cannot sort validator public keys, skipping approval", approval.ID, ":", err)
		return
	}

	amountSats, err := strconv.ParseUint(approval.AmountSmallestUnit, 10, 64)
	if err != nil {
		log.Println("BTC vault release relay: approval", approval.ID, "has an unparseable amount, skipping:", approval.AmountSmallestUnit)
		return
	}

	utxos, err := btcAddressUtxos(vaultAddress)
	if err != nil {
		log.Println("BTC vault release relay: failed to fetch vault UTXOs, skipping approval", approval.ID, ":", err)
		return
	}
	selected, feeSats, ok := selectVaultReleaseUtxo(utxos, amountSats, threshold, len(redeemScript))
	if !ok {
		log.Println("BTC vault release relay: no single vault UTXO covers amount+fee for approval", approval.ID, "- needs a larger deposit to consolidate against (multi-UTXO releases aren't supported)")
		return
	}

	changeSats := selected.Value - amountSats - feeSats
	hasChange := changeSats >= vaultBtcDustThresholdSats

	txidHash, err := chainhash.NewHashFromStr(selected.Txid)
	if err != nil {
		log.Println("BTC vault release relay: malformed UTXO txid, skipping approval", approval.ID, ":", err)
		return
	}
	outpoint := wire.OutPoint{Hash: *txidHash, Index: selected.Vout}

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))

	destScript, err := addressScriptPubKey(approval.DestinationExtAddress)
	if err != nil {
		log.Println("BTC vault release relay: invalid destination address for approval", approval.ID, ":", err)
		return
	}
	tx.AddTxOut(wire.NewTxOut(int64(amountSats), destScript))

	vaultScript, err := addressScriptPubKey(vaultAddress)
	if err != nil {
		log.Println("BTC vault release relay: failed to build the vault's own script for approval", approval.ID, ":", err)
		return
	}
	if hasChange {
		tx.AddTxOut(wire.NewTxOut(int64(changeSats), vaultScript))
	}

	derSigsByPubKeyHex := make(map[string][]byte, len(approval.Signatures))
	for creditChainAddr, sig := range approval.Signatures {
		v, ok := validators[creditChainAddr]
		if !ok || v.BtcPublicKeyHex == "" {
			continue
		}
		derSigsByPubKeyHex[v.BtcPublicKeyHex] = sig
	}

	witness, err := AssembleBtcVaultWitness(redeemScript, sortedPubKeys, derSigsByPubKeyHex, threshold, txscript.SigHashAll)
	if err != nil {
		log.Println("BTC vault release relay: failed to assemble witness for approval", approval.ID, ":", err)
		return
	}
	tx.TxIn[0].Witness = witness

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		log.Println("BTC vault release relay: failed to serialize release tx for approval", approval.ID, ":", err)
		return
	}
	rawTxHex := hex.EncodeToString(buf.Bytes())

	txid, err := btcBroadcastRawTx(rawTxHex)
	if err != nil {
		log.Println("BTC vault release relay: broadcast failed for approval", approval.ID, ":", err)
		return
	}

	log.Println("BTC vault release relay: broadcast release for approval", approval.ID, "txid", txid)
	// BTC's fee (if any) was already credited immediately at burn time
	// (creditVaultReleaseFee) - no relayer fee recipient to name here,
	// see vaultReleaseCompleteData.FeeRecipientEthAddress's doc comment.
	recordVaultReleaseComplete(approval.ID, txid, "")
}

// addressScriptPubKey decodes any bech32 segwit address (P2WPKH
// destination or the vault's own P2WSH address) into its real
// scriptPubKey via btcutil/txscript - reused for both the destination
// output and the vault's own change/prevout script rather than hand-
// building either.
func addressScriptPubKey(address string) ([]byte, error) {
	decoded, err := btcutil.DecodeAddress(address, btcVaultNetParams())
	if err != nil {
		return nil, fmt.Errorf("decode address %q: %w", address, err)
	}
	script, err := txscript.PayToAddrScript(decoded)
	if err != nil {
		return nil, fmt.Errorf("build scriptPubKey for %q: %w", address, err)
	}
	return script, nil
}
