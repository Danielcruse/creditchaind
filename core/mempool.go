package core

// MaxMempoolSize bounds unbounded memory growth from spam or a
// stalled network (no leader confirming blocks) - once full, Add
// evicts the single oldest pending transaction to make room. There's
// no fee market here to prioritize by, so eviction is strictly FIFO:
// whatever's been waiting longest goes first, same as it would be the
// next one dropped by expiry anyway (see Prune).
const MaxMempoolSize = 5000

// MempoolTxExpiry is how long a transaction may sit in mempool,
// unconfirmed, before Prune drops it - closes the gap where a
// transaction that will never be included (e.g. a validator silently
// rejected it for failing auth, or the network partitioned) would
// otherwise occupy space forever.
const MempoolTxExpiry = 15 * 60 // seconds

type Mempool struct {
	Transactions []Transaction
}

func (m *Mempool) Add(
	tx Transaction,
) {
	if len(m.Transactions) >= MaxMempoolSize {
		m.Transactions = m.Transactions[1:]
	}

	m.Transactions = append(
		m.Transactions,
		tx,
	)
}

// Prune drops every transaction older than MempoolTxExpiry relative
// to now - called periodically from the consensus loop's own ticker
// (see api/consensus_loop.go) rather than on every Add, since mempool
// staleness only needs to be swept occasionally, not checked on every
// single insert.
func (m *Mempool) Prune(now int64) {
	kept := make([]Transaction, 0, len(m.Transactions))
	for _, tx := range m.Transactions {
		if now-tx.Timestamp <= MempoolTxExpiry {
			kept = append(kept, tx)
		}
	}
	m.Transactions = kept
}

func (m *Mempool) Clear() {
	m.Transactions = []Transaction{}
}

// Has reports whether a transaction with this ID is already in the
// mempool, so gossip relays don't re-add the same transaction twice.
func (m *Mempool) Has(id string) bool {
	if id == "" {
		return false
	}
	for _, tx := range m.Transactions {
		if tx.ID == id {
			return true
		}
	}
	return false
}