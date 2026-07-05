package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
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
			"version": config.Version,
			"network": config.Network,
			"chainId": config.ChainID,
			"blockHeight": len(
				blockchain.Blocks,
			),
		},
	)
}