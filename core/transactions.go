package core

import "time"

type TransactionType string

const (
	TxBorrow         TransactionType = "BORROW"
	TxLend           TransactionType = "LEND"
	TxExchange       TransactionType = "EXCHANGE"
	TxReward         TransactionType = "REWARD"
	TxGovernance     TransactionType = "GOVERNANCE"
	TxReputation     TransactionType = "REPUTATION"

	TxStakeETH       TransactionType = "STAKE_ETH"
	TxUnstakeETH     TransactionType = "UNSTAKE_ETH"

	TxMintOETH       TransactionType = "MINT_OETH"
	TxBurnOETH       TransactionType = "BURN_OETH"

	TxClaimReward    TransactionType = "CLAIM_REWARD"

	TxBridgeDeposit  TransactionType = "BRIDGE_DEPOSIT"
	TxBridgeWithdraw TransactionType = "BRIDGE_WITHDRAW"
)

type Transaction struct {
	ID string

	From string
	To   string

	Amount uint64

	Type TransactionType

	Timestamp int64

	Data string

	Signature []byte
}

func NewTransaction(
	from string,
	to string,
	amount uint64,
	txType TransactionType,
	data string,
) Transaction {

	return Transaction{
		ID:        "",
		From:      from,
		To:        to,
		Amount:    amount,
		Type:      txType,
		Timestamp: time.Now().Unix(),
		Data:      data,
	}
}