package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// awsCredentialsFilePath is where the ETH and BTC custodial KMS
// signers' real AWS IAM credentials get written - deliberately a plain
// server-local file, never blockchain state. Both signers use the
// identical secp256k1 KMS key type and the same three API calls
// (CreateKey/GetPublicKey/Sign - see api/eth_signer.go and
// api/vault_btc_signer.go), so one IAM credential legitimately
// activates both; there is no real reason an operator would want ETH
// on production KMS custody while BTC stays on the insecure in-memory
// fallback. Routing this through the usual
// recordAdminTx/multisig-proposal pipeline (like TxAdminSetParam)
// would mean embedding the raw secret in a transaction's Data field,
// which becomes permanent, replicated, explorer-visible on-chain
// history - strictly worse than a single-admin-authenticated direct
// write. That's a deliberate tradeoff: this action is reachable by
// any one configured admin/multisig signer alone (see
// requireAdminAuthed), not gated behind a second approval the way
// fund-moving admin actions are - because the alternative (2-of-3 via
// the chain) would leak the secret itself, not just the decision to
// set one.
var awsCredentialsFilePath = envOrDefault("AWS_CREDENTIALS_FILE", "/opt/creditchain-mainnet/.env.aws")

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type adminSetAWSCredentialsData struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	Region          string `json:"region"`
}

// HandleAdminSetAWSCredentials lets an admin submit real AWS IAM
// credentials for the ETH AND BTC custodial KMS signers directly
// through the site - the value is write-only from here on: no endpoint
// anywhere echoes it back, and it's never logged. Writing the file
// alone doesn't activate anything - the running process already has
// its environment fixed at the OS level from the moment it started, so
// this only takes effect once the service is restarted and re-reads
// its environment (see ops/NODE_OPERATOR.md's EnvironmentFile= line).
func HandleAdminSetAWSCredentials(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req adminSetAWSCredentialsData
	if _, _, err := requireAdminAuthed(r, &req); err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if req.AccessKeyID == "" || req.SecretAccessKey == "" || req.Region == "" {
		http.Error(w, "accessKeyId, secretAccessKey and region are all required", http.StatusBadRequest)
		return
	}

	content := fmt.Sprintf(
		"AWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_REGION=%s\nETH_CUSTODIAL_KMS_ENABLED=true\nBTC_VAULT_KMS_ENABLED=true\n",
		req.AccessKeyID, req.SecretAccessKey, req.Region,
	)
	// 0600 - readable/writable only by the file's owner (the same
	// user the creditchaind process runs as), never world/group
	// readable.
	if err := os.WriteFile(awsCredentialsFilePath, []byte(content), 0600); err != nil {
		http.Error(w, "could not save credentials on the server", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status": "saved",
		"note":   "Saved on the server. This does not take effect until the service is restarted - let your operator know it's ready to restart.",
	})
}
