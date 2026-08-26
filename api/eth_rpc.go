package api

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ethRPCClient is nil unless an RPC URL is set and reachable - every
// ETH-custodial handler that needs it checks for nil and returns a clear
// error rather than panicking, same "degrade gracefully" pattern as
// db.go/eth_signer.go. Needs a free RPC provider API key (Infura,
// Alchemy, or similar) - not something this process can obtain on its
// own.
var ethRPCClient *ethclient.Client

// sepoliaChainID is Sepolia testnet's real, fixed chain ID - kept as a
// named constant (not just "the default") because the KMS safety gate
// below specifically distinguishes "this is Sepolia" from "this is
// anything else," regardless of what expectedEthChainID ends up
// resolving to.
var sepoliaChainID = big.NewInt(11155111)

// expectedEthChainID is which chain this node's ETH custodial corner is
// configured for - Sepolia by default (today's exact behavior,
// unchanged if ETH_EXPECTED_CHAIN_ID is never set), or whatever a
// mainnet instance sets it to (1 for real Ethereum mainnet). Resolved
// once in InitEthRPC, then used everywhere a chain ID is needed for
// EIP-155 signing/verification (core/eth_custodial.go's doc comment
// covers the licensing/legal reasoning behind not silently drifting
// this to mainnet).
var expectedEthChainID = sepoliaChainID

func InitEthRPC() {
	if id := os.Getenv("ETH_EXPECTED_CHAIN_ID"); id != "" {
		parsed, ok := new(big.Int).SetString(id, 10)
		if !ok {
			log.Println("ETH custodial corner disabled: ETH_EXPECTED_CHAIN_ID is not a valid integer:", id)
			return
		}
		expectedEthChainID = parsed
	}

	// NOTE: the "non-Sepolia requires real KMS" safety gate does NOT
	// live here (it used to, and that was a bug) - this function only
	// ever establishes a read/relay RPC connection, used by BOTH the
	// custodial corner (which needs the KMS gate) AND the fully
	// self-custodial flow (api/eth_selfcustody.go), which never touches
	// ethSigner/KMS at all - the private key never leaves the user's own
	// browser regardless of which chain this is pointed at, so gating
	// this shared connection on KMS would have blocked a feature that
	// carries none of the risk the gate exists to prevent. The real gate
	// now lives in HandleCreateEthCustodialAccount (api/eth_custodial.go),
	// scoped to exactly the one action (provisioning a server-held key)
	// that actually needs it.
	url := os.Getenv("ETH_RPC_URL")
	if url == "" {
		url = os.Getenv("SEPOLIA_RPC_URL") // legacy env var name, still honored
	}
	if url == "" {
		log.Println("ETH_RPC_URL/SEPOLIA_RPC_URL not set - ETH custodial corner disabled (no RPC connection)")
		return
	}

	client, err := ethclient.Dial(url)
	if err != nil {
		log.Println("ETH custodial corner disabled: failed to connect to RPC:", err)
		return
	}

	// Confirm we're actually talking to the chain we think we are, not
	// some other network the operator's RPC URL happens to point at - a
	// same-looking mistake here (e.g. an Ethereum mainnet URL while
	// still expecting Sepolia) would be exactly the "custodying real
	// value without the licensing review" scenario this whole feature
	// is scoped to avoid.
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	gotChainID, err := client.ChainID(ctx)
	if err != nil {
		log.Println("ETH custodial corner disabled: failed to query chain ID from RPC:", err)
		return
	}
	if gotChainID.Cmp(expectedEthChainID) != 0 {
		log.Printf("ETH custodial corner disabled: RPC URL points at chain ID %s, expected %s - refusing to enable against an unexpected network", gotChainID, expectedEthChainID)
		return
	}

	ethRPCClient = client
	log.Printf("ETH RPC connected: chain ID %s (self-custodial send/receive enabled; the custodial signing corner has its own separate KMS gate, see HandleCreateEthCustodialAccount)", expectedEthChainID)
}

const rpcTimeout = 15_000_000_000 // 15s, as a time.Duration-compatible int (nanoseconds)

// ethBalance returns addr's real Sepolia balance in wei.
func ethBalance(addr string) (*big.Int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return ethRPCClient.BalanceAt(ctx, common.HexToAddress(addr), nil)
}

// ethNonceAndGasPrice returns addr's next pending nonce and the RPC's
// currently suggested gas price - both needed by a self-custodial
// client to build an unsigned transaction locally, since the browser
// has no way to query the chain itself (see eth_selfcustody.go's doc
// comment for why this proxy exists at all). Same two ethRPCClient
// calls buildAndBroadcastWithdrawal already trusts, just for an
// arbitrary caller-supplied address instead of a stored custodial one.
func ethNonceAndGasPrice(addr string) (uint64, *big.Int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	nonce, err := ethRPCClient.PendingNonceAt(ctx, common.HexToAddress(addr))
	if err != nil {
		return 0, nil, err
	}
	gasPrice, err := ethRPCClient.SuggestGasPrice(ctx)
	if err != nil {
		return 0, nil, err
	}
	return nonce, gasPrice, nil
}

// ethBroadcastRaw relays an already-signed transaction (RLP-encoded,
// exactly what tx.MarshalBinary()/eth_sendRawTransaction expects) to
// the network. It never constructs, signs, or holds a private key -
// the caller (a self-custodial browser wallet) already did all of
// that; this is a pure relay, mirroring buildAndBroadcastWithdrawal's
// final broadcast step but skipping every step before it.
func ethBroadcastRaw(ctx context.Context, rawTxHex string) (string, error) {
	rawBytes, err := hexutil.Decode(rawTxHex)
	if err != nil {
		return "", fmt.Errorf("invalid raw tx hex: %w", err)
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(rawBytes); err != nil {
		return "", fmt.Errorf("invalid RLP-encoded transaction: %w", err)
	}
	// Reject anything not targeting the configured chain before it ever
	// reaches the RPC call - the chain ID here is attacker(client)-
	// controlled input, unlike InitEthRPC's own one-time startup check
	// on the RPC endpoint itself, so this needs its own per-request
	// guard.
	if tx.ChainId() == nil || tx.ChainId().Cmp(expectedEthChainID) != 0 {
		return "", fmt.Errorf("transaction is not for the configured chain (got %s, expected %s)", tx.ChainId(), expectedEthChainID)
	}
	if err := ethRPCClient.SendTransaction(ctx, tx); err != nil {
		return "", err
	}
	return tx.Hash().Hex(), nil
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

	signer := types.LatestSignerForChainID(expectedEthChainID)
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

// buildAndBroadcastContractCall is buildAndBroadcastWithdrawal's
// sibling for calling into a real deployed contract (the vault -
// contracts/CreditChainVault.sol) - same nonce/sign/broadcast skeleton,
// but builds an EIP-1559 types.DynamicFeeTx (the modern norm, worth
// using over legacy gas pricing for a fresh deployment) with `To` set
// to the contract address and `Data` set to already-ABI-encoded
// calldata (see api/vault_contract_abi.go's packDepositCalldata/
// packDepositERC20Calldata/packReleaseCalldata) instead of a bare
// value transfer. `value` is nonzero only for a plain-ETH deposit()
// call; every other call (depositERC20/release) passes big.NewInt(0).
// buildUnsignedContractCallTx does every step of a contract-call
// transaction EXCEPT signing - shared by buildAndBroadcastContractCall
// (custodial per-user keys via the ethSigner abstraction) and
// buildAndBroadcastContractCallWithPrivateKey (a single fixed operator-
// held key, e.g. the vault release relay's RELAYER_PRIVATE_KEY_HEX,
// which doesn't fit the per-user-custodial-key shape ethSigner exists
// for - see api/vault_release_relay.go).
func buildUnsignedContractCallTx(ctx context.Context, fromAddr, contractAddr string, data []byte, value *big.Int) (*types.Transaction, types.Signer, [32]byte, error) {
	var hash32 [32]byte
	from := common.HexToAddress(fromAddr)
	to := common.HexToAddress(contractAddr)

	nonce, err := ethRPCClient.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, nil, hash32, err
	}

	tipCap, err := ethRPCClient.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, hash32, err
	}
	head, err := ethRPCClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, nil, hash32, err
	}
	// Standard "2x current base fee + tip" heuristic - generous enough
	// to still be valid a few blocks later without needing to resubmit,
	// same conservative-headroom reasoning this codebase already applies
	// to its own live-gas-price-based fee quotes elsewhere.
	feeCap := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tipCap)

	gasLimit, err := ethRPCClient.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Value: value, Data: data})
	if err != nil {
		return nil, nil, hash32, fmt.Errorf("gas estimation failed: %w", err)
	}
	// A pure gas estimate can be an exact-but-fragile figure if state
	// shifts slightly between estimation and inclusion (e.g. another
	// pending tx changes a storage slot this call also touches) - a
	// modest safety margin avoids an otherwise-valid call failing from
	// running exactly out of gas.
	gasLimit = gasLimit * 12 / 10

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   expectedEthChainID,
		Nonce:     nonce,
		To:        &to,
		Value:     value,
		Gas:       gasLimit,
		GasFeeCap: feeCap,
		GasTipCap: tipCap,
		Data:      data,
	})

	signer := types.LatestSignerForChainID(expectedEthChainID)
	signingHash := signer.Hash(tx)
	copy(hash32[:], signingHash.Bytes())

	return tx, signer, hash32, nil
}

func buildAndBroadcastContractCall(ctx context.Context, keyRef, fromAddr, contractAddr string, data []byte, value *big.Int) (string, error) {
	tx, signer, hash32, err := buildUnsignedContractCallTx(ctx, fromAddr, contractAddr, data, value)
	if err != nil {
		return "", err
	}

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

// buildAndBroadcastContractCallWithRelayerSigner is
// buildAndBroadcastContractCall's sibling for a single fixed operator-
// held key (api/vault_relayer_signer.go's relayerSigner - KMS or raw-
// hex, resolved once by the caller) rather than a per-user custodial
// keyRef resolved through the ethSigner abstraction. Supersedes the
// earlier buildAndBroadcastContractCallWithPrivateKey, which only
// accepted a raw *ecdsa.PrivateKey directly and so could never route
// through KMS - see audit/KNOWN_GAPS.md's key-custody entry.
func buildAndBroadcastContractCallWithRelayerSigner(ctx context.Context, signer relayerSigner, fromAddr, contractAddr string, data []byte, value *big.Int) (string, error) {
	tx, txSigner, hash32, err := buildUnsignedContractCallTx(ctx, fromAddr, contractAddr, data, value)
	if err != nil {
		return "", err
	}

	sig, err := signer.sign(ctx, hash32)
	if err != nil {
		return "", err
	}

	signedTx, err := tx.WithSignature(txSigner, sig)
	if err != nil {
		return "", err
	}

	if err := ethRPCClient.SendTransaction(ctx, signedTx); err != nil {
		return "", err
	}

	return signedTx.Hash().Hex(), nil
}
