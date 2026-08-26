package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
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
	case core.TxBridgeTxHashRecord:
		// Pure plumbing (see core.TxBridgeTxHashRecord's doc comment) -
		// the real fund movement it's attached to is the original
		// TxBridgeWithdraw, already visible; showing this too would just
		// be a confusing duplicate row with no amount/counterparty of
		// its own. Its content is surfaced via TransactionView's
		// ExternalTxHash field on the original withdrawal instead (see
		// explorer_summary.go).
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

// HeldTokenBalance is one nonzero held (not staked) balance for an
// address - either a bridged native token (ETHB/BTCB/USDCTEST/YENE) or
// a developer-registered custom token. Balance is a decimal string
// (not uint64) since ETHB's wei-denominated balance can exceed JS's
// safe-integer range, matching the convention every other
// wei-denominated endpoint in this codebase already uses (see
// HandleEthbBalance).
type HeldTokenBalance struct {
	Symbol  string `json:"symbol"`
	Balance string `json:"balance"`
}

// AddressLoanView is one loan an address is party to, either as the
// borrower or the lender - deliberately excludes collateral/risk-stage
// detail (that's what /loans/health is for) since this is a summary
// list, not a full loan detail view.
type AddressLoanView struct {
	ID     string `json:"id"`
	Role   string `json:"role"` // "borrower" or "lender"
	Symbol string `json:"symbol"`
	Amount uint64 `json:"amount"`
	Status string `json:"status"`
	DueAt  int64  `json:"dueAt"`
}

// AddressProfile is the "explorer sheet" for a single address -
// balance across every ledger, reputation/tier, and ordered
// (newest-first) transaction and governance-vote history in one
// response, instead of making the caller stitch together /wallet,
// /token/balance, /reputation, /rept/balance, and a filtered
// transaction search into a picture by hand.
type AddressProfile struct {
	Address string `json:"address"`

	CCTBalance     string `json:"cctBalance"`
	ETHTestStaked  uint64 `json:"ethTestStaked"`
	CCTStaked      uint64 `json:"cctStaked"`
	USDCTestStaked uint64 `json:"usdcTestStaked"`
	OETHBalance    uint64 `json:"oethBalance"`
	REPTBalance    uint64 `json:"reptBalance"`

	// HeldBalances is every held (not staked) ETHB/BTCB/USDCTEST/YENE
	// and developer-registered custom-token balance that's actually
	// nonzero for this address. This used to be structurally
	// impossible for an arbitrary looked-up address - ETHB etc. live
	// at a sub-address derived from the wallet's real public key (see
	// core/address.go), which this server never stored for any
	// address except the one making a signed request. It's closed
	// here by recovering that public key from the address's OWN most
	// recent confirmed transaction (every core.Transaction already
	// carries its signer's AuthPublicKey for signature verification -
	// no new storage needed, just a lookup this profile already had
	// the data for). A wallet that's never transacted has no known
	// public key yet, so its held balances simply aren't computed -
	// disclosed limitation, not a crash, same "disclose don't
	// silently break" convention used everywhere else in this
	// codebase.
	HeldBalances []HeldTokenBalance `json:"heldBalances"`

	// Loans is every loan this address is party to, as either
	// borrower or lender - deliberately NOT including P2P marketplace
	// offers the address has posted (out of scope for this profile by
	// design, per the explicit "don't display marketplace offers"
	// request).
	Loans []AddressLoanView `json:"loans"`

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

	// authPublicKey is this address's own real public key, recovered
	// from the newest confirmed transaction it actually signed
	// (AuthAddress == address) - the loop below already walks every
	// block newest-first, so the FIRST match found here is genuinely
	// the most recent one. Stays "" if this address has never signed
	// anything yet (see HeldBalances' doc comment on AddressProfile).
	authPublicKey := ""

	for i := len(blockchain.Blocks) - 1; i >= 0; i-- {
		blockTxs := blockchain.Blocks[i].Transactions
		for j := len(blockTxs) - 1; j >= 0; j-- {
			tx := blockTxs[j]

			if authPublicKey == "" && tx.AuthAddress == address && tx.AuthPublicKey != "" {
				authPublicKey = tx.AuthPublicKey
			}

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

	heldBalances := []HeldTokenBalance{}
	if authPublicKey != "" {
		for _, symbol := range []string{"ETHB", "BTCB", "USDCTEST", "YENE"} {
			subAddr := resolveBalanceAddress(address, authPublicKey, symbol)
			var bal uint64
			switch symbol {
			case "ETHB":
				bal = bridgedEthStore.BalanceOf(subAddr)
			case "BTCB":
				bal = bridgedBtcStore.BalanceOf(subAddr)
			case "USDCTEST":
				bal = usdcTestStore.BalanceOf(subAddr)
			case "YENE":
				bal = yenStore.BalanceOf(subAddr)
			}
			if bal > 0 {
				heldBalances = append(heldBalances, HeldTokenBalance{Symbol: symbol, Balance: fmt.Sprintf("%d", bal)})
			}
		}

		// ETH_CCC1/USDC_CCC1/BTC_CCC1 (the real, mainnet vault-backed
		// balances) were missing from this profile entirely until now -
		// a real gap, not just the display-formatting one this session
		// also fixed on the explorer's transaction-list side. Balance
		// stays the raw smallest-unit string (18/6 decimals for
		// ETH_CCC1/USDC_CCC1, whole sats for BTC_CCC1) - the client
		// applies the same decimal formatting it already uses for
		// transaction amounts of these symbols, rather than this
		// endpoint pre-formatting a display string server-side.
		if ethCcc1Bal := ethCcc1Store.BalanceOf(resolveBalanceAddress(address, authPublicKey, "ETH_CCC1")); ethCcc1Bal != "0" {
			heldBalances = append(heldBalances, HeldTokenBalance{Symbol: "ETH_CCC1", Balance: ethCcc1Bal})
		}
		if usdcCcc1Bal := usdcCcc1Store.BalanceOf(resolveBalanceAddress(address, authPublicKey, "USDC_CCC1")); usdcCcc1Bal != "0" {
			heldBalances = append(heldBalances, HeldTokenBalance{Symbol: "USDC_CCC1", Balance: usdcCcc1Bal})
		}
		if btcCcc1Bal := btcCcc1Store.BalanceOf(resolveBalanceAddress(address, authPublicKey, "BTC_CCC1")); btcCcc1Bal > 0 {
			heldBalances = append(heldBalances, HeldTokenBalance{Symbol: "BTC_CCC1", Balance: fmt.Sprintf("%d", btcCcc1Bal)})
		}
	}
	// Developer-registered custom tokens live at the plain address
	// (see resolveBalanceAddress's default case) - no public key
	// needed for these, unlike the native tokens above.
	customSymbols := make([]string, 0, len(tokenRegistry.Tokens))
	for symbol := range tokenRegistry.Tokens {
		customSymbols = append(customSymbols, symbol)
	}
	sort.Strings(customSymbols)
	for _, symbol := range customSymbols {
		bal := tokenRegistry.Tokens[symbol].BalanceOf(address)
		if bal > 0 {
			heldBalances = append(heldBalances, HeldTokenBalance{Symbol: symbol, Balance: fmt.Sprintf("%d", bal)})
		}
	}

	// Loans this address is party to, as either borrower or lender -
	// sorted by ID for deterministic output (loanStore.Loans is a plain
	// Go map, whose iteration order is otherwise random).
	loanIDs := make([]string, 0, len(loanStore.Loans))
	for id := range loanStore.Loans {
		loanIDs = append(loanIDs, id)
	}
	sort.Strings(loanIDs)
	addressLoans := []AddressLoanView{}
	for _, id := range loanIDs {
		loan := loanStore.Loans[id]
		if loan.Borrower == address {
			addressLoans = append(addressLoans, AddressLoanView{
				ID: loan.ID, Role: "borrower", Symbol: loan.Symbol,
				Amount: loan.Amount, Status: string(loan.Status), DueAt: loan.DueAt,
			})
		} else if loan.Lender == address {
			addressLoans = append(addressLoans, AddressLoanView{
				ID: loan.ID, Role: "lender", Symbol: loan.Symbol,
				Amount: loan.Amount, Status: string(loan.Status), DueAt: loan.DueAt,
			})
		}
	}

	return AddressProfile{
		Address: address,

		CCTBalance:     cctStore.BalanceOf(address),
		ETHTestStaked:  wallet.StakedETH,
		CCTStaked:      wallet.StakedCCT,
		USDCTestStaked: wallet.StakedUSDCTEST,
		OETHBalance:    wallet.OETHBalance,
		REPTBalance:    reptStore.BalanceOf(address),

		HeldBalances: heldBalances,
		Loans:        addressLoans,

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
