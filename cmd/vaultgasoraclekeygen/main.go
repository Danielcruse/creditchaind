// vaultgasoraclekeygen generates a fresh CreditChain wallet keypair
// (the same Ed25519 format core.GenerateKeyPair produces) and writes
// ONLY the private key into a new systemd drop-in for
// creditchain-mainnet.service, granting it the narrow
// TxVaultGasPriceOracleUpdate role - see
// api/vault_gas_price_oracle.go's doc comment. The private key is
// never printed - only the derived public address, which is safe to
// share (see core.GenerateAddress). Run with sudo (writes under
// /etc/systemd/system).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

const targetPath = "/etc/systemd/system/creditchain-mainnet.service.d/vault-gas-price-oracle-key.conf"

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "key generation failed:", err)
		os.Exit(1)
	}

	hash := sha256.Sum256(pub)
	address := hex.EncodeToString(hash[:20])
	privHex := hex.EncodeToString(priv)

	content := fmt.Sprintf(
		"[Service]\n\nEnvironment=VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX=%s\nEnvironment=VAULT_GAS_PRICE_ORACLE_ADDRESS=cc%s\n",
		privHex, address,
	)

	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "could not write", targetPath, "- are you running with sudo?", err)
		os.Exit(1)
	}

	fmt.Println("Wrote", targetPath)
	fmt.Println("Oracle wallet address (safe to share, this is NOT the private key):", "cc"+address)
	fmt.Println("Next: sudo systemctl daemon-reload && sudo systemctl restart creditchain-mainnet")
}
