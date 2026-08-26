package api

import (
	"math/big"
	"os"

	"github.com/creditchain/creditchaind/core"
)

// This file adds a real-value-conserving fee on vault RELEASES (a burn +
// real-chain withdrawal), credited to whoever operates bridge-validator
// infrastructure - the actual out-of-pocket cost of running it (ETH/USDC
// gas the relayer pays from their own real balance, per
// api/vault_release_relay.go's doc comment) had no compensation
// mechanism at all before this. The fee comes out of the SAME
// already-locked CCC1 value being released, credited back into the SAME
// per-chain store at a configured recipient address - nothing is ever
// minted out of thin air and nothing is ever destroyed, so the vault's
// real locked-value backing is unaffected either way (the fee amount
// simply never leaves the vault, same as the recipient's own balance
// staying there until THEY withdraw it).

// DefaultVaultReleaseFeeBps is 0.5% (50 basis points of 10000) - BTC's
// release fee model only (see splitVaultReleaseFee): BTC release needs
// no relayer/gas cost at all (api/vault_btc_release_relay.go's own doc
// comment - broadcasting is free), so a flat percentage still makes
// sense there. ETH/USDC use vaultReleaseGasPriceWei's gas-cost-based
// model instead - see that function's doc comment for why a flat
// percentage stopped making sense for those two once a real relayer
// started paying real, independently-variable gas costs to actually
// broadcast each release.
const DefaultVaultReleaseFeeBps = 50

func vaultReleaseFeeBps() uint64 {
	return governanceParamsStore.GetOrDefault("VaultReleaseFeeBps", DefaultVaultReleaseFeeBps)
}

// DefaultVaultReleaseGasPriceWei is a conservative starting estimate
// (~20 gwei) - a judgment call, not a live market read (see
// VaultBtcReleaseFeeRateSatsPerVByte's identical reasoning in
// api/admin_params.go for why this must stay a FIXED, consensus-agreed
// value rather than each node's own live eth_gasPrice call: the
// signed release amount is part of what every bridge validator's
// signature covers, so all of them - and every node independently
// replaying applyVaultBurn - must compute the exact same fee from the
// exact same inputs, or their signatures/balances would diverge).
// Admin-tunable via the "VaultReleaseGasPriceWei" governance param as
// real gas prices drift - see api/admin_params.go's adminSettableParams.
const DefaultVaultReleaseGasPriceWei = 20_000_000_000

// vaultReleaseGasUnitsEth/vaultReleaseGasUnitsUsdc are fixed estimates
// of how much gas one release() call actually costs to execute on-chain -
// unlike the price-per-gas-unit above, actual gas USAGE for a given
// call shape is stable enough to hardcode (an ETH transfer costs
// noticeably less than an ERC20 transferFrom, hence two different
// constants), not something that needs periodic admin tuning the way a
// volatile market price does.
const (
	vaultReleaseGasUnitsEth  = 120_000
	vaultReleaseGasUnitsUsdc = 180_000
)

func vaultReleaseGasPriceWei() uint64 {
	return governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei)
}

// vaultReleaseEstimatedFeeWei is the real-gas-cost-based fee for one
// ETH or USDC release: the estimated real cost of broadcasting it
// (gas price x gas units) plus a flat 10% margin - see
// api/vault_release_approve_watch.go's doc comment and this session's
// design discussion for why this replaces the old flat-percentage
// model for these two chains specifically. Deliberately NOT applicable
// to BTC (see DefaultVaultReleaseFeeBps's doc comment).
func vaultReleaseEstimatedFeeWei(chain string) *big.Int {
	var gasUnits uint64
	switch chain {
	case "ETH":
		gasUnits = vaultReleaseGasUnitsEth
	case "USDC":
		gasUnits = vaultReleaseGasUnitsUsdc
	default:
		return big.NewInt(0)
	}
	gasCost := new(big.Int).Mul(new(big.Int).SetUint64(vaultReleaseGasPriceWei()), new(big.Int).SetUint64(gasUnits))
	fee := new(big.Int).Mul(gasCost, big.NewInt(110))
	fee.Div(fee, big.NewInt(100))
	return fee
}

// vaultFeeRecipientEthAddress is where ETH_CCC1/USDC_CCC1 release fees
// land - both stores are keyed by a real self-custodial ETH address (see
// api/subaddress.go's resolveBalanceAddress), so this must be a real
// 0x address, not a CreditChain cc-address. No hardcoded default - an
// unconfigured node simply doesn't collect this fee (splitVaultReleaseFee
// degrades to feeAmount=0), same "off unless explicitly configured"
// posture BRIDGE_VALIDATOR_PRIVATE_KEY_HEX already has.
func vaultFeeRecipientEthAddress() string {
	return os.Getenv("VAULT_FEE_RECIPIENT_ETH_ADDRESS")
}

// vaultFeeRecipientBtcCcc1Address is where BTC_CCC1 release fees land -
// btcCcc1Store is keyed by a CreditChain-derived sub-address (see
// core.BtcCcc1AddressTag), an opaque value only computable client-side
// from a wallet's own public key (website/js/vault.js's
// vaultBalanceAddress) - there is no server-side derivation path from a
// plain cc-address to this value, so the operator computes it once
// (open their own BTC_CCC1 balance page) and configures the resulting
// string directly, rather than this code trying to re-derive it.
func vaultFeeRecipientBtcCcc1Address() string {
	return os.Getenv("VAULT_FEE_RECIPIENT_BTC_CCC1_ADDRESS")
}

// vaultFeeRecipientConfigured reports whether a real fee recipient
// exists for this chain - splitVaultReleaseFee MUST check this before
// deducting anything: without it, a fee would be silently subtracted
// from what the user receives while creditVaultReleaseFee has nowhere
// to put it, permanently orphaning that value (still real, still
// locked in the vault contract, but attributed to no one's CCC1
// balance - a bug caught in review before any real release exercised
// it, see the incident this comment is describing).
func vaultFeeRecipientConfigured(chain string) bool {
	switch chain {
	case "ETH", "USDC":
		return vaultFeeRecipientEthAddress() != ""
	case "BTC":
		return vaultFeeRecipientBtcCcc1Address() != ""
	}
	return false
}

// splitVaultReleaseFee divides a requested release amount into
// (releaseAmount, feeAmount) at the current fee rate - releaseAmount is
// what actually gets signed for and sent to the real external address.
// BTC keeps the old flat-percentage model (feeAmount credited
// immediately via creditVaultReleaseFee, same as always - see that
// function's doc comment); ETH/USDC use vaultReleaseEstimatedFeeWei's
// gas-cost-based model instead, and feeAmount here is only ever HELD
// (see applyVaultBurn/core.VaultReleaseApproval.PendingFeeAmountSmallestUnit),
// credited later once a real relayer's TxVaultReleaseComplete says who
// actually paid the gas - see applyVaultReleaseComplete. Integer math
// throughout (no float), floor-rounded so releaseAmount+feeAmount never
// exceeds the original amount; if the fee would consume the entire
// requested amount (a withdrawal too small to cover real gas cost), the
// full amount becomes the fee and releaseAmount is 0 - the relayer
// still gets compensated for the gas it spends attempting this, but the
// user gets nothing external, same edge-case handling this file always
// had for BTC.
func splitVaultReleaseFee(chain string, amountSmallestUnit string) (releaseAmount string, feeAmount string) {
	amount, err := core.ParseDecimalSmallest(amountSmallestUnit)
	if err != nil || amount.Sign() <= 0 {
		return amountSmallestUnit, "0"
	}

	var fee *big.Int
	switch chain {
	case "ETH", "USDC":
		fee = vaultReleaseEstimatedFeeWei(chain)
	case "BTC":
		if !vaultFeeRecipientConfigured(chain) {
			return amountSmallestUnit, "0"
		}
		bps := vaultReleaseFeeBps()
		if bps == 0 {
			return amountSmallestUnit, "0"
		}
		fee = new(big.Int).Mul(amount, new(big.Int).SetUint64(bps))
		fee.Div(fee, big.NewInt(10000))
	default:
		return amountSmallestUnit, "0"
	}

	release := new(big.Int).Sub(amount, fee)
	if release.Sign() < 0 {
		release = big.NewInt(0)
		fee = amount
	}
	return release.String(), fee.String()
}

// creditVaultReleaseFee mints feeAmount into BTC's configured
// recipient's balance immediately at burn time - BTC-only now (see
// splitVaultReleaseFee's doc comment for why ETH/USDC defer this to
// applyVaultReleaseComplete via creditVaultReleaseFeeToRelayer
// instead). A no-op if feeAmount is zero or no recipient is configured,
// same as always.
func creditVaultReleaseFee(chain string, feeAmountSmallestUnit string) {
	if chain != "BTC" {
		return
	}
	fee, err := core.ParseDecimalSmallest(feeAmountSmallestUnit)
	if err != nil || fee.Sign() <= 0 || !fee.IsUint64() {
		return
	}
	recipient := vaultFeeRecipientBtcCcc1Address()
	if recipient == "" {
		return
	}
	btcCcc1Store.Mint(recipient, fee.Uint64())
}

// creditVaultReleaseFeeToRelayer mints the held ETH/USDC release fee
// into whichever real ETH address the winning relayer named in its own
// TxVaultReleaseComplete - see applyVaultReleaseComplete. A no-op if
// feeAmount is zero or the relayer didn't declare a recipient (an
// operator who never configured RELAYER_FEE_RECIPIENT_ETH_ADDRESS
// simply doesn't collect this - same "off unless explicitly
// configured" posture every other fee/key config in this codebase
// already has), so the fee is dropped rather than the release itself
// ever being blocked on it.
//
// relayerEthAddress is normalized (normalizeEthAddress) before minting -
// it comes straight from an env var (relayerFeeRecipientEthAddress),
// which is very likely to be pasted in checksummed/mixed-case form (the
// conventional way an Ethereum address is displayed/copied), but every
// OTHER ETH_CCC1/USDC_CCC1 balance read in this codebase
// (HandleVaultBalance, resolveBalanceAddress) always normalizes to
// lowercase first. Minting under the raw, un-normalized string would
// silently credit a balance no normal query could ever find again - a
// real incident this comment exists because of, caught by tracing a
// user's report of a withdrawal fee that seemed to have vanished: the
// fee had, in fact, minted correctly, just under a case-sensitive key
// nothing else in the codebase would ever look up.
func creditVaultReleaseFeeToRelayer(chain string, feeAmountSmallestUnit string, relayerEthAddress string) {
	if relayerEthAddress == "" {
		return
	}
	fee, err := core.ParseDecimalSmallest(feeAmountSmallestUnit)
	if err != nil || fee.Sign() <= 0 {
		return
	}
	relayerEthAddress = normalizeEthAddress(relayerEthAddress)
	switch chain {
	case "ETH":
		ethCcc1Store.Mint(relayerEthAddress, feeAmountSmallestUnit)
	case "USDC":
		usdcCcc1Store.Mint(relayerEthAddress, feeAmountSmallestUnit)
	}
}
