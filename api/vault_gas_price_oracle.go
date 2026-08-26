package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// This file lets VaultReleaseGasPriceWei (api/vault_fee.go) refresh
// itself periodically from a real eth_gasPrice read, WITHOUT reopening
// the single-key bypass the admin multisig exists to close - see
// core.TxVaultGasPriceOracleUpdate's doc comment for the full
// reasoning. Built at the user's explicit request after the mainnet
// default (20 Gwei, never updated since launch) was found to be
// overcharging ETH/USDC vault release fees by roughly 238x versus real
// conditions - see audit/KNOWN_GAPS.md.

// vaultGasPriceOracleUpdatePollInterval is how often the loop checks
// the real gas price and (if the bounded result actually differs from
// the current on-chain value) submits an update - deliberately not
// fast: this value only needs to track real gas trends, not every
// block-to-block fluctuation, and every submission is a real
// transaction every node must apply.
const vaultGasPriceOracleUpdatePollInterval = 30 * time.Minute

// VaultGasPriceOracleMinIntervalSeconds is the actual security
// boundary against a leaked oracle key (or a malfunctioning oracle
// job) submitting updates faster than intended - enforced at APPLY
// time (applyVaultGasPriceOracleUpdate), not just by this loop's own
// ticker, since /tx/submit accepts an already-signed transaction from
// anywhere, not only from this process. A leaked key can only ever
// move the value once per this window, each move bounded by
// VaultGasPriceOracleMaxStepBasisPoints.
const VaultGasPriceOracleMinIntervalSeconds int64 = 15 * 60

// VaultGasPriceOracleMinWei/MaxWei are the absolute floor/ceiling no
// bounded update may ever cross, regardless of what a live gas read or
// a compromised key claims - 0.05 Gwei floor (real gas is never
// meaningfully below this; a floor of 0 would let a bad read make
// releases effectively fee-free) and 200 Gwei ceiling (well above any
// realistic sustained mainnet gas price; a real spike beyond this
// should prompt a human to look, not an unbounded automatic fee).
const (
	VaultGasPriceOracleMinWei uint64 = 50_000_000
	VaultGasPriceOracleMaxWei uint64 = 200_000_000_000
)

// VaultGasPriceOracleMaxStepBasisPoints bounds how far a single update
// may move the value relative to whatever it's currently set to (5000
// = 50%) - the other half of the "bounded, not unsupervised" design:
// even an attacker with the oracle key, submitting as fast as
// VaultGasPriceOracleMinIntervalSeconds allows, can only ever
// compound the value by 50% every 15 minutes, not jump it arbitrarily
// in one transaction.
const VaultGasPriceOracleMaxStepBasisPoints uint64 = 5000

// vaultGasPriceOracleAddress reads VAULT_GAS_PRICE_ORACLE_ADDRESS - the
// ONE CreditChain wallet address this mechanism ever accepts an update
// from, deliberately a single address (not a threshold set like
// BRIDGE_VALIDATOR_ADDRESSES) since the bounded-step/bounded-rate
// design already caps the damage a single compromised key can do,
// the same reasoning RELAYER_PRIVATE_KEY_HEX's single-key gas-paying
// role already accepts.
func vaultGasPriceOracleAddress() string {
	return normalizeAdminAddress(os.Getenv("VAULT_GAS_PRICE_ORACLE_ADDRESS"))
}

// The raw-hex-key loading this function used to do directly now lives
// behind resolveVaultGasPriceOracleSigner (vault_gas_price_oracle_signer.go),
// which also supports a real AWS KMS-backed key.

type vaultGasPriceOracleUpdateData struct {
	NewValueWei uint64 `json:"newValueWei"`
}

// clampVaultGasPriceOracleUpdate is the pure bounding function both the
// submitting loop (as a courtesy - no point submitting a transaction
// the apply side will just re-clamp) and applyVaultGasPriceOracleUpdate
// (the real security boundary) call. Applies the step limit relative
// to current FIRST, then the absolute floor/ceiling - so the absolute
// bounds always win even if a step-limited value would otherwise still
// be out of the safe absolute range (e.g. current already sitting at
// the ceiling).
func clampVaultGasPriceOracleUpdate(current, proposed uint64) uint64 {
	maxStep := current * VaultGasPriceOracleMaxStepBasisPoints / 10_000
	// Guarantee at least some step room even when current is small
	// enough that the percentage rounds to 0 (e.g. current at the
	// absolute floor) - otherwise the value could never move at all.
	if maxStep == 0 {
		maxStep = VaultGasPriceOracleMinWei
	}
	lowerStep := uint64(0)
	if current > maxStep {
		lowerStep = current - maxStep
	}
	upperStep := current + maxStep

	bounded := proposed
	if bounded < lowerStep {
		bounded = lowerStep
	}
	if bounded > upperStep {
		bounded = upperStep
	}
	if bounded < VaultGasPriceOracleMinWei {
		bounded = VaultGasPriceOracleMinWei
	}
	if bounded > VaultGasPriceOracleMaxWei {
		bounded = VaultGasPriceOracleMaxWei
	}
	return bounded
}

// submitVaultGasPriceOracleUpdate builds and records a genuinely
// wallet-signed TxVaultGasPriceOracleUpdate directly (no HTTP
// round-trip - this runs inside the server process already), the same
// self-signing pattern submitVaultLockAttest uses. Signing goes
// through resolveVaultGasPriceOracleSigner (vault_gas_price_oracle_signer.go)
// so this works identically whether the operator configured a real
// AWS KMS key or the original raw-hex env var - the signature and
// wire format are byte-identical either way.
func submitVaultGasPriceOracleUpdate(newValueWei uint64) {
	signer, ok := resolveVaultGasPriceOracleSigner()
	if !ok {
		return
	}
	ctx := context.Background()
	address, publicKeyHex, err := signer.identity(ctx)
	if err != nil {
		log.Println("gas price oracle: failed to resolve signer identity:", err)
		return
	}

	data := vaultGasPriceOracleUpdateData{NewValueWei: newValueWei}
	payloadJSON, err := json.Marshal(data)
	if err != nil {
		return
	}

	env := core.SignedEnvelope{
		Address:   address,
		PublicKey: publicKeyHex,
		Timestamp: time.Now().Unix(),
		Payload:   payloadJSON,
	}
	sig, err := signer.sign(ctx, env.CanonicalMessage())
	if err != nil {
		log.Println("gas price oracle: failed to sign update:", err)
		return
	}
	env.Signature = hex.EncodeToString(sig)

	recordTx(env, address, "", 0, core.TxVaultGasPriceOracleUpdate, string(payloadJSON))
}

// startVaultGasPriceOracleLoop no-ops entirely unless the RPC, an
// oracle signing key, AND the matching authorized address are all
// configured - graceful-degrade exactly like startVaultReleaseRelayLoop.
// Every tick, reads the real current gas price, computes what
// VaultReleaseGasPriceWei would become under the bounded step/floor/
// ceiling rule, and only submits an update transaction if that bounded
// result actually differs from the current on-chain value - an
// unchanged value never generates a no-op transaction every 30 minutes
// forever.
func startVaultGasPriceOracleLoop() {
	if ethRPCClient == nil {
		return
	}
	signer, ok := resolveVaultGasPriceOracleSigner()
	if !ok {
		return
	}
	address, _, err := signer.identity(context.Background())
	if err != nil {
		log.Println("gas price oracle: failed to resolve signer identity, the loop will not run:", err)
		return
	}
	if vaultGasPriceOracleAddress() == "" {
		log.Println("A gas price oracle signing key is configured but VAULT_GAS_PRICE_ORACLE_ADDRESS is not - the gas price oracle loop will not run (an update signed by", address, "would never be accepted without its address also being configured as the authorized one)")
		return
	}

	log.Println("vault gas price oracle loop started - signing as", address, "- bounded to +/-", VaultGasPriceOracleMaxStepBasisPoints/100, "% per update, at most once every", VaultGasPriceOracleMinIntervalSeconds/60, "minutes, within [", VaultGasPriceOracleMinWei, ",", VaultGasPriceOracleMaxWei, "] wei")

	go func() {
		ticker := time.NewTicker(vaultGasPriceOracleUpdatePollInterval)
		defer ticker.Stop()
		for range ticker.C {
			scanAndUpdateVaultGasPriceOracleOnce()
		}
	}()
}

func scanAndUpdateVaultGasPriceOracleOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	liveGasPrice, err := ethRPCClient.SuggestGasPrice(ctx)
	cancel()
	if err != nil {
		log.Println("vault gas price oracle: failed to read live gas price:", err)
		return
	}
	if !liveGasPrice.IsUint64() {
		return
	}

	current := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei)
	bounded := clampVaultGasPriceOracleUpdate(current, liveGasPrice.Uint64())
	if bounded == current {
		return
	}
	submitVaultGasPriceOracleUpdate(bounded)
}

// applyVaultGasPriceOracleUpdate is the real security boundary for
// this whole mechanism - never trusts what the transaction claims
// about who's allowed to submit it, how much it may move the value, or
// how recently the last update happened. Three independent checks, all
// enforced here regardless of how the transaction reached this node
// (this process's own loop, a directly-crafted POST to /tx/submit, or
// P2P gossip from a peer):
//
//  1. tx.AuthAddress must equal the ONE configured
//     VAULT_GAS_PRICE_ORACLE_ADDRESS - not "any signature verifies",
//     which verifyTxAuth's TxVaultGasPriceOracleUpdate case alone
//     would otherwise accept from literally anyone.
//  2. At least VaultGasPriceOracleMinIntervalSeconds must have passed
//     since governanceParamsStore's own recorded SetAt timestamp for
//     this key - reusing the exact staleness-timestamp mechanism
//     EthUsdCentsPerEth's own staleness gate already established (see
//     core.GovernanceParamsStore.SetAt/LastSetAt).
//  3. The claimed new value is re-clamped through
//     clampVaultGasPriceOracleUpdate against the CURRENT on-chain
//     value at apply time (never the value the submitter last saw),
//     so a stale or malicious claim can only ever land wherever the
//     bound actually allows, never further.
func applyVaultGasPriceOracleUpdate(tx core.Transaction) {
	configured := vaultGasPriceOracleAddress()
	if configured == "" || normalizeAdminAddress(tx.AuthAddress) != configured {
		return
	}

	var data vaultGasPriceOracleUpdateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	if lastAt, ok := governanceParamsStore.LastSetAt("VaultReleaseGasPriceWei"); ok {
		if tx.Timestamp-lastAt < VaultGasPriceOracleMinIntervalSeconds {
			return
		}
	}

	current := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei)
	bounded := clampVaultGasPriceOracleUpdate(current, data.NewValueWei)
	governanceParamsStore.SetAt("VaultReleaseGasPriceWei", bounded, tx.Timestamp)
}
