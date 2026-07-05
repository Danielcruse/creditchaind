package core

func AddMember(
	pool *ValidatorPool,
	member PoolMember,
) {
	pool.Members =
		append(
			pool.Members,
			member,
		)

	UpdateLeader(pool)
}

func UpdateLeader(
	pool *ValidatorPool,
) {
	var highest uint64
	var leader string

	for _, member :=
		range pool.Members {

		if member.Percent >
			highest {

			highest =
				member.Percent

			leader =
				member.Address
		}
	}

	pool.Leader =
		leader
}