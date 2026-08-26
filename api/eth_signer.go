package api

import (
	"context"
	"crypto/ecdsa"
	"encoding/asn1"
	"fmt"
	"log"
	"math/big"
	"os"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// EthSigner is anything that can provision a secp256k1 signing key and
// produce a 65-byte recoverable ECDSA signature ([R(32) || S(32) || V(1)])
// over a 32-byte hash, without ever exposing the underlying private key
// outside its own implementation. This is the entire security boundary
// for the ETH custodial corner - see audit/THREAT_MODEL.md's custodial-
// key entry and core/eth_custodial.go's doc comment.
type EthSigner interface {
	NewKey(ctx context.Context) (keyRef string, ethAddress string, err error)
	Sign(ctx context.Context, keyRef string, hash [32]byte) (sig []byte, err error)
}

var ethSigner EthSigner

// InitEthSigner picks the real KMS-backed signer if explicitly enabled
// and reachable, otherwise falls back to a local, clearly-labeled
// dev-only signer - same "degrade gracefully rather than crash" pattern
// as db.go's InitDB, except here "not configured" means "less secure,"
// not merely "less functional," which is exactly why every place this
// matters (UI labeling, ETH_CUSTODIAL_SETUP.md) must make the active
// signer mode obvious rather than silently accepting the fallback.
func InitEthSigner() {
	if os.Getenv("ETH_CUSTODIAL_KMS_ENABLED") == "true" {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			log.Println("ETH custodial: KMS enabled but failed to load AWS config, falling back to local dev signer:", err)
		} else {
			ethSigner = &kmsEthSigner{client: kms.NewFromConfig(cfg)}
			log.Println("ETH custodial signer: AWS KMS (production-grade key custody)")
			return
		}
	}
	ethSigner = newLocalDevEthSigner()
	log.Println("ETH custodial signer: LOCAL DEV FALLBACK - NOT production-grade key custody (see localDevEthSigner doc comment). Set ETH_CUSTODIAL_KMS_ENABLED=true with real AWS credentials before this handles anything beyond a developer's own test account.")
}

// --- AWS KMS-backed signer (production) -------------------------------

type kmsEthSigner struct {
	client *kms.Client
}

// pkixPublicKeyInfo is a minimal ASN.1 SubjectPublicKeyInfo shape -
// deliberately not using crypto/x509's own PKIX parser, since Go's
// standard library doesn't recognize the secp256k1 curve (only the NIST
// curves), so x509.ParsePKIXPublicKey would fail on exactly the key type
// this signer needs. Parsing the raw structure ourselves and handing the
// bit-string bytes to go-ethereum's own secp256k1-aware unmarshaler
// avoids that gap entirely.
type pkixPublicKeyInfo struct {
	Algorithm asn1.RawValue
	PublicKey asn1.BitString
}

func parseKMSPublicKey(der []byte) (*ecdsa.PublicKey, error) {
	var info pkixPublicKeyInfo
	if _, err := asn1.Unmarshal(der, &info); err != nil {
		return nil, fmt.Errorf("parse KMS public key DER: %w", err)
	}
	pub, err := crypto.UnmarshalPubkey(info.PublicKey.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unmarshal secp256k1 point from KMS public key: %w", err)
	}
	return pub, nil
}

// ecdsaDERSignature mirrors the ASN.1 ECDSA-Sig-Value structure KMS
// returns from Sign - SEQUENCE { r INTEGER, s INTEGER }.
type ecdsaDERSignature struct {
	R, S *big.Int
}

func padTo32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func (s *kmsEthSigner) NewKey(ctx context.Context) (string, string, error) {
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

	return keyID, crypto.PubkeyToAddress(*pub).Hex(), nil
}

// Sign asks KMS to sign a pre-computed digest (MessageType: DIGEST) -
// Ethereum's own transaction-signing hash is Keccak256, not the SHA-256
// KMS's "ECDSA_SHA_256" algorithm name implies; passing MessageType
// DIGEST tells KMS to sign exactly the 32 bytes given rather than
// re-hashing a message with SHA-256 first, which is the standard,
// documented way to use KMS for Ethereum signing.
//
// KMS never returns a recovery id (the "which of two possible public
// keys does this signature recover to" byte Ethereum's signature format
// requires) - it's brute-forced here by trying both possibilities
// against the public key independently re-fetched from KMS itself for
// this key (never trusted from a caller-supplied address).
func (s *kmsEthSigner) Sign(ctx context.Context, keyID string, hash [32]byte) ([]byte, error) {
	signOut, err := s.client.Sign(ctx, &kms.SignInput{
		KeyId:            &keyID,
		Message:          hash[:],
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

	// Ethereum requires "low-S" signatures (EIP-2 malleability
	// protection) - flip s to its low-S equivalent if needed.
	secp256k1N := crypto.S256().Params().N
	halfN := new(big.Int).Rsh(secp256k1N, 1)
	sVal := derSig.S
	if sVal.Cmp(halfN) > 0 {
		sVal = new(big.Int).Sub(secp256k1N, sVal)
	}

	pubOut, err := s.client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: &keyID})
	if err != nil {
		return nil, fmt.Errorf("kms get public key for recovery-id check: %w", err)
	}
	wantPub, err := parseKMSPublicKey(pubOut.PublicKey)
	if err != nil {
		return nil, err
	}
	wantAddr := crypto.PubkeyToAddress(*wantPub)

	rBytes := padTo32(derSig.R.Bytes())
	sBytes := padTo32(sVal.Bytes())

	for recID := byte(0); recID < 2; recID++ {
		candidate := make([]byte, 0, 65)
		candidate = append(candidate, rBytes...)
		candidate = append(candidate, sBytes...)
		candidate = append(candidate, recID)

		recoveredPub, err := crypto.SigToPub(hash[:], candidate)
		if err != nil {
			continue
		}
		if crypto.PubkeyToAddress(*recoveredPub) == wantAddr {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("could not determine recovery id for KMS signature from key %s", keyID)
}

// --- Local dev-only signer (fallback) ----------------------------------

// localDevEthSigner generates and holds REAL secp256k1 private keys
// directly in this process's own memory. This is NOT production-grade
// key custody - there is no HSM/KMS boundary, and keys are lost on
// restart (a fresh account must be re-created; any prior local-dev
// custodial address becomes permanently unusable, which is an acceptable
// limitation for something that only ever holds disclosed, worthless
// Sepolia testnet ETH). It exists purely so the ETH-custodial feature is
// testable end-to-end without requiring AWS KMS to be provisioned first.
//
// NEVER let this be the active signer once this feature handles anything
// beyond a developer's own throwaway test account - see
// ops/ETH_CUSTODIAL_SETUP.md and audit/KNOWN_GAPS.md. InitEthSigner logs
// loudly which mode is active specifically so this is never silently the
// case in a real deployment.
type localDevEthSigner struct {
	mu   sync.Mutex
	keys map[string]*ecdsa.PrivateKey
}

func newLocalDevEthSigner() *localDevEthSigner {
	return &localDevEthSigner{keys: make(map[string]*ecdsa.PrivateKey)}
}

func (s *localDevEthSigner) NewKey(ctx context.Context) (string, string, error) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		return "", "", err
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	keyID := "localdev-" + addr.Hex()

	s.mu.Lock()
	s.keys[keyID] = priv
	s.mu.Unlock()

	return keyID, addr.Hex(), nil
}

func (s *localDevEthSigner) Sign(ctx context.Context, keyID string, hash [32]byte) ([]byte, error) {
	s.mu.Lock()
	priv, ok := s.keys[keyID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("local dev signer: unknown key %s (this signer's keys live only in process memory and do not survive a restart)", keyID)
	}
	return crypto.Sign(hash[:], priv)
}
