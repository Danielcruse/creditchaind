package api

import (
	"fmt"
	"os"
)

// This file is the ETH liquid-staking feature's contract-address
// resolution - see api/vault_lock.go's ccc1SymbolForChain/
// mintVaultToken and api/vault_eth_watch.go for how the "STETH" chain
// identifier this resolves flows through the SAME lock-and-mint
// attestation path ETH_CCC1/USDC_CCC1 already use, once a real Lido
// deposit is locked into the CreditChain vault contract (see
// website/js/vault.js's Lido staking helpers for the client-side
// unvault -> Lido submit() -> vault depositERC20() sequence).
//
// The staking flow itself has no other new consensus-critical Go code:
// unvaulting reuses HandleVaultBurn unchanged (see api/vault_lock.go),
// and minting STETH_CCC1 reuses the existing bridge-validator lock-
// attestation threshold mechanism unchanged - only the address this
// file resolves, and the chain-recognition switch case in the watch
// loop, are new.

// lidoStethMainnetAddress is Lido's real, well-known stETH/core
// contract address on Ethereum mainnet - verified directly against
// Lido's own official documentation (docs.lido.fi/deployed-contracts)
// before use, given a wrong address here would mean unvaulted ETH
// staked at a real transaction that either reverts or, worse, sends
// value to an unintended real contract. LIDO_STETH_CONTRACT_ADDRESS
// overrides this for an operator pointing at a different deployment -
// always re-verify against Lido's own docs before relying on an
// override for a real deposit.
const lidoStethMainnetAddress = "0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84"

// lidoStethContractAddress mirrors usdcContractAddress's exact
// resolution shape - mainnet only. Lido has no equivalent production
// deployment on Sepolia this codebase's testnet setup could point at,
// so the ETH liquid-staking feature is mainnet-only by construction:
// this returns an error on any other chain ID rather than guessing.
func lidoStethContractAddress() (string, error) {
	if addr := os.Getenv("LIDO_STETH_CONTRACT_ADDRESS"); addr != "" {
		return addr, nil
	}
	if expectedEthChainID.Int64() == 1 {
		return lidoStethMainnetAddress, nil
	}
	return "", fmt.Errorf("no known Lido stETH contract for chain ID %s - ETH liquid staking is mainnet-only", expectedEthChainID)
}
