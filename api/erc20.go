package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strconv"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// api/erc20.go is the SELF-CUSTODIAL counterpart to eth_selfcustody.go,
// scoped to real ERC-20 tokens (USDC first) rather than plain ETH
// transfers. Same trust model throughout: this file never sees,
// stores, or needs a private key for any address it's asked about -
// every address is caller-supplied, all signing happens in the
// browser (website/js/vendor/eth-tx.js + erc20-abi.js), and this is a
// pure read-through-RPC proxy. There is no custodial ERC-20 corner and
// none should ever be added here - the user has explicitly and
// repeatedly rejected CreditChain holding any token's private key.

// usdcMainnetContractAddress / usdcSepoliaContractAddress are the real,
// well-known USDC contract addresses on Ethereum mainnet and Sepolia
// testnet respectively. USDC_CONTRACT_ADDRESS overrides both for an
// operator pointing at a non-standard deployment - always verify the
// resolved address against Etherscan before relying on it for a real
// mainnet deposit/send, since a wrong contract address here would
// silently look correct but move nothing (or the wrong token).
const (
	usdcMainnetContractAddress = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	usdcSepoliaContractAddress = "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
	usdcDecimals               = 6
)

func usdcContractAddress() (string, error) {
	if addr := os.Getenv("USDC_CONTRACT_ADDRESS"); addr != "" {
		return addr, nil
	}
	switch expectedEthChainID.Int64() {
	case 1:
		return usdcMainnetContractAddress, nil
	case 11155111:
		return usdcSepoliaContractAddress, nil
	default:
		return "", fmt.Errorf("no known USDC contract for chain ID %s - set USDC_CONTRACT_ADDRESS explicitly", expectedEthChainID)
	}
}

// erc20BalanceOf calls the standard ERC-20 balanceOf(address) view
// function via eth_call - a pure read, no key/signing involved. The
// call data is the fixed 4-byte function selector
// (keccak256("balanceOf(address)")[:4] = 0x70a08231) followed by the
// holder address left-padded to a 32-byte word, per the standard ABI
// encoding for a single address argument - no ABI library needed for
// one fixed, well-known call shape.
func erc20BalanceOf(ctx context.Context, contractAddr, holderAddr string) (*big.Int, error) {
	contract := common.HexToAddress(contractAddr)
	holder := common.HexToAddress(holderAddr)

	data := make([]byte, 4+32)
	data[0], data[1], data[2], data[3] = 0x70, 0xa0, 0x82, 0x31
	copy(data[4+12:4+32], holder.Bytes())

	result, err := ethRPCClient.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(result), nil
}

// HandleUsdcSelfCustodyBalance returns the real USDC ERC-20 balance for
// an arbitrary self-custodial Ethereum address - the same address
// already used for self-custodial ETH (see eth_selfcustody.go), since
// an ERC-20 token balance lives at whatever address holds the token,
// not a separate CreditChain-issued one. No CreditChain auth: this
// isn't tied to any stored account, just a live RPC read-through for
// whatever address the caller already knows is its own.
func HandleUsdcSelfCustodyBalance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if ethRPCClient == nil {
		http.Error(w, "ETH RPC not enabled (no ETH_RPC_URL/SEPOLIA_RPC_URL configured)", http.StatusServiceUnavailable)
		return
	}

	address := r.URL.Query().Get("address")
	if !common.IsHexAddress(address) {
		http.Error(w, "address must be a valid 0x-prefixed Ethereum address", http.StatusBadRequest)
		return
	}

	contract, err := usdcContractAddress()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()

	balance, err := erc20BalanceOf(ctx, contract, address)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query USDC balance: %v", err), http.StatusBadGateway)
		return
	}
	nonce, gasPrice, err := ethNonceAndGasPrice(address)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query nonce/gas price: %v", err), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"address":              address,
		"contract":             contract,
		"balance":              balance.String(),
		"decimals":             strconv.Itoa(usdcDecimals),
		"nonce":                strconv.FormatUint(nonce, 10),
		"suggestedGasPriceWei": gasPrice.String(),
	})
}

// HandleErc20SelfCustodyBalance is the generic version of
// HandleUsdcSelfCustodyBalance - accepts any real ERC-20 contract
// address (via ?contract=) rather than hardcoding USDC, so a new token
// (e.g. Lido's stETH, see api/lido_stake.go) never needs its own
// bespoke balance endpoint. Same pure read-through-RPC trust model as
// the rest of this file - no key, no auth, every address is caller-
// supplied.
func HandleErc20SelfCustodyBalance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if ethRPCClient == nil {
		http.Error(w, "ETH RPC not enabled (no ETH_RPC_URL/SEPOLIA_RPC_URL configured)", http.StatusServiceUnavailable)
		return
	}

	address := r.URL.Query().Get("address")
	contract := r.URL.Query().Get("contract")
	if !common.IsHexAddress(address) {
		http.Error(w, "address must be a valid 0x-prefixed Ethereum address", http.StatusBadRequest)
		return
	}
	if !common.IsHexAddress(contract) {
		http.Error(w, "contract must be a valid 0x-prefixed Ethereum address", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()

	balance, err := erc20BalanceOf(ctx, contract, address)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query balance: %v", err), http.StatusBadGateway)
		return
	}
	nonce, gasPrice, err := ethNonceAndGasPrice(address)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query nonce/gas price: %v", err), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"address":              address,
		"contract":             contract,
		"balance":              balance.String(),
		"nonce":                strconv.FormatUint(nonce, 10),
		"suggestedGasPriceWei": gasPrice.String(),
	})
}
