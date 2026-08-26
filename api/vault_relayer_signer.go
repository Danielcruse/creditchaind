package api

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/ethereum/go-ethereum/crypto"
)

// This file closes the "relayer key still a bare env var" follow-up
// flagged after the gas-price-oracle key got the same treatment (see
// audit/KNOWN_GAPS.md's Custody / Key Management section) - mirrors
// api/vault_gas_price_oracle_signer.go's exact shape, but for a
// secp256k1 key, so it reuses api/eth_signer.go's existing KMS
// machinery (kmsSecp256k1KeyAddress, the ECC_SECG_P256K1 key spec)
// directly rather than duplicating it.
//
// relayerSigner is deliberately narrower than the full EthSigner
// interface (api/eth_signer.go) - no NewKey, since the relayer is one
// fixed, operator-provisioned key, not "create a new key per user."
type relayerSigner interface {
	address(ctx context.Context) (string, error)
	sign(ctx context.Context, hash [32]byte) ([]byte, error)
}

// resolveRelayerSigner prefers AWS KMS (RELAYER_KMS_ENABLED=true +
// RELAYER_KMS_KEY_ID naming a real ECC_SECG_P256K1 key), falling back
// to the original RELAYER_PRIVATE_KEY_HEX raw-hex env var - same
// "prefer real custody, degrade gracefully, never crash" pattern
// InitEthSigner and resolveVaultGasPriceOracleSigner both already
// establish. Called fresh each time, not cached, so a KMS
// misconfiguration surfaces as "the relay doesn't run" rather than a
// boot-time crash.
func resolveRelayerSigner() (relayerSigner, bool) {
	if os.Getenv("RELAYER_KMS_ENABLED") == "true" {
		keyID := strings.TrimSpace(os.Getenv("RELAYER_KMS_KEY_ID"))
		if keyID == "" {
			log.Println("RELAYER_KMS_ENABLED is true but RELAYER_KMS_KEY_ID is unset - falling back to RELAYER_PRIVATE_KEY_HEX")
		} else if cfg, err := awsconfig.LoadDefaultConfig(context.Background()); err != nil {
			log.Println("relayer: KMS enabled but failed to load AWS config, falling back:", err)
		} else {
			return &kmsRelayerSigner{client: kms.NewFromConfig(cfg), keyID: keyID}, true
		}
	}

	raw := strings.TrimSpace(os.Getenv("RELAYER_PRIVATE_KEY_HEX"))
	if raw == "" {
		return nil, false
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(raw, "0x"))
	if err != nil {
		log.Println("RELAYER_PRIVATE_KEY_HEX is set but not a valid hex-encoded secp256k1 private key - the vault release relay will not run")
		return nil, false
	}
	return &localHexRelayerSigner{priv: key}, true
}

// --- AWS KMS-backed signer (production) -------------------------------

type kmsRelayerSigner struct {
	client *kms.Client
	keyID  string
}

func (s *kmsRelayerSigner) address(ctx context.Context) (string, error) {
	return kmsSecp256k1KeyAddress(ctx, s.client, s.keyID)
}

// sign delegates to kmsEthSigner.Sign (api/eth_signer.go) - identical
// ECDSA-Sig-Value DER unwrapping, low-S normalization, and recovery-id
// search, this just supplies a different (operator-chosen, pre-
// existing) key ID instead of one ethSigner itself provisioned via
// NewKey for a custodial account.
func (s *kmsRelayerSigner) sign(ctx context.Context, hash [32]byte) ([]byte, error) {
	signer := &kmsEthSigner{client: s.client}
	return signer.Sign(ctx, s.keyID, hash)
}

// --- Local / operator-managed raw-key signer (existing behavior) ------

// localHexRelayerSigner wraps the exact pre-existing behavior (a raw
// secp256k1 key read from RELAYER_PRIVATE_KEY_HEX) behind the same
// relayerSigner interface, so every call site only ever talks to the
// interface, never cares which one it's actually using.
type localHexRelayerSigner struct {
	priv *ecdsa.PrivateKey
}

func (s *localHexRelayerSigner) address(ctx context.Context) (string, error) {
	return crypto.PubkeyToAddress(s.priv.PublicKey).Hex(), nil
}

func (s *localHexRelayerSigner) sign(ctx context.Context, hash [32]byte) ([]byte, error) {
	sig, err := crypto.Sign(hash[:], s.priv)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return sig, nil
}
