package core

// ProtocolManifest is the consensus-committed record of every
// security-critical configuration value that MUST be identical across
// every node applying the same blocks - closes two findings from the
// 2026-08-24 external security review:
//
//   - C-09: admin wallet/multisig config, bridge validator list/
//     threshold, and mainnet mode previously lived ONLY as per-node
//     environment variables, with no cross-node consistency guarantee.
//     Two differently-configured nodes could apply the identical block
//     differently - e.g. disagreeing on whether a bridge release
//     approval had reached "enough" distinct signatures - producing
//     silent state divergence or a permanent chain partition. Nothing
//     about a plain os.Getenv read is replicated or agreed upon; it's
//     just whatever happens to be in that one process's environment.
//   - C-06: there was no single, explicit, fail-closed record of what
//     "activated on mainnet" actually means - individual risk points
//     (wallet recovery, the faucet, USDCTEST) each separately checked
//     mainnetMode() rather than reading one committed source of truth.
//     This manifest is that source of truth.
//
// Mirrors ActivatedProtocolVersion's exact established precedent: a
// plain mutable core-package var, written directly by the one apply-
// time function that confirms a change (applySetProtocolManifest,
// api/apply.go), with its own dedicated ChainSnapshot persistence
// field (api/persistence.go) rather than living in the generic
// GovernanceParamsStore (these are structured, multi-field values, not
// a single uint64).
type ProtocolManifest struct {
	// Set is false until the manifest has ever been committed - the
	// bootstrap state. While false, every consumer (api/admin.go,
	// api/bridge_validator.go, api/mainnet_gate.go) falls back to its
	// own local environment variable exactly as it always has, so a
	// fresh node can bootstrap and cast the FIRST
	// TxSetProtocolManifest using its own local admin config, the same
	// way every admin action already works today. Once Set is true,
	// every node applying blocks derives these values from THIS
	// replicated struct instead of its own local environment - genuine
	// cross-node consistency, not merely a detectable mismatch.
	Set bool

	MainnetMode bool

	AdminWalletAddress     string
	AdminMultisigAddresses []string
	AdminMultisigThreshold int

	BridgeValidatorAddresses []string
	BridgeValidatorThreshold int

	SetAt int64  // tx/block timestamp, never wall-clock - see every other apply-time timestamp in this codebase
	SetBy string // the admin address that committed this version, for auditability
}

// ActiveProtocolManifest is the live, replicated value every consumer
// reads. Zero value (Set: false) is the correct, safe starting state
// for every existing deployment - nothing changes until an admin
// explicitly commits one.
var ActiveProtocolManifest ProtocolManifest
