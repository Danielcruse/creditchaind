package core

func CalculateContribution(
	member *PoolMember,
) uint64 {

	return member.Stake +
		member.Storage +
		member.Compute
}

func UpdatePercentages(
	pool *ValidatorPool,
) {

	var total uint64

	for _, member :=
		range pool.Members {

		total +=
			CalculateContribution(
				&member,
			)
	}

	if total == 0 {
		return
	}

	for i :=
		range pool.Members {

		score :=
			CalculateContribution(
				&pool.Members[i],
			)

		pool.Members[i].Percent =
			(score * 100) /
				total
	}

	UpdateLeader(pool)
}

func DistributeRewards(
	pool *ValidatorPool,
	reward uint64,
) map[string]uint64 {

	rewards :=
		make(
			map[string]uint64,
		)

	for _, member :=
		range pool.Members {

		share :=
			(reward *
				member.Percent) /
				100

		rewards[member.Address] =
			share
	}

	return rewards
}