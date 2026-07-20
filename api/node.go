package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

func HandleNode(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	json.NewEncoder(w).Encode(
		map[string]any{
			"nodeId": config.NodeID,
			// nodePublicKey lets a client explicitly sign which node
			// it's delegating block-production to when registering as
			// a validator (see api/validators.go's HandleRegisterValidator) -
			// without this, the delegation is just something the
			// receiving node asserts about itself, unsigned and
			// therefore tamperable in transit by a malicious producer.
			"nodePublicKey": hex.EncodeToString(nodeIdentity.PublicKey),
			"version":       config.Version,
			// activatedProtocolVersion is the governance-gated feature-
			// flag flip - see core.ActivatedProtocolVersion's doc
			// comment. Empty string means nothing has ever been
			// activated via a VERSION_ACTIVATION proposal.
			"activatedProtocolVersion": core.ActivatedProtocolVersion,
			"network":                  config.Network,
			"chainId":                  config.ChainID,
			"blockHeight": len(
				blockchain.Blocks,
			),
		},
	)
}
