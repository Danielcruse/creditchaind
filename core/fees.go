package core

type FeeEngine struct {
	BorrowFee uint64
	StakeFee  uint64
	TxFee     uint64
}

func NewFeeEngine() *FeeEngine {
	return &FeeEngine{
		BorrowFee: 1,
		StakeFee:  1,
		TxFee:     1,
	}
}