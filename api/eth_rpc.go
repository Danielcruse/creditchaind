package api

import (
	"context"
	"log"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ethRPCClient is nil unless SEPOLIA_RPC_URL is set and reachable - every
// ETH-custodial handler that needs it checks for nil and returns a clear
// error rather than panicking, same "degrade gracefully" pattern as
// db.go/eth_signer.go. Needs a free RPC provider API key (Infura,
// Alchemy, or similar) - not something this process can obtain on its
// own.
var ethRPCClient *ethclient.Client

// sepoliaChainID is Sepolia testnet's real, fixed chain ID - required for
// EIP-155 replay-protected transaction signing (core/eth_custodial.go's
// doc comment covers why this points at a testnet, not mainnet).
var sepoliaChainID = big.NewInt(11155111)

func InitEthRPC() {
	url := os.Getenv("SEPOLIA_RPC_URL")
	if url == "" {
		log.Println("SEPOLIA_RPC_URL not set - ETH custodial corner disabled (no RPC connection)")
		return
	}

	client, err := ethclient.Dial(url)
	if err != nil {
		log.Println("ETH custodial corner disabled: failed to connect to Sepolia RPC:", err)
		return
	}

	// Confirm we're actually talking to Sepolia, not some other network
	// the operator's RPC URL happens to point at - a same-looking
	// mistake here (e.g. an Ethereum mainnet URL) would be exactly the
	// "custodying real value without the licensing review" scenario this
	// whole feature is scoped to avoid.
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	gotChainID, err := client.ChainID(ctx)
	if err != nil {
		log.Println("ETH custodial corner disabled: failed to query chain ID from RPC:", err)
		return
	}
	if gotChainID.Cmp(sepoliaChainID) != 0 {
		log.Printf("ETH custodial corner disabled: SEPOLIA_RPC_URL points at chain ID %s, expected Sepolia (%s) - refusing to enable against an unexpected network", gotChainID, sepoliaChainID)
		return
	}

	ethRPCClient = client
	log.Println("ETH custodial corner enabled: connected to Sepolia testnet")
}

const rpcTimeout = 15_000_000_000 // 15s, as a time.Duration-compatible int (nanoseconds)

// ethBalance returns addr's real Sepolia balance in wei.
func ethBalance(addr string) (*big.Int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return ethRPCClient.BalanceAt(ctx, common.HexToAddress(addr), nil)
}

// buildAndBroadcastWithdrawal constructs a real EIP-155 Sepolia
// transaction sending amountWei from the custodial account's own address
// to `to`, signs it via the given signer/keyRef (never touching the raw
// private key directly - see eth_signer.go), and broadcasts it. Returns
// the transaction hash on success.
func buildAndBroadcastWithdrawal(ctx context.Context, keyRef, fromAddr, to string, amountWei *big.Int) (string, error) {
	from := common.HexToAddress(fromAddr)
	toAddr := common.HexToAddress(to)

	nonce, err := ethRPCClient.PendingNonceAt(ctx, from)
	if err != nil {
		return "", err
	}
	gasPrice, err := ethRPCClient.SuggestGasPrice(ctx)
	if err != nil {
		return "", err
	}

	const plainTransferGas = 21000
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &toAddr,
		Value:    amountWei,
		Gas:      plainTransferGas,
		GasPrice: gasPrice,
	})

	signer := types.LatestSignerForChainID(sepoliaChainID)
	signingHash := signer.Hash(tx)

	var hash32 [32]byte
	copy(hash32[:], signingHash.Bytes())

	sig, err := ethSigner.Sign(ctx, keyRef, hash32)
	if err != nil {
		return "", err
	}

	signedTx, err := tx.WithSignature(signer, sig)
	if err != nil {
		return "", err
	}

	if err := ethRPCClient.SendTransaction(ctx, signedTx); err != nil {
		return "", err
	}

	return signedTx.Hash().Hex(), nil
}
