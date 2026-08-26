package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/creditchain/creditchaind/core"
)

// This file - not core/btc_vault.go as task C.1 was originally titled -
// is where btcsuite-dependent vault code lives, mirroring the existing
// convention that external-chain-specific code (api/eth_rpc.go,
// api/eth_signer.go, api/btc_rpc.go) stays out of core/ so core/ never
// needs a third-party blockchain SDK dependency. core/ only holds the
// chain-agnostic BridgeValidator/VaultLockAttestation/
// VaultReleaseApproval record shapes.

// btcVaultNetParams mirrors this project's existing testnet-by-default
// posture (core/btc_address.go's BtcAddress doc comment, api/btc_rpc.go's
// BTC_API_BASE_URL default) - mainnetMode() (api/mainnet_gate.go) is the
// same single switch every other BTC-facing code path in this project
// already gates on.
func btcVaultNetParams() *chaincfg.Params {
	if mainnetMode() {
		return &chaincfg.MainNetParams
	}
	return &chaincfg.TestNet3Params
}

// btcVaultMultisigThreshold mirrors bridgeValidatorThreshold()
// (api/bridge_validator.go) - the BTC vault's redeem script uses the
// SAME M-of-N as ETH/USDC's on-chain validator threshold, since both are
// just different chain-specific expressions of one bridge-validator
// signer set's authority.
func btcVaultMultisigThreshold() int {
	return bridgeValidatorThreshold()
}

// sortedBtcVaultPublicKeys returns every registered bridge validator's
// compressed secp256k1 BTC public key, deterministically ordered by the
// raw pubkey bytes. Determinism matters here in a way it doesn't for the
// ETH contract's validator array (which is explicit on-chain state) -
// every node independently reconstructs this same redeem script from
// the same BridgeValidatorStore snapshot, and any two nodes that order
// the keys differently would derive a DIFFERENT P2WSH address for what
// is supposed to be the one shared vault.
func sortedBtcVaultPublicKeys(validators map[string]*core.BridgeValidator) ([][]byte, error) {
	type entry struct {
		raw []byte
	}
	entries := make([]entry, 0, len(validators))
	for _, v := range validators {
		if v.BtcPublicKeyHex == "" {
			continue
		}
		raw, err := hex.DecodeString(v.BtcPublicKeyHex)
		if err != nil {
			return nil, fmt.Errorf("sortedBtcVaultPublicKeys: validator %s has malformed BtcPublicKeyHex: %w", v.CreditChainAddress, err)
		}
		if len(raw) != 33 {
			return nil, fmt.Errorf("sortedBtcVaultPublicKeys: validator %s BTC public key must be 33 bytes (compressed), got %d", v.CreditChainAddress, len(raw))
		}
		entries = append(entries, entry{raw: raw})
	}
	sort.Slice(entries, func(i, j int) bool {
		return hex.EncodeToString(entries[i].raw) < hex.EncodeToString(entries[j].raw)
	})
	out := make([][]byte, len(entries))
	for i, e := range entries {
		out[i] = e.raw
	}
	return out, nil
}

// btcVaultAddressPubKeys converts raw compressed pubkeys into the
// btcutil.AddressPubKey wrapper txscript.MultiSigScript requires.
func btcVaultAddressPubKeys(rawPubKeys [][]byte, params *chaincfg.Params) ([]*btcutil.AddressPubKey, error) {
	out := make([]*btcutil.AddressPubKey, len(rawPubKeys))
	for i, raw := range rawPubKeys {
		addrPubKey, err := btcutil.NewAddressPubKey(raw, params)
		if err != nil {
			return nil, fmt.Errorf("btcVaultAddressPubKeys: invalid public key at index %d: %w", i, err)
		}
		out[i] = addrPubKey
	}
	return out, nil
}

// BuildBtcVaultRedeemScript constructs the OP_M <pubkeys...> OP_N
// OP_CHECKMULTISIG redeem script for the current registered
// bridge-validator set. Exported (capitalized) since C.4's witness
// assembly and C.5's deposit watcher both need this exact script, not
// just the derived address.
func BuildBtcVaultRedeemScript(validators map[string]*core.BridgeValidator) ([]byte, error) {
	threshold := btcVaultMultisigThreshold()
	if threshold < 1 {
		return nil, fmt.Errorf("BuildBtcVaultRedeemScript: bridge validator threshold must be at least 1, got %d", threshold)
	}

	rawPubKeys, err := sortedBtcVaultPublicKeys(validators)
	if err != nil {
		return nil, err
	}
	if len(rawPubKeys) < threshold {
		return nil, fmt.Errorf("BuildBtcVaultRedeemScript: only %d validators have a registered BTC public key, need at least threshold %d", len(rawPubKeys), threshold)
	}

	params := btcVaultNetParams()
	addrPubKeys, err := btcVaultAddressPubKeys(rawPubKeys, params)
	if err != nil {
		return nil, err
	}

	redeemScript, err := txscript.MultiSigScript(addrPubKeys, threshold)
	if err != nil {
		return nil, fmt.Errorf("BuildBtcVaultRedeemScript: MultiSigScript: %w", err)
	}
	return redeemScript, nil
}

// BtcVaultWitnessProgram is plain SHA256(redeemScript) - 32 bytes, NOT
// the RIPEMD160(SHA256(...)) hash160 convention P2WPKH/legacy-P2SH use.
// Confirmed directly against btcsuite/btcd's own
// NewAddressWitnessScriptHash implementation, which hard-rejects any
// witness program that isn't exactly 32 bytes for a P2WSH address.
func BtcVaultWitnessProgram(redeemScript []byte) [32]byte {
	return sha256.Sum256(redeemScript)
}

// BuildBtcVaultAddress derives the shared bech32 P2WSH vault address
// every bridge validator's own node independently watches (api/vault_btc_watch.go,
// C.5) - the BTC-side equivalent of Phase B's single deployed
// CreditChainVault.sol contract address.
func BuildBtcVaultAddress(validators map[string]*core.BridgeValidator) (btcutil.Address, []byte, error) {
	redeemScript, err := BuildBtcVaultRedeemScript(validators)
	if err != nil {
		return nil, nil, err
	}
	witnessProgram := BtcVaultWitnessProgram(redeemScript)

	params := btcVaultNetParams()
	addr, err := btcutil.NewAddressWitnessScriptHash(witnessProgram[:], params)
	if err != nil {
		return nil, nil, fmt.Errorf("BuildBtcVaultAddress: NewAddressWitnessScriptHash: %w", err)
	}
	return addr, redeemScript, nil
}

// btcVaultAddressString is the convenience form api/vault_btc_watch.go
// (C.5) and any status/lookup HTTP handler actually want - the bech32
// string, not the btcutil.Address interface value.
func btcVaultAddressString(validators map[string]*core.BridgeValidator) (string, error) {
	addr, _, err := BuildBtcVaultAddress(validators)
	if err != nil {
		return "", err
	}
	return addr.EncodeAddress(), nil
}
