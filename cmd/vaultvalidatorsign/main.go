// vaultvalidatorsign is a one-off operator tool for running the
// validator-application/staking/registration process by hand, stage by
// stage, without hand-crafting signed envelopes. Reuses this codebase's
// own core.SignedEnvelope/core.SignMessage/core.GenerateAddress exactly
// as every other client does - no separate crypto implementation.
//
// Deliberately NOT wired into the main server or committed as
// permanent tooling - a narrow, temporary CLI for one wallet operator
// to run themselves, matching this session's cmd/blockforge and
// cmd/regforge precedent (built, used, then deleted). Never reads a
// key over the network and never sends the key anywhere - it only ever
// signs locally and POSTs the resulting signed envelope, the same
// shape a browser's apiPostSigned produces.
//
// Usage (run ON the box that has the key file, or point -keyfile at a
// local copy):
//
//	vaultvalidatorsign -keyfile /etc/creditchain/bridge_validator.env create
//	vaultvalidatorsign -keyfile /path/to/supporter.env support <applicantAddress>
//	vaultvalidatorsign -keyfile /etc/creditchain/bridge_validator.env confirm <token>
//	vaultvalidatorsign -keyfile /etc/creditchain/bridge_validator.env stake <amountCCT>
//	vaultvalidatorsign -keyfile /etc/creditchain/bridge_validator.env register
//
// -keyfile parses a systemd EnvironmentFile-style KEY=value file and
// reads -keyname (default BRIDGE_VALIDATOR_PRIVATE_KEY_HEX) from it.
// -keyhex takes the hex key directly instead, for a supporting wallet
// whose key isn't sitting in that specific file.
package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type signedEnvelope struct {
	Address   string          `json:"address"`
	PublicKey string          `json:"publicKey"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func canonicalMessage(e signedEnvelope) []byte {
	msg := e.Address + "|" + e.PublicKey + "|" + strconv.FormatInt(e.Timestamp, 10) + "|" + string(e.Payload)
	return []byte(msg)
}

func generateAddress(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:20])
}

func loadKeyFromFile(path, keyName string) (ed25519.PrivateKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || parts[0] != keyName {
			continue
		}
		return decodeHexKey(parts[1])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%s not found in %s", keyName, path)
}

func decodeHexKey(raw string) (ed25519.PrivateKey, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("not valid hex: %w", err)
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("wrong length: got %d bytes, want %d", len(decoded), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(decoded), nil
}

func sign(priv ed25519.PrivateKey, payload any) (signedEnvelope, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return signedEnvelope{}, err
	}
	pub := priv.Public().(ed25519.PublicKey)
	env := signedEnvelope{
		Address:   generateAddress(pub),
		PublicKey: hex.EncodeToString(pub),
		Timestamp: time.Now().Unix(),
		Payload:   payloadJSON,
	}
	env.Signature = hex.EncodeToString(ed25519.Sign(priv, canonicalMessage(env)))
	return env, nil
}

func postSigned(client *http.Client, baseURL, path string, priv ed25519.PrivateKey, payload any) error {
	env, err := sign(priv, payload)
	if err != nil {
		return err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	resp, err := client.Post(baseURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	fmt.Printf("%s %s -> %s\n%s\n", "POST", path, resp.Status, respBody)
	return nil
}

func getJSON(client *http.Client, baseURL, path string) (map[string]any, error) {
	resp, err := client.Get(baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func main() {
	keyfile := flag.String("keyfile", "", "path to a systemd EnvironmentFile-style KEY=value file containing the signing key")
	keyname := flag.String("keyname", "BRIDGE_VALIDATOR_PRIVATE_KEY_HEX", "variable name to read from -keyfile")
	keyhex := flag.String("keyhex", "", "hex-encoded Ed25519 private key directly, instead of -keyfile")
	baseURL := flag.String("url", "https://localhost:9090", "API base URL - default assumes running ON the mainnet box itself")
	insecure := flag.Bool("insecure-skip-verify", true, "skip TLS cert verification (mainnet's cert is self-signed for localhost)")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Println("usage: vaultvalidatorsign [-keyfile PATH | -keyhex HEX] [-url BASEURL] <create|support ADDR|confirm TOKEN|stake AMOUNT|register>")
		os.Exit(1)
	}
	stage := args[0]

	var priv ed25519.PrivateKey
	var err error
	switch {
	case *keyhex != "":
		priv, err = decodeHexKey(*keyhex)
	case *keyfile != "":
		priv, err = loadKeyFromFile(*keyfile, *keyname)
	default:
		fmt.Println("must pass either -keyfile or -keyhex")
		os.Exit(1)
	}
	if err != nil {
		fmt.Println("failed to load key:", err)
		os.Exit(1)
	}
	pub := priv.Public().(ed25519.PublicKey)
	addr := generateAddress(pub)
	fmt.Println("signing as address:", addr)

	client := &http.Client{Timeout: 15 * time.Second}
	if *insecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}

	switch stage {
	case "create":
		err = postSigned(client, *baseURL, "/validator-application/create", priv, struct{}{})
	case "support":
		if len(args) < 2 {
			fmt.Println("usage: support <applicantAddress>")
			os.Exit(1)
		}
		err = postSigned(client, *baseURL, "/validator-application/support", priv, map[string]string{"address": args[1]})
	case "confirm":
		if len(args) < 2 {
			fmt.Println("usage: confirm <token>")
			os.Exit(1)
		}
		err = postSigned(client, *baseURL, "/validator-application/confirm", priv, map[string]string{"token": args[1]})
	case "stake":
		if len(args) < 2 {
			fmt.Println("usage: stake <amountCCT>")
			os.Exit(1)
		}
		amount, perr := strconv.ParseUint(args[1], 10, 64)
		if perr != nil {
			fmt.Println("invalid amount:", perr)
			os.Exit(1)
		}
		err = postSigned(client, *baseURL, "/stake", priv, map[string]any{"Amount": amount, "Symbol": "CCT"})
	case "register":
		node, gerr := getJSON(client, *baseURL, "/node")
		if gerr != nil {
			fmt.Println("failed to fetch /node:", gerr)
			os.Exit(1)
		}
		nodeID, _ := node["nodeId"].(string)
		nodePub, _ := node["nodePublicKey"].(string)
		fmt.Println("delegating to node:", nodeID, nodePub)
		err = postSigned(client, *baseURL, "/validators/register", priv, map[string]string{"nodeId": nodeID, "nodePublicKey": nodePub})
	default:
		fmt.Println("unknown stage:", stage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Println("request failed:", err)
		os.Exit(1)
	}
}
