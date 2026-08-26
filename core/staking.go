package core

// RewardForStake computes the OETH reward for a given staked amount.
// Still used by StakingStore.Reward (staking_rewards.go).
func RewardForStake(amount uint64) uint64 {
	return amount / 10
}

// NOTE: the free functions StakeETH(*Wallet,...), RequestUnstake(*Wallet,...),
// Stake(*Validator,...), and Unstake(*Validator,...) that used to live here
// were removed during the staking consolidation pass - they mutated
// Wallet.StakedETH / Validator.Stake directly, bypassing StakingStore,
// which is now the single canonical stake ledger (see staking_store.go,
// staking_sync.go). They had zero external callers at the time of removal
// (verified via full-repo grep), so this is a no-op for anything that
// was compiling before.
