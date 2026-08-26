package api

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/creditchain/creditchaind/core"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// This file is the release-approval counterpart to vault_eth_watch.go:
// before it existed, a real release only ever got co-signed when a
// human opened admin-vault-keys.html and clicked "Sign & Approve" - if
// nobody happened to notice a pending release, it just sat there
// forever, the exact same single-point-of-failure that once left real
// ETH deposits undetected for days. This automates the co-signing
// itself, not the authorization it represents: the vault contract's
// own ecrecover-based threshold check still requires bridgeValidatorThreshold
// INDEPENDENT signing keys to agree before release() will actually
// execute - each validator just runs their own agreement automatically
// now, instead of a human manually reproducing it in a browser.

const vaultReleaseApproveWatchPollInterval = 30 * time.Second

// releaseApprovalDigestArgs mirrors website/js/vault.js's
// buildReleaseApprovalDigest exactly (address, address, uint256,
// uint256, address, uint256, in that order) - a standalone
// keccak256(abi.encode(...)) digest, NOT a function call, so this uses
// abi.Arguments.Pack directly rather than vaultContractABI.Pack (which
// prefixes a 4-byte function selector release() itself doesn't sign
// over).
var releaseApprovalDigestArgs abi.Arguments

func init() {
	addressType, err := abi.NewType("address", "", nil)
	if err != nil {
		panic("vault_release_approve_watch.go: bad address abi type: " + err.Error())
	}
	uint256Type, err := abi.NewType("uint256", "", nil)
	if err != nil {
		panic("vault_release_approve_watch.go: bad uint256 abi type: " + err.Error())
	}
	releaseApprovalDigestArgs = abi.Arguments{
		{Type: addressType}, {Type: addressType}, {Type: uint256Type},
		{Type: uint256Type}, {Type: addressType}, {Type: uint256Type},
	}
}

// buildReleaseApprovalDigestGo is the Go-side twin of vault.js's
// buildReleaseApprovalDigest - must stay byte-for-byte identical, or
// this node's signatures would verify against a different message than
// a browser-signed approval's and the contract's ecrecover would just
// never match either one.
func buildReleaseApprovalDigestGo(to, token common.Address, amount, releaseID *big.Int, vaultContract common.Address, chainID *big.Int) ([]byte, error) {
	packed, err := releaseApprovalDigestArgs.Pack(to, token, amount, releaseID, vaultContract, chainID)
	if err != nil {
		return nil, err
	}
	return crypto.Keccak256(packed), nil
}

// vaultReleaseSigningKey loads this node operator's own secp256k1
// release-approval signing key from VAULT_RELEASE_SIGNING_KEY_HEX -
// deliberately a THIRD, separate credential from both
// RELAYER_PRIVATE_KEY_HEX (vault_release_relay.go - pays gas only, no
// authority over what's valid) and bridgeValidatorSigningKey
// (vault_eth_watch.go - Ed25519, only authenticates which CreditChain
// wallet is submitting). This is the one with real on-chain authority:
// its derived address must match whatever EthSigningAddress this
// validator declared via HandleBridgeValidatorRegister, or the vault
// contract's own ecrecover check will simply never accept its
// signatures - a node with no key configured here (or a mismatched one)
// just never successfully co-signs, same "can run as an ordinary node
// without this role" degrade-gracefully posture bridgeValidatorSigningKey
// already has.
func vaultReleaseSigningKey() (address string, priv *ecdsa.PrivateKey, ok bool) {
	raw := strings.TrimSpace(os.Getenv("VAULT_RELEASE_SIGNING_KEY_HEX"))
	if raw == "" {
		return "", nil, false
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(raw, "0x"))
	if err != nil {
		log.Println("VAULT_RELEASE_SIGNING_KEY_HEX is set but not a valid hex-encoded secp256k1 private key - this node will not auto-sign vault release approvals")
		return "", nil, false
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), key, true
}

// ensureVaultReleaseSigningKeyRegistered self-registers this node's
// configured VAULT_RELEASE_SIGNING_KEY_HEX as its bridge-validator
// wallet's declared EthSigningAddress, if it isn't already - so a fresh
// deployment only ever needs the two env vars set, not a separate
// manual HandleBridgeValidatorRegister submission through the UI first.
// A no-op once already registered with the matching address (checked
// every startup, so rotating the key just re-registers under its new
// address automatically). The BTC public key field is a required part
// of the same registration record (see HandleBridgeValidatorRegister's
// shape check) but genuinely unused by anything yet - BTC release
// needs no relayer/signing key at all (see
// api/vault_btc_release_relay.go's own doc comment) - so this reuses
// the SAME secp256k1 key's own compressed public key rather than
// generating a second, wholly pointless keypair just to fill the field.
func ensureVaultReleaseSigningKeyRegistered(walletAddress string, walletKey ed25519.PrivateKey, signAddress string, signKey *ecdsa.PrivateKey) {
	if existing, ok := bridgeValidatorStore.Get(walletAddress); ok && strings.EqualFold(existing.EthSigningAddress, signAddress) {
		return
	}

	btcPubKeyHex := hex.EncodeToString(crypto.CompressPubkey(&signKey.PublicKey))
	payload := bridgeValidatorRegisterData{
		EthSigningAddress: signAddress,
		BtcPublicKeyHex:   btcPubKeyHex,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return
	}

	env := core.SignedEnvelope{
		Address:   walletAddress,
		PublicKey: hex.EncodeToString(walletKey.Public().(ed25519.PublicKey)),
		Timestamp: time.Now().Unix(),
		Payload:   payloadJSON,
	}
	env.Signature = hex.EncodeToString(core.SignMessage(walletKey, env.CanonicalMessage()))

	recordTx(env, walletAddress, "", 0, core.TxBridgeValidatorRegister, string(payloadJSON))
	log.Println("vault release auto-approve: self-registered EthSigningAddress", signAddress, "for bridge validator", walletAddress)
}

// startVaultReleaseApproveWatchLoop no-ops entirely unless this node has
// both a bridge-validator wallet identity AND a release-signing key
// configured - same "degrade gracefully" posture every other watch loop
// in this codebase already has.
func startVaultReleaseApproveWatchLoop() {
	walletAddress, walletKey, ok := bridgeValidatorSigningKey()
	if !ok {
		return
	}
	signAddress, signKey, ok := vaultReleaseSigningKey()
	if !ok {
		return
	}
	if _, ok := vaultContractAddress(); !ok {
		return
	}

	ensureVaultReleaseSigningKeyRegistered(walletAddress, walletKey, signAddress, signKey)

	go func() {
		ticker := time.NewTicker(vaultReleaseApproveWatchPollInterval)
		defer ticker.Stop()
		for range ticker.C {
			scanVaultReleaseApprovalsForAutoSign()
		}
	}()
	log.Println("vault release auto-approve loop started")
}

// signatureMatchesAddress reports whether sig recovers to addr for the
// given digest - Ethereum's 27/28 recovery-id convention is normalized
// to go-ethereum's 0/1 first, same adjustment applied everywhere else
// in this file that hands a signature to/from crypto.Sign.
func signatureMatchesAddress(digest []byte, sig []byte, addr common.Address) bool {
	if len(sig) != 65 {
		return false
	}
	normalized := make([]byte, 65)
	copy(normalized, sig)
	if normalized[64] >= 27 {
		normalized[64] -= 27
	}
	pub, err := crypto.SigToPub(digest, normalized)
	if err != nil {
		return false
	}
	return crypto.PubkeyToAddress(*pub) == addr
}

// scanVaultReleaseApprovalsForAutoSign signs and submits
// TxVaultReleaseApprove for every ETH/USDC release this validator's own
// wallet address hasn't already CORRECTLY co-signed - "correctly"
// meaning the stored signature (if any) still recovers to this node's
// CURRENT VAULT_RELEASE_SIGNING_KEY_HEX. Without that check, a
// signature made under an old key that's since been rotated stays
// stuck under this wallet's entry forever - the loop used to only ask
// "did I sign this at all," never "does what I signed still match who
// I am now," so a real approval got permanently wedged (relayed every
// 30s, always failing the vault contract's own ecrecover check) after
// exactly this kind of rotation. Re-signing here only helps if
// applyVaultReleaseApprove (api/vault_lock.go) also allows the
// resulting resubmission to overwrite the stale entry - see that
// function's matching fix.
//
// BTC is deliberately excluded - a materially different signing
// mechanism (buildAndSignBtcVaultRelease reconstructs the exact
// unsigned Bitcoin transaction itself) that doesn't fit this
// digest-based flow.
func scanVaultReleaseApprovalsForAutoSign() {
	walletAddress, walletKey, ok := bridgeValidatorSigningKey()
	if !ok {
		return
	}
	signAddress, signKey, ok := vaultReleaseSigningKey()
	if !ok {
		return
	}
	vaultAddr, ok := vaultContractAddress()
	if !ok {
		return
	}
	signAddr := common.HexToAddress(signAddress)

	for _, approval := range vaultReleaseApprovalStore.Snapshot() {
		if approval.Chain != "ETH" && approval.Chain != "USDC" {
			continue
		}
		if approval.Status != core.VaultReleasePending {
			continue
		}
		if vaultReleaseConfirmationsRemaining(approval) > 0 {
			// Burn not yet buried under VaultReleaseConfirmationDepthBlocks
			// CreditChain blocks - see core.VaultReleaseConfirmationDepthBlocks's
			// doc comment. applyVaultReleaseApprove would reject a
			// submission this early anyway; skipping here avoids a wasted
			// sign-and-submit every 30s until it clears.
			continue
		}
		if !common.IsHexAddress(approval.DestinationExtAddress) {
			log.Println("vault release auto-approve: approval", approval.ID, "has an invalid destination, skipping")
			continue
		}

		var tokenAddr common.Address
		if approval.Chain == "USDC" {
			usdcAddr, err := usdcContractAddress()
			if err != nil {
				log.Println("vault release auto-approve: USDC contract address not configured, skipping", approval.ID)
				continue
			}
			tokenAddr = common.HexToAddress(usdcAddr)
		}

		amount, ok := new(big.Int).SetString(approval.AmountSmallestUnit, 10)
		if !ok {
			log.Println("vault release auto-approve: approval", approval.ID, "has an unparseable amount, skipping")
			continue
		}
		releaseIDBig := new(big.Int).SetUint64(approval.ReleaseID)

		digest, err := buildReleaseApprovalDigestGo(common.HexToAddress(approval.DestinationExtAddress), tokenAddr, amount, releaseIDBig, vaultAddr, expectedEthChainID)
		if err != nil {
			log.Println("vault release auto-approve: failed to build digest for", approval.ID, ":", err)
			continue
		}

		if existing, already := approval.Signatures[walletAddress]; already && signatureMatchesAddress(digest, existing, signAddr) {
			continue
		}

		sig, err := crypto.Sign(digest, signKey)
		if err != nil {
			log.Println("vault release auto-approve: failed to sign digest for", approval.ID, ":", err)
			continue
		}
		// go-ethereum's crypto.Sign returns recovery id 0/1 in the last
		// byte; the contract's ecrecover (and every other signer in this
		// system, see EthTx.signDigest's doc comment) expects 27/28.
		sig[64] += 27

		submitVaultReleaseApprove(walletAddress, walletKey, approval.ID, sig)
		log.Println("vault release auto-approve: signed and submitted approval for", approval.ID, "as", signAddress)
	}
}

// submitVaultReleaseApprove builds and records a genuinely wallet-signed
// TxVaultReleaseApprove directly - no HTTP round-trip needed, this code
// already runs inside the server process, same pattern
// submitVaultLockAttest (vault_eth_watch.go) already established.
func submitVaultReleaseApprove(walletAddress string, walletKey ed25519.PrivateKey, burnTxID string, signature []byte) {
	payload := vaultReleaseApproveData{
		BurnTxId:     burnTxID,
		SignatureHex: hex.EncodeToString(signature),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return
	}

	env := core.SignedEnvelope{
		Address:   walletAddress,
		PublicKey: hex.EncodeToString(walletKey.Public().(ed25519.PublicKey)),
		Timestamp: time.Now().Unix(),
		Payload:   payloadJSON,
	}
	env.Signature = hex.EncodeToString(core.SignMessage(walletKey, env.CanonicalMessage()))

	recordTx(env, walletAddress, "", 0, core.TxVaultReleaseApprove, string(payloadJSON))
}
