package core

import (
	"encoding/json"
	"os"
)

// ActivatedProtocolVersion is the "ship dormant, activate via
// governance" flag - see api/apply.go's processProposalFinalization
// VERSION_ACTIVATION branch, the only writer. Empty means no
// governance-gated version has ever been activated. This does NOT
// prevent an operator from deploying new code (impossible for an
// off-chain binary swap) - it only gives future consensus-critical
// code a real, governance-approved switch to check before turning
// itself on, e.g. `if core.ActivatedProtocolVersion >= "v2" { ... }`.
var ActivatedProtocolVersion string

type Config struct {
	ChainID uint64 `json:"chainId"`

	Network string `json:"network"`

	Version string `json:"version"`

	Port string `json:"port"`

	NodeID string `json:"nodeId"`

	// Peers are other nodes' HTTP base URLs (e.g.
	// "http://localhost:8082") this node dials on startup to join the
	// network. Can be overridden with the PEERS env var
	// (comma-separated) so multiple local node processes can be
	// wired together without editing files.
	Peers []string `json:"peers"`
}

func LoadConfig(
	path string,
) (*Config, error) {

	data, err :=
		os.ReadFile(path)

	if err != nil {
		return nil, err
	}

	var cfg Config

	err =
		json.Unmarshal(
			data,
			&cfg,
		)

	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
