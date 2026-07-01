package core

type Mempool struct {
	Transactions []Transaction
}

func (m *Mempool) Add(
	tx Transaction,
) {
	m.Transactions = append(
		m.Transactions,
		tx,
	)
}

func (m *Mempool) Clear() {
	m.Transactions = []Transaction{}
}