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

	// Kind/ParamKey/ParamValue/TargetVersion are only meaningful for
	// their respective non-plain Kind - see
	// api/governance_params.go and core/config.go's
	// ActivatedProtocolVersion for what a PASSED outcome of each kind
	// actually does.
	Kind          ProposalKind
	ParamKey      string
	ParamValue    uint64
	TargetVersion string

	// SystemGenerated marks a proposal the network itself created
	// (see api/apply.go's processAutoProposal) rather than a real
	// user - keeps the governance feed "constantly active" even when
	// no one has proposed anything lately. Always Kind ==
	// ProposalKindPlain (civic-engagement prompts only - a
	// system-generated proposal never carries real protocol-changing
	// power, that stays exclusively user-initiated).
	SystemGenerated bool
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
