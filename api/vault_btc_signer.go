package api

import (
	"context"
	"encoding/asn1"
	"fmt"
	"log"
	"os"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/ethereum/go-ethereum/crypto"
)

// BtcVaultSigner is the BTC-side sibling of EthSigner (api/eth_signer.go) -
// same "provision a key, sign a hash, never expose the private key
// outside this implementation" security boundary, but producing a
// plain DER-encoded ECDSA signature rather than a 65-byte recoverable
// one: OP_CHECKMULTISIG's witness verification needs no recovery id
// (unlike ecrecover), so there's nothing to brute-force here - actually
// simpler than the ETH signer for that reason.
type BtcVaultSigner interface {
	// NewKey provisions a fresh secp256k1 keypair and returns a
	// signer-scoped reference plus the COMPRESSED public key, hex-
	// encoded - the exact format core.BridgeValidator.BtcPublicKeyHex
	// expects (api/vault_btc.go's sortedBtcVaultPublicKeys requires
	// 33-byte compressed keys).
	NewKey(ctx context.Context) (keyRef string, compressedPubKeyHex string, err error)
	// Sign produces a DER-encoded ECDSA signature over a pre-computed
	// 32-byte hash - the caller (api/vault_btc_sign.go, C.3/C.4) is
	// responsible for appending the SIGHASH_ALL type byte before
	// placing this into a witness stack.
	Sign(ctx context.Context, keyRef string, sighash [32]byte) (derSig []byte, err error)
}

var btcVaultSigner BtcVaultSigner

// InitBtcVaultSigner mirrors InitEthSigner's degrade-gracefully
// structure exactly, now that the KMS-backed production variant below
// closes the previously-disclosed "not yet implemented" gap (see the
// bridge-vault plan's original Phase C notes, and audit/KNOWN_GAPS.md).
func InitBtcVaultSigner() {
	if os.Getenv("BTC_VAULT_KMS_ENABLED") == "true" {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			log.Println("BTC vault signer: KMS enabled but failed to load AWS config, falling back to local dev signer:", err)
		} else {
			btcVaultSigner = &kmsBtcVaultSigner{client: kms.NewFromConfig(cfg)}
			log.Println("BTC vault signer: AWS KMS (production-grade key custody)")
			return
		}
	}
	btcVaultSigner = newLocalDevBtcVaultSigner()
	log.Println("BTC vault signer: LOCAL DEV FALLBACK - NOT production-grade key custody (see localDevBtcVaultSigner doc comment). Do not let this hold real BTC vault-validator responsibility beyond a developer's own testnet setup.")
}

// --- AWS KMS-backed signer (production) --------------------------------

// kmsBtcVaultSigner mirrors kmsEthSigner (api/eth_signer.go) - same
// secp256k1 KMS key type (Bitcoin and Ethereum use the identical curve),
// same DER-signature parsing/low-S correction, and it deliberately
// reuses eth_signer.go's parseKMSPublicKey/padTo32/ecdsaDERSignature
// helpers rather than duplicating them, since none of that logic is
// actually Ethereum-specific - it's plain secp256k1/KMS plumbing. What's
// genuinely different from the ETH signer: no recovery-id brute-force is
// needed (OP_CHECKMULTISIG's witness verification has no ecrecover
// equivalent, so there's nothing to recover), and the public key comes
// back COMPRESSED (33 bytes) rather than as an Ethereum address, per
// BtcVaultSigner.NewKey's own documented contract.
type kmsBtcVaultSigner struct {
	client *kms.Client
}

func (s *kmsBtcVaultSigner) NewKey(ctx context.Context) (string, string, error) {
	out, err := s.client.CreateKey(ctx, &kms.CreateKeyInput{
		KeySpec:  kmstypes.KeySpecEccSecgP256k1,
		KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		return "", "", fmt.Errorf("kms create key: %w", err)
	}
	keyID := *out.KeyMetadata.KeyId

	pubOut, err := s.client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: &keyID})
	if err != nil {
		return "", "", fmt.Errorf("kms get public key: %w", err)
	}
	pub, err := parseKMSPublicKey(pubOut.PublicKey)
	if err != nil {
		return "", "", err
	}

	compressedHex := fmt.Sprintf("%x", crypto.CompressPubkey(pub))
	return keyID, compressedHex, nil
}

// Sign asks KMS to sign a pre-computed sighash (MessageType: DIGEST,
// same reasoning as kmsEthSigner.Sign - KMS's "ECDSA_SHA_256" algorithm
// name would otherwise re-hash with SHA-256 first, which is wrong for a
// hash Bitcoin's own sighash algorithm already produced), then re-encodes
// the result through this codebase's own btcec/ecdsa Signature type
// (rather than returning KMS's raw DER bytes unmodified) so a KMS-backed
// and a local-dev-signed release are byte-for-byte identical in shape -
// necessary anyway since the low-S correction below changes S, requiring
// fresh DER encoding regardless.
func (s *kmsBtcVaultSigner) Sign(ctx context.Context, keyID string, sighash [32]byte) ([]byte, error) {
	signOut, err := s.client.Sign(ctx, &kms.SignInput{
		KeyId:            &keyID,
		Message:          sighash[:],
		MessageType:      kmstypes.MessageTypeDigest,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecEcdsaSha256,
	})
	if err != nil {
		return nil, fmt.Errorf("kms sign: %w", err)
	}

	var derSig ecdsaDERSignature
	if _, err := asn1.Unmarshal(signOut.Signature, &derSig); err != nil {
		return nil, fmt.Errorf("parse KMS signature DER: %w", err)
	}

	var rScalar, sScalar btcec.ModNScalar
	rScalar.SetByteSlice(padTo32(derSig.R.Bytes()))
	sScalar.SetByteSlice(padTo32(derSig.S.Bytes()))

	// Bitcoin also requires low-S signatures (BIP62/BIP146 standardness) -
	// the same malleability-prevention rule Ethereum's EIP-2 enforces,
	// same fix shape as kmsEthSigner.Sign's own low-S correction.
	if sScalar.IsOverHalfOrder() {
		sScalar.Negate()
	}

	return ecdsa.NewSignature(&rScalar, &sScalar).Serialize(), nil
}

// localDevBtcVaultSigner mirrors localDevEthSigner exactly: real
// secp256k1 keys generated and held only in this process's own memory,
// lost on restart. Exists purely so Phase C is testable end-to-end
// (address construction, sighash computation, witness assembly, testnet
// deposit/release) without requiring AWS KMS support for this key type
// to be built first.
type localDevBtcVaultSigner struct {
	mu   sync.Mutex
	keys map[string]*btcec.PrivateKey
}

func newLocalDevBtcVaultSigner() *localDevBtcVaultSigner {
	return &localDevBtcVaultSigner{keys: make(map[string]*btcec.PrivateKey)}
}

func (s *localDevBtcVaultSigner) NewKey(ctx context.Context) (string, string, error) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		return "", "", fmt.Errorf("local dev BTC vault signer: generate key: %w", err)
	}
	pubHex := fmt.Sprintf("%x", priv.PubKey().SerializeCompressed())
	keyRef := "localdev-btcvault-" + pubHex

	s.mu.Lock()
	s.keys[keyRef] = priv
	s.mu.Unlock()

	return keyRef, pubHex, nil
}

func (s *localDevBtcVaultSigner) Sign(ctx context.Context, keyRef string, sighash [32]byte) ([]byte, error) {
	s.mu.Lock()
	priv, ok := s.keys[keyRef]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("local dev BTC vault signer: unknown key %s (this signer's keys live only in process memory and do not survive a restart)", keyRef)
	}
	sig := ecdsa.Sign(priv, sighash[:])
	return sig.Serialize(), nil
}
