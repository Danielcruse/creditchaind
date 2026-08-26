package api

import (
	"encoding/json"
	"net/http"
)

// HandleVaultBalance mirrors HandleEthbBalance/HandleBtcbBalance's shape
// exactly - the caller (website/js/vault.js) already resolves the
// correct balance address client-side (mirroring api/subaddress.go's
// resolveBalanceAddress: ETH_CCC1/USDC_CCC1 unify with the wallet's real
// self-custodial ETH address, BTC_CCC1 uses a derived sub-address), this
// handler just looks the balance up. ETH_CCC1/USDC_CCC1 balances are
// decimal strings (wei/USDC-6dp can exceed a JS-safe integer), BTC_CCC1
// is a plain integer (sats never overflow).
func HandleVaultBalance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	symbol := r.URL.Query().Get("symbol")
	address := r.URL.Query().Get("address")

	switch symbol {
	case "ETH_CCC1":
		// Normalized before lookup - see normalizeEthAddress's doc
		// comment (api/subaddress.go) for why a caller passing a
		// differently-cased spelling of the same real address must
		// never silently read back a different (usually zero) balance.
		json.NewEncoder(w).Encode(map[string]string{"balance": ethCcc1Store.BalanceOf(normalizeEthAddress(address))})
	case "USDC_CCC1":
		json.NewEncoder(w).Encode(map[string]string{"balance": usdcCcc1Store.BalanceOf(normalizeEthAddress(address))})
	case "STETH_CCC1":
		// Same real self-custodial ETH address as ETH_CCC1/USDC_CCC1 -
		// stETH is an ERC-20 held at whatever address holds it, not a
		// separate CreditChain-derived one (see api/lido_stake.go).
		json.NewEncoder(w).Encode(map[string]string{"balance": stethCcc1Store.BalanceOf(normalizeEthAddress(address))})
	case "BTC_CCC1":
		json.NewEncoder(w).Encode(map[string]uint64{"balance": btcCcc1Store.BalanceOf(address)})
	default:
		http.Error(w, "symbol must be ETH_CCC1, USDC_CCC1, STETH_CCC1, or BTC_CCC1", http.StatusBadRequest)
	}
}

// HandleVaultInfo is a public, unauthenticated read the Phase F frontend
// deposit flows need before anything else: where to actually send funds
// (the ETH/USDC vault contract's real deployed address, or BTC's derived
// shared P2WSH address), and the threshold/confirmation-depth values a
// status page needs to render "N of M validators confirmed" correctly.
// Deliberately safe to expose - every one of these values is either
// already public on-chain (the contract address, the derived P2WSH
// address) or a governance parameter with no confidentiality value
// (bridgeValidatorThreshold, confirmation depths).
func HandleVaultInfo(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	chain := r.URL.Query().Get("chain")
	if !vaultChainValid(chain) {
		http.Error(w, "chain must be ETH, BTC, USDC, or STETH", http.StatusBadRequest)
		return
	}

	resp := map[string]any{
		"chain":             chain,
		"requiredThreshold": bridgeValidatorThreshold(),
	}

	// The two chain families use different fee models now - see
	// api/vault_fee.go's splitVaultReleaseFee doc comment. BTC still
	// shows a flat bps rate; ETH/USDC show the actual estimated
	// smallest-unit fee amount (gas price x gas units x 1.10) so a
	// client (vault-withdraw.js) can show the real number before the
	// user confirms, rather than a percentage they'd have to compute
	// against for themselves.
	if chain == "BTC" {
		resp["releaseFeeBps"] = func() uint64 {
			if !vaultFeeRecipientConfigured(chain) {
				return 0
			}
			return vaultReleaseFeeBps()
		}()
	} else {
		resp["estimatedReleaseFeeSmallestUnit"] = vaultReleaseEstimatedFeeWei(chain).String()
	}

	switch chain {
	case "ETH", "USDC", "STETH":
		addr, ok := vaultContractAddress()
		resp["configured"] = ok
		if ok {
			resp["depositAddress"] = addr.Hex()
		}
		resp["requiredConfirmations"] = vaultEthConfirmations()
		if chain == "USDC" {
			if usdcAddr, err := usdcContractAddress(); err == nil {
				resp["tokenContractAddress"] = usdcAddr
			}
		}
		if chain == "STETH" {
			// depositERC20() targets Lido's own real stETH contract here,
			// same "tokenContractAddress" shape USDC already uses - see
			// api/lido_stake.go's doc comment for why this is the ONLY
			// new contract address this feature needed.
			if lidoAddr, err := lidoStethContractAddress(); err == nil {
				resp["tokenContractAddress"] = lidoAddr
			} else {
				resp["configured"] = false
			}
		}
	case "BTC":
		validators := bridgeValidatorStore.Snapshot()
		addr, err := btcVaultAddressString(validators)
		resp["configured"] = err == nil
		if err == nil {
			resp["depositAddress"] = addr
		}
		resp["requiredConfirmations"] = vaultBtcConfirmations()
		// releaseFeeRateSatsPerVByte lets a browser-based BTC release-
		// approval signer (client-side, no server RPC access) reconstruct
		// the IDENTICAL unsigned transaction api/vault_btc_release_relay.go
		// builds - any mismatch here just produces a signature that fails
		// to verify against what actually gets broadcast (see that file's
		// top doc comment), never a fund-safety issue.
		resp["releaseFeeRateSatsPerVByte"] = vaultBtcReleaseFeeRateSatsPerVByte()
	}

	json.NewEncoder(w).Encode(resp)
}
