package core

// ProposalStatus is only meaningful once Open flips false - see
// api/apply.go's processProposalFinalization, the sole writer. Open
// itself is kept unchanged (still true for the entire voting window)
// for backward compatibility with anything reading it as "still
// voting."
type ProposalStatus string

const (
	ProposalStatusOpen   ProposalStatus = ""
	ProposalStatusPassed ProposalStatus = "PASSED"
	ProposalStatusFailed ProposalStatus = "FAILED"
)

// ProposalKind distinguishes a plain/informational vote from the two
// kinds that actually change protocol state on PASSED - see
// api/apply.go's processProposalFinalization. Judgment call: kept as
// a plain string (not an enum with compile-time-checked cases) so a
// future kind can be added without touching every switch that already
// handles the existing two.
type ProposalKind string

const (
	ProposalKindPlain             ProposalKind = ""
	ProposalKindParamChange       ProposalKind = "PARAM_CHANGE"
	ProposalKindVersionActivation ProposalKind = "VERSION_ACTIVATION"
	ProposalKindTokenCreation     ProposalKind = "TOKEN_CREATION"
	// ProposalKindPrediction is a public Yes/No prediction about a
	// price target (see PredictionTargetSymbol/PredictionTargetPrice
	// below) that anyone can optionally back with RC - see
	// core/prediction.go's PredictionPool and
	// api/prediction_vote.go's applyPredictionVote/
	// processPredictionResolution for the RC-bet mechanics layered on
	// top of this ordinary proposal's existing Yes/No headcount vote.
	ProposalKindPrediction ProposalKind = "PREDICTION"
	// ProposalKindModeratorDispute lets any user reference a specific
	// past moderator decision (a negotiation, P2P offer, or Trading Hub
	// company-application ID that moderator was actually assigned to -
	// see DisputeReferenceType/DisputeReferenceID below) and request a
	// community vote on whether that moderator abused the role. A
	// PASSED outcome applies a reputation penalty to
	// DisputeModeratorAddress (see api/apply.go's finalizeProposal) -
	// deliberately does NOT restitute funds to the user, which would
	// need a funding-source/escrow decision this mechanism doesn't
	// make; that's named explicit future work.
	ProposalKindModeratorDispute ProposalKind = "MODERATOR_DISPUTE"
	// ProposalKindNodeAdmission requests admission of a specific new
	// node identity (NodeAdmissionPublicKey/NodeAdmissionNodeID below)
	// to the peer gossip network - see core.NodeAdmissionStore, the
	// ONLY structural way a new node can ever be admitted post-genesis.
	// Deliberately a PERMANENT grant, not time-limited: nothing else in
	// this governance system expires, and expiry would require a
	// re-vote cadence and a mid-session-lapse story with no real gain
	// toward the actual goal (removing any single party's unilateral
	// admission power). Revoking an already-admitted bad actor is a
	// separate, deliberately deferred problem (see
	// ProposalKindModeratorDispute's own reference-and-vote shape for
	// the intended future pattern). Voted on with
	// api/governance_weight.go's votingWeightForNodeAdmission (an
	// accumulated-conviction score, NOT the general votingWeight
	// formula) at a doubled quorum (api/apply.go's
	// NodeAdmissionProposalQuorum) - see that function's own doc
	// comment for why a plain reputation/stake vote isn't used here.
	ProposalKindNodeAdmission ProposalKind = "NODE_ADMISSION"
	// ProposalKindEmergency is a fast, still-decentralized alternative
	// to the admin multisig for a narrow set of safety actions - open
	// to the same general electorate as every other proposal kind
	// (same votingWeight formula, no executive-committee restriction),
	// just with a shorter voting window and a higher quorum bar (see
	// EmergencyProposalVotingWindowSeconds/
	// EmergencyProposalQuorumMultiplier below) so it can actually move
	// fast in a real incident without being a unilateral centralized
	// escape hatch. Deliberately scoped to a narrow, explicit allowlist
	// of ParamKeys at creation time (currently just BridgePausedKey -
	// see api/bridge_pause.go) rather than being a general-purpose fast
	// PARAM_CHANGE, so "emergency" can't be used to push through an
	// arbitrary change under a shorter deadline. Still timelocked (see
	// EmergencyProposalTimelockSeconds) - short, not zero - so even an
	// emergency vote never takes effect in the same block it passes.
	ProposalKindEmergency ProposalKind = "EMERGENCY"
)

// DefaultProposalQuorum/ProposalVotingWindowSeconds are judgment
// calls, not derived from any spec - calibrated for this network's
// current small scale. Both are snapshotted onto the Proposal at
// creation time (not read live at finalization) so a later change to
// either default can't retroactively move an in-flight proposal's
// bar.
const (
	DefaultProposalQuorum       uint64 = 500
	ProposalVotingWindowSeconds int64  = 7 * 24 * 3600
)

// ProposalTimelockSeconds is the delay between a PARAM_CHANGE/
// VERSION_ACTIVATION/TOKEN_CREATION proposal reaching PASSED and its
// effect actually being applied (see api/apply.go's finalizeProposal/
// executeProposal/processProposalExecution) - the three kinds whose
// PASSED outcome mutates real protocol state. Gives every wallet a
// real window to react (e.g. withdraw, adjust position) before a
// passed change takes effect, instead of it landing in the same
// block that finalizes the vote. A judgment call (48h), not derived
// from any spec. Every other kind (plain, PREDICTION,
// MODERATOR_DISPUTE, NODE_ADMISSION) still executes immediately on
// PASSED, unchanged - see ProposalKind's doc comment for why those
// don't need the same reaction window.
const ProposalTimelockSeconds int64 = 48 * 3600

// EmergencyProposalVotingWindowSeconds/EmergencyProposalTimelockSeconds
// are ProposalKindEmergency's own, shorter equivalents of
// ProposalVotingWindowSeconds/ProposalTimelockSeconds above - snapshotted
// onto the Proposal at creation time the same way, for the same reason.
// 24h to vote (vs. the ordinary 7 days) and a 4h timelock (vs. 48h) - fast
// enough to matter in a real incident, never instant. The matching higher
// quorum bar (api/governance.go's EmergencyProposalQuorum) is what keeps
// "fast" from also meaning "easy."
const (
	EmergencyProposalVotingWindowSeconds int64 = 24 * 3600
	EmergencyProposalTimelockSeconds     int64 = 4 * 3600
)

type Proposal struct {
	ID          string
	Title       string
	Description string
	CreatedBy   string

	YesVotes uint64
	NoVotes  uint64

	Open bool

	// Quorum/Deadline are snapshotted at creation - see the constants'
	// doc comment above for why. Status is set once, by
	// processProposalFinalization, the instant Deadline passes.
	Quorum   uint64
	Deadline int64
	Status   ProposalStatus

	// ExecuteAfter/Executed implement the timelock between a PASSED
	// outcome and that outcome's real protocol-state effect actually
	// landing - see ProposalTimelockSeconds/EmergencyProposalTimelockSeconds
	// above and api/apply.go's finalizeProposal/executeProposal/
	// processProposalExecution. Only ever set for the timelocked kinds
	// (PARAM_CHANGE/VERSION_ACTIVATION/TOKEN_CREATION/EMERGENCY) - every
	// other kind executes immediately on PASSED and leaves both fields
	// zero/false, same as before this existed.
	ExecuteAfter int64
	Executed     bool

	// Kind/ParamKey/ParamValue/TargetVersion/Token* are only meaningful
	// for their respective non-plain Kind - see
	// api/governance_params.go, core/config.go's
	// ActivatedProtocolVersion, and api/token_creation_governance.go
	// for what a PASSED outcome of each kind actually does.
	Kind          ProposalKind
	ParamKey      string
	ParamValue    uint64
	TargetVersion string

	// TokenSymbol/TokenName/TokenInitialSupply/TokenStandard/
	// TokenPegReference are only meaningful for Kind ==
	// ProposalKindTokenCreation - the same fields
	// core.CustomToken/TokenRegistry.Register already takes directly
	// for the (now-retired) self-service path, carried here instead so
	// registration only actually happens once this proposal PASSES.
	TokenSymbol        string
	TokenName          string
	TokenInitialSupply uint64
	TokenStandard      string
	TokenPegReference  string

	// SystemGenerated marks a proposal the network itself created
	// (see api/apply.go's processAutoProposal) rather than a real
	// user - keeps the governance feed "constantly active" even when
	// no one has proposed anything lately. Always Kind ==
	// ProposalKindPlain (civic-engagement prompts only - a
	// system-generated proposal never carries real protocol-changing
	// power, that stays exclusively user-initiated).
	SystemGenerated bool

	// PromotedUntil is a unix timestamp set by TxProposalPromote
	// (api/rc_promote.go) - a wallet burns RC to keep this proposal
	// sorted near the top of governance.html's Voting Ballots list
	// until this time. Zero means never promoted. Re-promoting extends
	// (max, not additive) rather than stacking.
	PromotedUntil int64

	// PredictionTargetSymbol/PredictionTargetPrice are only meaningful
	// for Kind == ProposalKindPrediction. TargetSymbol must be one of
	// the two developer-accessible CCT-paired price-reference pools
	// ("BTCTEST" or "ETHTEST" - see api/token.go's
	// btcTestStore/cctBtcTestPool and ethTestStore/cctEthTestPool).
	// TargetPrice is CCT-per-unit-of-symbol, scaled by the same
	// priceScale (1,000,000) core/price_history.go and
	// core/order_book.go already use, so it's directly comparable to a
	// pool's live price with no unit conversion - see
	// api/prediction_vote.go's processPredictionResolution.
	PredictionTargetSymbol string
	PredictionTargetPrice  uint64

	// PredictionDurationSeconds is the actual voting-window length this
	// specific prediction was created with (see
	// api/prediction_vote.go's predictionDurationSeconds) - stored
	// rather than only implied by Deadline, since a PREDICTION's
	// Deadline isn't always CreatedBy + the fixed
	// ProposalVotingWindowSeconds the way every other kind's is. Lets
	// processPredictionResolution recover the real creation time
	// (Deadline - PredictionDurationSeconds) to check the 14-day
	// patience-bonus threshold.
	PredictionDurationSeconds int64

	// DisputeModeratorAddress/DisputeReferenceType/DisputeReferenceID
	// are only meaningful for Kind == ProposalKindModeratorDispute.
	// ReferenceType is one of "negotiation"/"offer"/"company" and
	// ReferenceID is that record's own ID - see
	// api/moderator_dispute_governance.go's
	// moderatorDisputeReferenceValid for how this is checked against
	// the real record before the proposal is even created.
	DisputeModeratorAddress string
	DisputeReferenceType    string
	DisputeReferenceID      string

	// NodeAdmissionPublicKey/NodeAdmissionNodeID are only meaningful
	// for Kind == ProposalKindNodeAdmission. PublicKey is hex-encoded,
	// same format as handshakeMsg.PublicKey (api/peer.go) - this is
	// what core.NodeAdmissionStore is actually keyed by once granted.
	// No separate "requesting wallet" field is needed - CreatedBy
	// already serves that role, exactly as applyTokenCreationFromProposal
	// already reuses it for ProposalKindTokenCreation.
	NodeAdmissionPublicKey string
	NodeAdmissionNodeID    string
}

func (p *Proposal) VoteYes(
	power uint64,
) {
	p.YesVotes += power
}

func (p *Proposal) VoteNo(
	power uint64,
) {
	p.NoVotes += power
}

func (p *Proposal) Passed() bool {
	return p.YesVotes > p.NoVotes
}
