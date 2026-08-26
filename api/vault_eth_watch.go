package api

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/creditchain/creditchaind/core"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// vaultEthWatchPollInterval mirrors ethBridgeDepositPollInterval's 30s
// cadence (api/eth_bridge.go) - RPC calls are relatively expensive/
// rate-limited, and real vault locks aren't latency-sensitive.
const vaultEthWatchPollInterval = 30 * time.Second

// vaultEthLastScannedBlock is node-local watch-loop progress, not
// consensus state - every bridge validator's own node independently
// tracks how far it has scanned (see core/vault_lock_attestation.go's
// doc comment: this is the point of the redesign, each validator
// verifies for themselves rather than trusting one node's report).
// Persisted for continuity across restarts (api/persistence.go), same
// "node-local but still worth persisting" treatment
// EthWatchedAddresses already gets - losing this on restart isn't
// unsafe (attestation IDs are idempotent, re-scanning overlapping
// blocks just re-submits harmless duplicate attestations), just
// wasteful.
var vaultEthLastScannedBlock uint64

// vaultContractAddress reads VAULT_CONTRACT_ADDRESS - unset means the
// vault watch loop and release relay both stay disabled, same
// "degrade gracefully, don't panic" posture InitEthRPC already has for
// a missing RPC URL.
func vaultContractAddress() (common.Address, bool) {
	raw := os.Getenv("VAULT_CONTRACT_ADDRESS")
	if raw == "" || !common.IsHexAddress(raw) {
		return common.Address{}, false
	}
	return common.HexToAddress(raw), true
}

// bridgeValidatorSigningKey loads this node operator's own bridge-
// validator wallet key from BRIDGE_VALIDATOR_PRIVATE_KEY_HEX (hex-
// encoded, same 64-byte Ed25519 format core.GenerateKeyPair produces) -
// deliberately a real WALLET key, not core/node_identity.go's
// NodeIdentity (which only ever signs blocks) - see
// core/vault_lock_attestation.go's doc comment for why
// TxVaultLockAttest is wallet-signed, address-attributable per
// validator, not the self-signed-node-identity pattern TxBridgeDeposit
// uses. A node with no key configured here simply never submits
// attestations - it can still run as an ordinary validator/relay node
// without also being a bridge validator.
func bridgeValidatorSigningKey() (address string, priv ed25519.PrivateKey, ok bool) {
	raw := os.Getenv("BRIDGE_VALIDATOR_PRIVATE_KEY_HEX")
	if raw == "" {
		return "", nil, false
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		log.Println("BRIDGE_VALIDATOR_PRIVATE_KEY_HEX is set but not a valid 64-byte hex-encoded Ed25519 private key - this node will not submit vault attestations")
		return "", nil, false
	}
	priv = ed25519.PrivateKey(decoded)
	pub := priv.Public().(ed25519.PublicKey)
	return core.GenerateAddress(pub), priv, true
}

// submitVaultLockAttest builds and records a genuinely wallet-signed
// TxVaultLockAttest directly (no HTTP round-trip needed - this code
// already runs inside the server process), exactly the same envelope
// shape a browser client's apiPostSigned would produce. Silently no-ops
// if this node has no bridge-validator key configured.
func submitVaultLockAttest(data vaultLockAttestData) {
	address, priv, ok := bridgeValidatorSigningKey()
	if !ok {
		return
	}
	payloadJSON, err := json.Marshal(data)
	if err != nil {
		return
	}

	env := core.SignedEnvelope{
		Address:   address,
		PublicKey: hex.EncodeToString(priv.Public().(ed25519.PublicKey)),
		Timestamp: time.Now().Unix(),
		Payload:   payloadJSON,
	}
	env.Signature = hex.EncodeToString(core.SignMessage(priv, env.CanonicalMessage()))

	recordTx(env, address, "", 0, core.TxVaultLockAttest, string(payloadJSON))
}

// startVaultEthWatchLoop replaces the old balance-polling model
// (startEthBridgeDepositLoop) with real event-watching for ETH/USDC
// specifically - see core/vault_lock_attestation.go's doc comment for
// why this is the actual fix for the mirror model's race condition.
// Reuses the already-initialized ethRPCClient (api/eth_rpc.go) - no new
// RPC connection. No-ops entirely if the RPC, the vault contract
// address, or this node's own bridge-validator key aren't configured.
func startVaultEthWatchLoop() {
	if ethRPCClient == nil {
		return
	}
	contractAddr, ok := vaultContractAddress()
	if !ok {
		return
	}
	if _, _, ok := bridgeValidatorSigningKey(); !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	head, err := ethRPCClient.BlockNumber(ctx)
	cancel()
	if err != nil {
		log.Println("vault ETH watch loop disabled: failed to read current block number:", err)
		return
	}
	if vaultEthLastScannedBlock == 0 || vaultEthLastScannedBlock > head {
		// First run (or a persisted value from a different/reset chain) -
		// start from the current head rather than genesis, same
		// pragmatic default this codebase's other watch loops use when
		// there's no prior progress to resume from. A real production
		// deployment should set this explicitly to the contract's
		// deployment block on first launch instead.
		vaultEthLastScannedBlock = head
	}

	log.Printf("vault ETH watch loop started: contract %s, chain %s", contractAddr.Hex(), expectedEthChainID)

	go func() {
		ticker := time.NewTicker(vaultEthWatchPollInterval)
		defer ticker.Stop()
		for range ticker.C {
			scanVaultEthLogsOnce(contractAddr)
		}
	}()
}

// scanVaultEthLogsOnce scans every confirmation-safe new block since
// the last scan for Locked events, and submits one TxVaultLockAttest
// per event found. Only scans up to head-requiredConfirmations, so any
// event found here is ALREADY confirmation-safe by construction (see
// core/vault_lock_attestation.go's confirmation-depth reasoning) -
// simpler than finding an event immediately and separately tracking
// whether it's "confirmed enough" yet.
func scanVaultEthLogsOnce(contractAddr common.Address) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	head, err := ethRPCClient.BlockNumber(ctx)
	if err != nil {
		log.Println("vault ETH watch loop: failed to read current block number:", err)
		return
	}

	confirmations := vaultEthConfirmations()
	if head < confirmations {
		return
	}
	safeHead := head - confirmations
	if safeHead <= vaultEthLastScannedBlock {
		return
	}

	fromBlock := vaultEthLastScannedBlock + 1
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(safeHead),
		Addresses: []common.Address{contractAddr},
		Topics:    [][]common.Hash{{lockedEventSignature}},
	}

	logs, err := ethRPCClient.FilterLogs(ctx, query)
	if err != nil {
		log.Println("vault ETH watch loop: FilterLogs failed:", err)
		return
	}

	usdcAddr, usdcErr := usdcContractAddress()
	lidoAddr, lidoErr := lidoStethContractAddress()

	for _, l := range logs {
		decoded, err := decodeLockedEvent(l)
		if err != nil {
			log.Println("vault ETH watch loop: failed to decode a Locked log, skipping:", err)
			continue
		}

		var chain string
		switch {
		case decoded.Token == (common.Address{}):
			chain = "ETH"
		case usdcErr == nil && decoded.Token == common.HexToAddress(usdcAddr):
			chain = "USDC"
		case lidoErr == nil && decoded.Token == common.HexToAddress(lidoAddr):
			chain = "STETH"
		default:
			// The contract's own allowlist should make this
			// unreachable, but never guess - an unrecognized token
			// address is skipped, not silently attested as something
			// it isn't.
			log.Printf("vault ETH watch loop: Locked event for unrecognized token %s, skipping", decoded.Token.Hex())
			continue
		}

		submitVaultLockAttest(vaultLockAttestData{
			Chain:              chain,
			ExternalTxHash:     l.TxHash.Hex(),
			ExternalIndex:      uint32(l.Index),
			DepositorCcAddress: hex.EncodeToString(decoded.CreditChainAddress[:]),
			DepositorPublicKey: hex.EncodeToString(decoded.CreditChainPublicKey[:]),
			AmountSmallestUnit: decoded.Amount.String(),
		})
	}

	vaultEthLastScannedBlock = safeHead
}
