package core

type PoolMember struct {
	Address string

	Stake uint64

	Storage uint64

	Compute uint64

	Percent uint64
}

type ValidatorPool struct {
	ValidatorID string

	Leader string

	Members []PoolMember
}