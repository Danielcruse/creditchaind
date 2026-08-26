package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// adminSettableParams is the exhaustive allowlist of GovernanceParamsStore
// keys TxAdminSetParam is allowed to touch - deliberately explicit rather
// than "any string key", so this can never become an arbitrary-key setter.
// Extending this list later is a deliberate, separate decision, not
// implied by this mechanism's existence (see core/transactions.go's doc
// comment on TxAdminSetParam and core/governance_params.go's own doc
// comment on why this codebase deliberately widens GovernanceParamsStore
// coverage narrowly, one named constant at a time).
var adminSettableParams = map[string]bool{
	"UnbondingPeriodSeconds": true,
	"DormancyPeriodSeconds":  true,
	// EthUsdCentsPerEth/BtcUsdCentsPerBtc/AccountHoldingsCapUsdCents
	// back the per-account total BTCB+ETHB holdings cap (see
	// api/account_holdings_cap.go) - replaces the earlier flat
	// per-transaction BridgeDepositCapWeiETH/BridgeDepositCapSatsBTC
	// keys, which are no longer read anywhere and have been removed
	// from this allowlist.
	"EthUsdCentsPerEth":          true,
	"BtcUsdCentsPerBtc":          true,
	"AccountHoldingsCapUsdCents": true,
	// StethEthRatioBasisPoints - the assumed stETH:ETH exchange ratio
	// (10000 = 1:1, see api/apply.go's DefaultStethEthRatioBasisPoints)
	// collateralValueInYen applies as a haircut on STETH_CCC1 collateral
	// - admin-tunable downward if stETH depegs from ETH in real markets.
	"StethEthRatioBasisPoints": true,
	// ModeratorRegisterFeeCCT/ModeratorStakeRequirementCCT - see
	// api/moderators.go's moderatorRegisterFeeCCT()/
	// moderatorStakeRequirementCCT() for the read side and
	// api/executive_proposal.go's isAllowedExecutiveProposalParamKey
	// for the second (executive-vote) writer of these same two keys.
	"ModeratorRegisterFeeCCT":      true,
	"ModeratorStakeRequirementCCT": true,
	// VaultEthConfirmations/VaultBtcConfirmations - how many blocks deep
	// a real vault lock event must be before a bridge validator may
	// attest to it (see core/vault_lock_attestation.go's confirmation-
	// depth reasoning: a reorg shallower than this could "unlock" an
	// already-minted CCC1 deposit). Read via
	// vaultEthConfirmations()/vaultBtcConfirmations() below.
	"VaultEthConfirmations": true,
	"VaultBtcConfirmations": true,
	// VaultBtcReleaseFeeRateSatsPerVByte is deliberately a FIXED,
	// consensus-agreed value rather than a live fee-market lookup (unlike
	// btcFeeEstimateSatsPerVByte, which the deposit/send flows use) - see
	// api/vault_btc_release_relay.go's doc comment for why: every bridge
	// validator's own external signing infrastructure must independently
	// reconstruct the EXACT same unsigned release transaction (and thus
	// the exact same BIP143 sighash) the relay will build, with no
	// coordination round-trip beforehand. A live, node-local API lookup
	// would let different validators sign against different fee
	// assumptions at different moments, producing mismatched sighashes.
	"VaultBtcReleaseFeeRateSatsPerVByte": true,
	// VaultReleaseGasPriceWei - see api/vault_fee.go's
	// vaultReleaseGasPriceWei/vaultReleaseEstimatedFeeWei for the
	// gas-cost-based ETH/USDC release fee this feeds, and why it must
	// stay a fixed, consensus-agreed value rather than a live
	// eth_gasPrice lookup (same reasoning as
	// VaultBtcReleaseFeeRateSatsPerVByte just above).
	"VaultReleaseGasPriceWei": true,
	// EthCcc1TransferFeeUsdCents/UsdcCcc1TransferFeeUsdCents/
	// BtcCcc1TransferFeeUsdCents - the admin-set USD-cent approximation
	// of real-chain gas cost each CCC1 transfer fee is computed from
	// (see api/ccc1_transfer_fee.go's ccc1TransferFeeSmallest). Same
	// "admin-tunable approximation, not a live oracle" precedent as
	// EthUsdCentsPerEth/BtcUsdCentsPerBtc just above.
	"EthCcc1TransferFeeUsdCents":  true,
	"UsdcCcc1TransferFeeUsdCents": true,
	"BtcCcc1TransferFeeUsdCents":  true,
	// TSIWeight*Bps - the six basis-point weights ComputeTSI blends
	// Wallet.Trust's dimensions with (see core/trust.go's
	// DefaultTrustWeights). Admin-tunable one dimension at a time,
	// matching the only kind of write this whole mechanism supports -
	// see syncTrustWeightIfNeeded (api/governance_params.go) for the
	// other half of what actually applies a change.
	"TSIWeightRepaymentBps":    true,
	"TSIWeightTradingBps":      true,
	"TSIWeightGovernanceBps":   true,
	"TSIWeightContributionBps": true,
	"TSIWeightVerificationBps": true,
	"TSIWeightDisputeBps":      true,
	// CctUsdCents - CCT's own admin-set USD-cents-per-whole-CCT rate,
	// same "admin-tunable approximation, not a live oracle" precedent as
	// EthUsdCentsPerEth/BtcUsdCentsPerBtc above. Lets a non-CCT platform-
	// fee burn (ETH_CCC1/BTC_CCC1/USDC_CCC1) be valued in CCT terms for
	// the block-reward calculation without ever touching
	// api/price_oracle.go's node-local live price cache - see
	// api/burn_value_oracle.go's cctEquivalentWholeOfBurn.
	"CctUsdCents": true,
}

// DefaultVaultEthConfirmations/DefaultVaultBtcConfirmations are the
// conservative starting depths - 12 blocks (~2.5-3 min) for ETH/USDC,
// the long-standing "effectively final" heuristic; 6 blocks (~60 min
// mainnet) for BTC, matching standard exchange practice. Both are
// judgment calls, not derived from a spec - admin-tunable upward for
// large amounts via the allowlist above.
const (
	DefaultVaultEthConfirmations = 12
	DefaultVaultBtcConfirmations = 6
	// DefaultVaultBtcReleaseFeeRateSatsPerVByte is a conservative flat
	// starting rate - a judgment call, not derived from a live market
	// read (see the allowlist doc comment above for why this must stay
	// fixed/consensus-agreed rather than live). Admin-tunable if real
	// mempool conditions run persistently above/below this.
	DefaultVaultBtcReleaseFeeRateSatsPerVByte = 10
)

func vaultEthConfirmations() uint64 {
	return governanceParamsStore.GetOrDefault("VaultEthConfirmations", DefaultVaultEthConfirmations)
}

func vaultBtcConfirmations() uint64 {
	return governanceParamsStore.GetOrDefault("VaultBtcConfirmations", DefaultVaultBtcConfirmations)
}

func vaultBtcReleaseFeeRateSatsPerVByte() uint64 {
	return governanceParamsStore.GetOrDefault("VaultBtcReleaseFeeRateSatsPerVByte", DefaultVaultBtcReleaseFeeRateSatsPerVByte)
}

type adminSetParamData struct {
	Key   string `json:"key"`
	Value uint64 `json:"value"`
}

// HandleAdminSetParam directly overrides one allowlisted
// GovernanceParamsStore key - same self-signed node-authored transaction
// pattern as HandleAdminSetSalePrice, but a second, faster writer to the
// same store the community PARAM_CHANGE governance-vote path already
// writes to (core/governance_params.go's Set is otherwise only called
// from a passed proposal). Built for bootstrapping/tuning real economic
// constants at mainnet launch before any real voting power exists, and
// for reacting quickly to something like the bridge deposit cap without
// a full proposal+vote cycle each time.
func HandleAdminSetParam(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req adminSetParamData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if !adminSettableParams[req.Key] {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "key is not in the admin-settable allowlist"})
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxAdminSetParam, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// applyAdminSetParam re-checks the allowlist (never trust submission-time
// state - the HTTP-layer check above is a fast-fail, not the security
// boundary) before writing through to governanceParamsStore.Set.
func applyAdminSetParam(tx core.Transaction) {
	var data adminSetParamData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !adminSettableParams[data.Key] {
		return
	}
	governanceParamsStore.SetAt(data.Key, data.Value, tx.Timestamp)
	syncTrustWeightIfNeeded(data.Key, data.Value)
}

// HandleGetAdminParams is a public, unauthenticated read of every
// currently-set governance parameter - same "no confidentiality to
// protect" reasoning as HandleAdminMultisigProposals: these values are
// already visible on-chain the moment any TxAdminSetParam/PARAM_CHANGE
// proposal confirms, and every signer needs to see the current value
// before proposing a new one anyway.
func HandleGetAdminParams(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(governanceParamsStore.Snapshot())
}

type bridgeExcessDepositEntry struct {
	CreditChainAddress string `json:"creditChainAddress"`
	Symbol             string `json:"symbol"`
	ExcessWei          string `json:"excessWei,omitempty"`
	ExcessSats         uint64 `json:"excessSats,omitempty"`
}

// HandleAdminBridgeExcessDeposits lists every account with a nonzero
// account-holdings-cap excess (see applyBridgeDeposit/
// applyBridgeBtcDeposit in api/apply.go and api/account_holdings_cap.go)
// - a concrete, persisted list for the admin to work from once the
// account has headroom again or a manual credit is decided, rather than
// relying on log lines that scroll away.
func HandleAdminBridgeExcessDeposits(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if _, _, err := requireAdminAuthed(r, nil); err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	entries := []bridgeExcessDepositEntry{}
	for _, account := range ethCustodialStore.Snapshot() {
		if account.UncreditedExcessWei != "" && account.UncreditedExcessWei != "0" {
			entries = append(entries, bridgeExcessDepositEntry{
				CreditChainAddress: account.CreditChainAddress,
				Symbol:             "ETHB",
				ExcessWei:          account.UncreditedExcessWei,
			})
		}
	}
	for _, watched := range btcAddressStore.Snapshot() {
		if watched.UncreditedExcessSats > 0 {
			entries = append(entries, bridgeExcessDepositEntry{
				CreditChainAddress: watched.CreditChainAddress,
				Symbol:             "BTCB",
				ExcessSats:         watched.UncreditedExcessSats,
			})
		}
	}

	json.NewEncoder(w).Encode(entries)
}
