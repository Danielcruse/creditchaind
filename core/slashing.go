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
	ReputationPenaltyMissedBlock  = 2
	ReputationPenaltyDoubleSign   = 50
	SlashStrikesMissedBlock       = 1
	SlashStrikesDoubleSign        = 3
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
