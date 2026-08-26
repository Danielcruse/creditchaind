// relayerkeygen generates a fresh secp256k1 keypair for the vault
// release relay (RELAYER_PRIVATE_KEY_HEX - see api/vault_release_relay.go)
// and writes ONLY the private key into /etc/creditchain/relayer.env,
// overwriting whatever was there before - the same file path the
// system was originally documented to use. Also rewrites the
// relayer-key.conf systemd drop-in to a clean EnvironmentFile
// reference instead of an inline Environment= value, matching the
// bridge_validator.env/vault_release_signing.env pattern already in
// use elsewhere. The private key is never printed - only the derived
// address, which is safe to share. Run with sudo (writes under
// /etc/creditchain and /etc/systemd/system).
package main

import (
	"crypto/rand"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/crypto"
)

const (
	envPath    = "/etc/creditchain/relayer.env"
	dropinPath = "/etc/systemd/system/creditchain-mainnet.service.d/relayer-key.conf"
)

func main() {
	key, err := crypto.GenerateKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "key generation failed:", err)
		os.Exit(1)
	}
	_ = rand.Reader // crypto.GenerateKey already reads from crypto/rand internally

	privHex := fmt.Sprintf("%064x", key.D)
	address := crypto.PubkeyToAddress(key.PublicKey).Hex()

	envContent := fmt.Sprintf("RELAYER_PRIVATE_KEY_HEX=%s\n", privHex)
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "could not write", envPath, "- are you running with sudo?", err)
		os.Exit(1)
	}

	dropinContent := fmt.Sprintf("[Service]\n\nEnvironmentFile=%s\n", envPath)
	if err := os.WriteFile(dropinPath, []byte(dropinContent), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "could not write", dropinPath, "- are you running with sudo?", err)
		os.Exit(1)
	}

	fmt.Println("Wrote", envPath, "(private key, mode 0600)")
	fmt.Println("Wrote", dropinPath, "(now references the env file above, no more inline value)")
	fmt.Println()
	fmt.Println("New relayer address (safe to share, this is NOT the private key):", address)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Fund this new address with a small amount of real ETH for gas")
	fmt.Println("  2. sudo systemctl daemon-reload && sudo systemctl restart creditchain-mainnet")
	fmt.Println("  3. Sweep any remaining ETH out of the OLD relayer address (0x5E6dde9078441C893007beC7826FF4A117473A90) - its key is compromised")
}
