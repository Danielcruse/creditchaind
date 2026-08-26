package api

import (
	"context"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/creditchain/creditchaind/core"
	"github.com/ethereum/go-ethereum/common"
)

// This file is the node-local relay that actually performs the real,
// irreversible ETH/USDC release once a core.VaultReleaseApproval reaches
// signature threshold - the deliberately-deferred piece
// applyVaultReleaseApprove's doc comment names explicitly. See
// contracts/CreditChainVault.sol's release() - on-chain ecrecover
// verification means the relayer's own identity carries NO authority
// over whether a release is valid; a compromised or malicious relayer
// key can at worst waste its own gas or race another relayer to submit
// first (harmless - the contract's usedReleaseIds makes a second
// submission of the same releaseId a no-op revert), never move funds it
// hasn't already been cryptographically authorized to move. This is
// exactly why relaying can be - and here is - permissionless: any
// account with ETH for gas can submit a valid signature bundle.

// vaultReleaseRelayPollInterval mirrors vaultEthWatchPollInterval's own
// reasoning - not latency-sensitive, keep RPC call volume low.
const vaultReleaseRelayPollInterval = 30 * time.Second

// The raw-hex-key loading this function used to do directly now lives
// behind resolveRelayerSigner (vault_relayer_signer.go), which also
// supports a real AWS KMS-backed key - a single, fixed, operator-
// funded ETH account used ONLY to pay gas for release() calls,
// deliberately NOT run through the per-user-custodial ethSigner global
// (that machinery exists to provision many custodial keys, not
// resolve one fixed operator key).

// relayerFeeRecipientEthAddress reads RELAYER_FEE_RECIPIENT_ETH_ADDRESS -
// where this node's share of the gas-cost-based ETH/USDC release fee
// (api/vault_fee.go's vaultReleaseEstimatedFeeWei) lands whenever ITS
// OWN relayerPrivateKey successfully broadcasts a release. Deliberately
// separate from relayerPrivateKey itself: the relayer's Ethereum
// address has no ETH_CCC1/USDC_CCC1 balance concept of its own (it's
// purely a gas-paying account, see relayerPrivateKey's doc comment), so
// this names a DIFFERENT real self-custodial ETH address to actually
// receive the earned fee. An operator who never sets this simply
// doesn't collect the fee (creditVaultReleaseFeeToRelayer no-ops on an
// empty recipient) - relaying itself is unaffected either way, same
// "off unless explicitly configured" posture every other piece of this
// bridge already has.
func relayerFeeRecipientEthAddress() string {
	return strings.TrimSpace(os.Getenv("RELAYER_FEE_RECIPIENT_ETH_ADDRESS"))
}

// startVaultReleaseRelayLoop degrades gracefully exactly like
// startVaultEthWatchLoop - no-ops if the RPC, the vault contract
// address, or a relayer key aren't configured, rather than crashing or
// spinning uselessly.
func startVaultReleaseRelayLoop() {
	if ethRPCClient == nil {
		return
	}
	if _, ok := vaultContractAddress(); !ok {
		return
	}
	signer, ok := resolveRelayerSigner()
	if !ok {
		return
	}
	relayerAddr, err := signer.address(context.Background())
	if err != nil {
		log.Println("vault release relay: failed to resolve relayer signer address, the loop will not run:", err)
		return
	}
	log.Println("vault ETH/USDC release relay: this node will pay gas from", relayerAddr, "- fund it with real ETH or every release() call will fail for insufficient balance")

	go func() {
		ticker := time.NewTicker(vaultReleaseRelayPollInterval)
		defer ticker.Stop()
		for range ticker.C {
			scanVaultReleaseApprovalsOnce()
		}
	}()
	log.Println("vault ETH/USDC release relay loop started")
}

// scanVaultReleaseApprovalsOnce submits a real release() call for every
// ETH/USDC release approval that has reached signature threshold and
// hasn't been released yet. Deliberately re-derives the threshold live
// (same reasoning as maybeExecuteAdminMultisigProposal) rather than
// trusting a value captured earlier.
func scanVaultReleaseApprovalsOnce() {
	contractAddr, ok := vaultContractAddress()
	if !ok {
		return
	}
	signer, ok := resolveRelayerSigner()
	if !ok {
		return
	}
	relayerAddr, err := signer.address(context.Background())
	if err != nil {
		log.Println("vault release relay: failed to resolve relayer signer address:", err)
		return
	}
	threshold := bridgeValidatorThreshold()
	if threshold < 1 {
		return
	}

	for _, approval := range vaultReleaseApprovalStore.Snapshot() {
		if approval.Chain != "ETH" && approval.Chain != "USDC" {
			continue
		}
		if approval.Status != core.VaultReleasePending {
			continue
		}
		if len(approval.Signatures) < threshold {
			continue
		}
		submitVaultRelease(approval, contractAddr, relayerAddr, signer)
	}
}

// submitVaultRelease assembles and broadcasts one real release() call.
// Errors are logged, not retried immediately - the next poll tick will
// naturally retry (the approval stays Pending until
// TxVaultReleaseComplete confirms it), and a duplicate submission is
// harmless (contracts/CreditChainVault.sol's usedReleaseIds rejects the
// second one as a no-op revert).
func submitVaultRelease(approval *core.VaultReleaseApproval, contractAddr common.Address, relayerAddr string, signer relayerSigner) {
	if !common.IsHexAddress(approval.DestinationExtAddress) {
		log.Println("vault release relay: approval", approval.ID, "has an invalid destination address, skipping:", approval.DestinationExtAddress)
		return
	}
	to := common.HexToAddress(approval.DestinationExtAddress)

	var token common.Address
	if approval.Chain == "USDC" {
		usdcAddr, err := usdcContractAddress()
		if err != nil {
			log.Println("vault release relay: USDC contract address not configured, skipping approval", approval.ID)
			return
		}
		token = common.HexToAddress(usdcAddr)
	}

	amount, ok := new(big.Int).SetString(approval.AmountSmallestUnit, 10)
	if !ok {
		log.Println("vault release relay: approval", approval.ID, "has an unparseable amount, skipping:", approval.AmountSmallestUnit)
		return
	}

	signatures := make([][]byte, 0, len(approval.Signatures))
	for _, sig := range approval.Signatures {
		signatures = append(signatures, sig)
	}

	calldata, err := packReleaseCalldata(to, token, amount, approval.ReleaseID, signatures)
	if err != nil {
		log.Println("vault release relay: failed to pack release calldata for approval", approval.ID, ":", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	txHash, err := buildAndBroadcastContractCallWithRelayerSigner(ctx, signer, relayerAddr, contractAddr.Hex(), calldata, big.NewInt(0))
	if err != nil {
		log.Println("vault release relay: release() call failed for approval", approval.ID, ":", err)
		return
	}

	log.Println("vault release relay: submitted release() for approval", approval.ID, "tx", txHash)
	recordVaultReleaseComplete(approval.ID, txHash, relayerFeeRecipientEthAddress())
}
