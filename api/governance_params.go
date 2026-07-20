package api

import "github.com/creditchain/creditchaind/core"

// governanceParamsStore seeds with today's hardcoded fee defaults so
// every read has a real value from process start, before any
// PARAM_CHANGE proposal has ever passed - see
// core.GovernanceParamsStore's doc comment for why this starts with
// only these two keys, not a full constants migration.
var governanceParamsStore = core.NewGovernanceParamsStore()

func init() {
	governanceParamsStore.Seed("ValidatorRegisterFeeCCT", ValidatorRegisterFeeCCT)
	governanceParamsStore.Seed("CCTTransferFee", CCTTransferFee)
}

// governanceParam reads a governance-overridable parameter, falling
// back to the given hardcoded default if it was never seeded/set
// (defensive only - init() above always seeds both keys this
// codebase currently reads this way, so the fallback path is not
// expected to actually run in practice).
func governanceParam(key string, fallback uint64) uint64 {
	if v, ok := governanceParamsStore.Get(key); ok {
		return v
	}
	return fallback
}
