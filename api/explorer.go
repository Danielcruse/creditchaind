package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

const (
	defaultExplorerLimit = 10
	maxExplorerLimit     = 50
)

type ExplorerResponse struct {
	BlockHeight     int    `json:"blockHeight"`
	LatestBlockHash string `json:"latestBlockHash"`

	Blocks       []core.Block `json:"blocks"`
	BlocksTotal  int          `json:"blocksTotal"`
	BlocksOffset int          `json:"blocksOffset"`
	BlocksLimit  int          `json:"blocksLimit"`

	// Confirmed transactions, newest block first - windowed the same
	// way as blocks so a chain with thousands of transactions doesn't
	// get sent (and rendered) in one endless dump. Scanning stops as
	// soon as enough are collected, so cost is bounded by offset+limit,
	// not total chain length.
	Transactions        []TransactionView `json:"transactions"`
	TransactionsOffset  int               `json:"transactionsOffset"`
	TransactionsLimit   int               `json:"transactionsLimit"`
	TransactionsHasMore bool              `json:"transactionsHasMore"`

	PendingTransactions []TransactionView `json:"pendingTransactions"`
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clampNonNeg(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func clampLimit(n int) int {
	if n <= 0 {
		return defaultExplorerLimit
	}
	if n > maxExplorerLimit {
		return maxExplorerLimit
	}
	return n
}

// isExplorerVisible decides whether a transaction is worth showing in
// the Explorer. P2P settlement produces several purely procedural
// "someone clicked a button" transactions (buyer claiming payment,
// a rejected disagree-confirm, a hub accepting an offer) that never
// move funds and just clutter the feed - only the transactions that
// actually move escrow (offer/trade creation, a real confirm-and-release,
// moderator resolution) count as reportable P2P activity. Deliberately
// scoped to only these P2P types - every other transaction type
// (governance, KYC, validator registration, etc.) stays visible.
func isExplorerVisible(tx core.Transaction) bool {
	switch tx.Type {
	case core.TxP2PBuyerMarkPaid, core.TxP2PSellerDispute, core.TxP2PExternalAccept:
		return false
	case core.TxP2PBuyerConfirmPaid:
		var data p2pBuyerConfirmData
		if err := json.Unmarshal([]byte(tx.Data), &data); err == nil && !data.Agree {
			return false
		}
	}
	return true
}

// collectConfirmedTransactions walks blocks newest-to-oldest collecting
// transactions, skipping offset and stopping once limit is collected -
// cost is bounded by how far back the requested page is, not by the
// total size of the chain.
func collectConfirmedTransactions(offset, limit int) (result []core.Transaction, hasMore bool) {
	skipped := 0
	for i := len(blockchain.Blocks) - 1; i >= 0; i-- {
		txs := blockchain.Blocks[i].Transactions
		for j := len(txs) - 1; j >= 0; j-- {
			if !isExplorerVisible(txs[j]) {
				continue
			}
			if skipped < offset {
				skipped++
				continue
			}
			if len(result) == limit {
				return result, true
			}
			result = append(result, txs[j])
		}
	}
	return result, false
}

func HandleExplorer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	q := r.URL.Query()

	blocksOffset := clampNonNeg(atoiDefault(q.Get("blocksOffset"), 0))
	blocksLimit := clampLimit(atoiDefault(q.Get("blocksLimit"), defaultExplorerLimit))
	txOffset := clampNonNeg(atoiDefault(q.Get("txOffset"), 0))
	txLimit := clampLimit(atoiDefault(q.Get("txLimit"), defaultExplorerLimit))

	total := len(blockchain.Blocks)

	end := total - blocksOffset
	if end < 0 {
		end = 0
	}
	if end > total {
		end = total
	}
	start := end - blocksLimit
	if start < 0 {
		start = 0
	}

	window := make([]core.Block, 0, end-start)
	for i := end - 1; i >= start; i-- {
		window = append(window, blockchain.Blocks[i])
	}

	confirmedTxs, txHasMore := collectConfirmedTransactions(txOffset, txLimit)

	var latestHash string
	if total > 0 {
		latestHash = blockchain.Blocks[total-1].Hash
	}

	resp := ExplorerResponse{
		BlockHeight:     total,
		LatestBlockHash: latestHash,

		Blocks:       window,
		BlocksTotal:  total,
		BlocksOffset: blocksOffset,
		BlocksLimit:  blocksLimit,

		Transactions:        viewTransactions(confirmedTxs),
		TransactionsOffset:  txOffset,
		TransactionsLimit:   txLimit,
		TransactionsHasMore: txHasMore,

		PendingTransactions: viewTransactions(mempool.Transactions),
	}

	json.NewEncoder(w).Encode(resp)
}

type ExplorerSearchResponse struct {
	Query        string            `json:"query"`
	Found        bool              `json:"found"`
	Type         string            `json:"type,omitempty"` // "block" | "transactions" | "address"
	Block        *core.Block       `json:"block,omitempty"`
	Transactions []TransactionView `json:"transactions,omitempty"`
	Address      *AddressProfile   `json:"address,omitempty"`
}

// addressPattern matches the shape every real address in this system
// has - core.GenerateAddress hex-encodes a 20-byte hash, always
// exactly 40 lowercase hex characters, never anything else (block
// hashes are 64 hex chars or "GENESIS"; transaction IDs are checked
// for an exact match separately, before this is ever consulted) - so
// this reliably tells "the user is searching for an address" apart
// from a garbled/unrelated query, even for an address that happens to
// have zero activity yet.
var addressPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// maxAddressProfileTransactions/Votes cap how much history a single
// search result carries - the same "windowed, not an endless dump"
// principle already applied to the main block/transaction lists (see
// HandleExplorer), just applied to a single address's own activity
// instead of the whole chain.
const (
	maxAddressProfileTransactions = 50
	maxAddressProfileVotes        = 50
)

type AddressVoteRecord struct {
	ProposalID string `json:"proposalId"`
	Title      string `json:"title"`
	Support    bool   `json:"support"`
	Timestamp  int64  `json:"timestamp"`
}

// AddressProfile is the "explorer sheet" for a single address -
// balance across every ledger, reputation/tier, and ordered
// (newest-first) transaction and governance-vote history in one
// response, instead of making the caller stitch together /wallet,
// /token/balance, /reputation, /rept/balance, and a filtered
// transaction search into a picture by hand.
type AddressProfile struct {
	Address string `json:"address"`

	CCTBalance     uint64 `json:"cctBalance"`
	ETHTestStaked  uint64 `json:"ethTestStaked"`
	CCTStaked      uint64 `json:"cctStaked"`
	USDCTestStaked uint64 `json:"usdcTestStaked"`
	OETHBalance    uint64 `json:"oethBalance"`
	REPTBalance    uint64 `json:"reptBalance"`
	// ETHTEST/USDCTEST HELD (not staked) balances are deliberately not
	// included here - they live at a sub-address derived from the
	// wallet's real public key (see core/address.go), which this
	// server never stores for any address except the one currently
	// making a signed request. Looking up an arbitrary address by
	// string alone (what this profile does) has no way to derive it -
	// querying the raw address itself would always read 0 (nothing is
	// ever credited there), which is worse than not showing a number
	// at all. A wallet viewing its OWN balance can and does compute
	// this client-side (see js/api.js's balanceAddressForSymbol) -
	// this is the same disclosed gap wallet recovery and the dormancy
	// rule already work around the same way.

	Reputation  uint64          `json:"reputation"`
	CreditTier  core.CreditTier `json:"creditTier"`
	VotingPower uint64          `json:"votingPower"`

	Transactions      []TransactionView `json:"transactions"`
	TransactionsTotal int               `json:"transactionsTotal"`

	Votes []AddressVoteRecord `json:"votes"`
}

// buildAddressProfile walks the full chain newest-block-first exactly
// once, collecting both the transaction history and the governance
// votes an address cast in the same pass - two related "what has this
// address done" views assembled together rather than two separate
// full scans.
func buildAddressProfile(address string) AddressProfile {
	wallet := walletStore.Get(address)
	stakingStore.SyncWallet(wallet, address)

	// Initialized non-nil so a fresh/inactive address serializes as
	// [] rather than null - a client checking .length on an empty
	// result shouldn't have to null-guard every field first.
	txs := []core.Transaction{}
	votes := []AddressVoteRecord{}
	total := 0

	for i := len(blockchain.Blocks) - 1; i >= 0; i-- {
		blockTxs := blockchain.Blocks[i].Transactions
		for j := len(blockTxs) - 1; j >= 0; j-- {
			tx := blockTxs[j]
			if tx.From != address && tx.To != address {
				continue
			}
			if !isExplorerVisible(tx) {
				continue
			}
			total++

			if len(txs) < maxAddressProfileTransactions {
				txs = append(txs, tx)
			}

			if tx.Type == core.TxGovernance && tx.From == address && len(votes) < maxAddressProfileVotes {
				var data governanceData
				if err := json.Unmarshal([]byte(tx.Data), &data); err == nil && data.Action == "vote" {
					title := data.ProposalID
					if p, ok := proposalStore.Get(data.ProposalID); ok {
						title = p.Title
					}
					votes = append(votes, AddressVoteRecord{
						ProposalID: data.ProposalID,
						Title:      title,
						Support:    data.Support,
						Timestamp:  tx.Timestamp,
					})
				}
			}
		}
	}

	return AddressProfile{
		Address: address,

		CCTBalance:     tokenStore.BalanceOf(address),
		ETHTestStaked:  wallet.StakedETH,
		CCTStaked:      wallet.StakedCCT,
		USDCTestStaked: wallet.StakedUSDCTEST,
		OETHBalance:    wallet.OETHBalance,
		REPTBalance:    reptStore.BalanceOf(address),

		Reputation:  wallet.Reputation,
		CreditTier:  core.TierForReputation(wallet.Reputation),
		VotingPower: wallet.VotingPower,

		Transactions:      viewTransactions(txs),
		TransactionsTotal: total,

		Votes: votes,
	}
}

// HandleExplorerSearch resolves a query against the full chain, not just
// the windowed "recent" view HandleExplorer returns - this is the
// deliberate escape hatch for "I need something older than what's on
// screen" without ever having to load the whole chain into the browser.
func HandleExplorerSearch(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	resp := ExplorerSearchResponse{Query: query}

	if query == "" {
		json.NewEncoder(w).Encode(resp)
		return
	}

	if idx, err := strconv.Atoi(query); err == nil && idx >= 0 && idx < len(blockchain.Blocks) {
		b := blockchain.Blocks[idx]
		resp.Found = true
		resp.Type = "block"
		resp.Block = &b
		json.NewEncoder(w).Encode(resp)
		return
	}

	for _, b := range blockchain.Blocks {
		if b.Hash == query {
			block := b
			resp.Found = true
			resp.Type = "block"
			resp.Block = &block
			json.NewEncoder(w).Encode(resp)
			return
		}

		for _, tx := range b.Transactions {
			if tx.ID == query && isExplorerVisible(tx) {
				resp.Found = true
				resp.Type = "transactions"
				resp.Transactions = []TransactionView{viewTransaction(tx)}
				json.NewEncoder(w).Encode(resp)
				return
			}
		}
	}

	// Not a block or an exact transaction ID - if it's shaped like a
	// real address, return the full "explorer sheet" for it (balance
	// across every ledger, reputation, ordered transaction and vote
	// history) rather than just a flat list of matching transactions.
	// Deliberately returns a (mostly empty) profile even for an
	// address with zero activity, same as a real explorer would - "no
	// history yet" is a valid, meaningful answer, not a failed search.
	if addressPattern.MatchString(query) {
		profile := buildAddressProfile(query)
		resp.Found = true
		resp.Type = "address"
		resp.Address = &profile
	}

	json.NewEncoder(w).Encode(resp)
}
