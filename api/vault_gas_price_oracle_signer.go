package api

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/creditchain/creditchaind/core"
)

// This file closes part of the "key custody is still bare env vars"
// gap flagged as follow-up work after the security audit remediation
// (audit/KNOWN_GAPS.md) - mirrors EthSigner's exact design
// (api/eth_signer.go) for the gas-price-oracle key specifically, which
// this operator's own server actually holds and signs with (unlike
// BRIDGE_VALIDATOR_PRIVATE_KEY_HEX, which is deliberately each bridge
// validator's own EXTERNAL signing infrastructure per Phase A's
// design - hardening THIS server's copy of that key wouldn't even be
// the right fix, since the whole point of an M-of-N validator set is
// that no single operator's custody failure is fatal). The relayer key
// (RELAYER_PRIVATE_KEY_HEX) has 6 call sites across 4 files and is
// already the lowest-risk of the three by design (gas-only, no fund
// authority, permissionless to relay at all - see
// vault_release_relay.go's own doc comment) - extending this same
// pattern there is real, valuable follow-up work, deliberately not
// attempted in this same pass to avoid a rushed touch of live,
// multi-call-site fund-relay code.
//
// vaultOracleSigner is deliberately narrower than EthSigner: the gas
// oracle key is one fixed, operator-provisioned key (not "create a new
// key per user"), so there's no NewKey - only resolving the one
// configured identity and signing with it.
type vaultOracleSigner interface {
	identity(ctx context.Context) (address, publicKeyHex string, err error)
	sign(ctx context.Context, message []byte) (sig []byte, err error)
}

// resolveVaultGasPriceOracleSigner prefers AWS KMS (if
// VAULT_GAS_PRICE_ORACLE_KMS_ENABLED=true and
// VAULT_GAS_PRICE_ORACLE_KMS_KEY_ID names a real KMS Ed25519 key),
// falling back to the original VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX
// raw-hex env var - same "prefer real custody, degrade gracefully,
// never crash" pattern InitEthSigner established for the ETH
// custodial corner. Called fresh each time (not cached at startup)
// so a KMS misconfiguration surfaces as "the loop doesn't run" (same
// as an invalid hex key today) rather than a boot-time crash.
func resolveVaultGasPriceOracleSigner() (vaultOracleSigner, bool) {
	if os.Getenv("VAULT_GAS_PRICE_ORACLE_KMS_ENABLED") == "true" {
		keyID := strings.TrimSpace(os.Getenv("VAULT_GAS_PRICE_ORACLE_KMS_KEY_ID"))
		if keyID == "" {
			log.Println("VAULT_GAS_PRICE_ORACLE_KMS_ENABLED is true but VAULT_GAS_PRICE_ORACLE_KMS_KEY_ID is unset - falling back to VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX")
		} else if cfg, err := awsconfig.LoadDefaultConfig(context.Background()); err != nil {
			log.Println("gas price oracle: KMS enabled but failed to load AWS config, falling back:", err)
		} else {
			return &kmsEd25519OracleSigner{client: kms.NewFromConfig(cfg), keyID: keyID}, true
		}
	}

	raw := os.Getenv("VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX")
	if raw == "" {
		return nil, false
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		log.Println("VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX is set but not a valid 64-byte hex-encoded Ed25519 private key - the gas price oracle loop will not run")
		return nil, false
	}
	return &localHexEd25519OracleSigner{priv: ed25519.PrivateKey(decoded)}, true
}

// --- AWS KMS-backed signer (production) -------------------------------

type kmsEd25519OracleSigner struct {
	client *kms.Client
	keyID  string
}

func (s *kmsEd25519OracleSigner) identity(ctx context.Context) (string, string, error) {
	out, err := s.client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: &s.keyID})
	if err != nil {
		return "", "", fmt.Errorf("kms get public key: %w", err)
	}
	// Unlike secp256k1 (eth_signer.go's parseKMSPublicKey, which needs a
	// hand-rolled ASN.1 parser since Go's x509 package doesn't know that
	// curve), Ed25519 is a curve x509.ParsePKIXPublicKey recognizes
	// natively.
	pub, err := x509.ParsePKIXPublicKey(out.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("parse KMS Ed25519 public key: %w", err)
	}
	edPub, ok := pub.(ed25519.PublicKey)
	if !ok {
		return "", "", fmt.Errorf("KMS key %s is not an Ed25519 key (got %T)", s.keyID, pub)
	}
	return core.GenerateAddress(edPub), hex.EncodeToString(edPub), nil
}

// sign asks KMS to sign the RAW message directly (MessageType: RAW,
// SigningAlgorithmSpecEd25519Sha512) - unlike ECDSA, Ed25519 can't be
// split into "hash first, then sign the hash": its signature
// deterministically incorporates the full message, so there is no
// DIGEST mode for this algorithm. KMS returns the raw 64-byte
// signature directly - no ASN.1/DER unwrapping needed (unlike
// kmsEthSigner.Sign's ECDSA-Sig-Value parsing), and no recovery-id
// search either (Ed25519 signatures don't have one).
func (s *kmsEd25519OracleSigner) sign(ctx context.Context, message []byte) ([]byte, error) {
	out, err := s.client.Sign(ctx, &kms.SignInput{
		KeyId:            &s.keyID,
		Message:          message,
		MessageType:      kmstypes.MessageTypeRaw,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecEd25519Sha512,
	})
	if err != nil {
		return nil, fmt.Errorf("kms sign: %w", err)
	}
	return out.Signature, nil
}

// --- Local dev / operator-managed raw-key signer (existing behavior) --

// localHexEd25519OracleSigner wraps the exact pre-existing behavior
// (a raw Ed25519 key read from VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX)
// behind the same vaultOracleSigner interface, so submitVaultGasPriceOracleUpdate
// never has to care which one it's actually talking to.
type localHexEd25519OracleSigner struct {
	priv ed25519.PrivateKey
}

func (s *localHexEd25519OracleSigner) identity(ctx context.Context) (string, string, error) {
	pub := s.priv.Public().(ed25519.PublicKey)
	return core.GenerateAddress(pub), hex.EncodeToString(pub), nil
}

func (s *localHexEd25519OracleSigner) sign(ctx context.Context, message []byte) ([]byte, error) {
	return core.SignMessage(s.priv, message), nil
}
