package core

// SlashingRecord is a permanent, auditable record of a validator
// penalty - intended to be written to LoanHistory-style permanent
// storage / Explorer once that wiring is built out.
type SlashingRecord struct {
	Validator string
	Reason    string
	Amount    uint64 // reputation points deducted
}

// Locked slashing penalties (approved 2026-07). Double-signing gets a
// severe penalty because it attacks the protocol itself, not just an
// individual validator's own standing.
const (
	ReputationPenaltyMissedBlock = 2
	ReputationPenaltyDoubleSign  = 50
	SlashStrikesMissedBlock      = 1
	SlashStrikesDoubleSign       = 3

	// MissedBlockStakeSlashBps/DoubleSignStakeSlashBps are PoSR's
	// literal "may lose staked CCT" penalty, in basis points (100 =
	// 1%) of the validator's currently-staked CCT - burned, not
	// redistributed, so a validator can never profit from falsely
	// reporting another one. Sized proportionally to the existing
	// reputation penalties above (missed-block is a minor, common
	// slip; double-sign attacks consensus itself and is punished far
	// more severely).
	MissedBlockStakeSlashBps = 50   // 0.50%
	DoubleSignStakeSlashBps  = 1000 // 10.00%

	// PermanentBanSlashCountThreshold is PoSR's "repeated offences may
	// permanently remove validator privileges" - reuses the existing
	// SlashStrikes weighting above (2 double-signs, or 1 double-sign +
	// 3 missed blocks, or 6 missed blocks alone, all cross it). A
	// permanently banned validator can never register or be selected
	// again - see api/apply.go's applyValidatorRegister and
	// api/consensus_loop.go's hydrateValidators.
	PermanentBanSlashCountThreshold = 6
)

// ApplyMissedBlock penalizes a validator selected to produce a block
// who failed to do so.
func ApplyMissedBlock(v *Validator) SlashingRecord {
	DecreaseReputation(v, ReputationPenaltyMissedBlock)
	v.SlashCount += SlashStrikesMissedBlock

	return SlashingRecord{
		Validator: v.Wallet.Address,
		Reason:    "missed_block",
		Amount:    ReputationPenaltyMissedBlock,
	}
}

// ApplyDoubleSign penalizes a validator caught signing conflicting
// blocks - the most severe offense in the protocol, since it attacks
// consensus itself rather than just being unreliable.
func ApplyDoubleSign(v *Validator) SlashingRecord {
	DecreaseReputation(v, ReputationPenaltyDoubleSign)
	v.SlashCount += SlashStrikesDoubleSign

	return SlashingRecord{
		Validator: v.Wallet.Address,
		Reason:    "double_sign",
		Amount:    ReputationPenaltyDoubleSign,
	}
}
