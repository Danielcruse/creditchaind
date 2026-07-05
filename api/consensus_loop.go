package api

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/creditchain/creditchaind/core"
)

const consensusTickInterval = 8 * time.Second

// missedRoundsBeforeFallback gates the recovery path below - raised
// from an original 2 (16s) to 6 (48s) after a live fork was traced to
// this firing during an ordinary node restart: the elected leader's
// own node coming back online and gossiping its next block routinely
// takes several seconds, and self-producing a "recovery" block at the
// same moment the real leader was about to publish its own creates
// two different blocks extending the same tip - an unrecoverable fork
// under this codebase's current no-rollback reorg policy (see
// api/peer.go's syncChainFromPeer doc comment). 48s comfortably
// exceeds a normal restart while still recovering from a genuinely
// dead validator in reasonable time.
const missedRoundsBeforeFallback = 6

// maxTransactionsPerBlock bounds block size - without it, a single
// block could grow unboundedly with the entire mempool's contents.
// Enforced both when THIS node produces a block (below) and when
// adopting one from a peer (see api/peer.go's blockWithinLimits) -
// a size cap only self-imposed by honest producers isn't a real limit
// at all, since nothing would stop a peer from producing an oversized
// one anyway.
const maxTransactionsPerBlock = 500

// transactionsForNextBlock takes up to maxTransactionsPerBlock oldest
// pending transactions, leaving any remainder in mempool for the next
// round - FIFO, same as mempool eviction, since there's no fee market
// here to prioritize by.
func transactionsForNextBlock() []core.Transaction {
	if len(mempool.Transactions) <= maxTransactionsPerBlock {
		return mempool.Transactions
	}
	return mempool.Transactions[:maxTransactionsPerBlock]
}

func hydrateValidator(status *core.ValidatorStatus) *core.Validator {
	wallet := walletStore.Get(status.Address)
	stakingStore.SyncWallet(wallet, status.Address)

	return &core.Validator{
		Wallet:         *wallet,
		Stake:          wallet.StakedETH,
		Uptime:         status.Uptime(),
		SlashCount:     status.SlashCount,
		Active:         status.Active,
		BlocksProduced: status.BlocksProduced,
	}
}

func hydrateValidators() []*core.Validator {
	statuses := validatorStore.List()
	list := make([]*core.Validator, 0, len(statuses))
	for _, s := range statuses {
		list = append(list, hydrateValidator(s))
	}
	return list
}

// currentLeader is the validator PoSR-selection would pick right now,
// given the current chain tip - used both by the automatic loop and
// to gate the manual "produce block" nudge to only the elected leader.
func currentLeader() *core.Validator {
	return core.SelectValidator(hydrateValidators(), blockchain.LatestBlock().Hash)
}

// localAuthorizedValidator returns a validator address this node is
// authorized to produce for - used as the fallback producer when the
// true elected leader has missed too many rounds and, being
// deterministically re-elected from an unchanging chain tip, would
// otherwise deadlock the network forever. This is a recovery path,
// not a PoSR selection - it deliberately doesn't re-run the weighted
// draw, since the point is breaking the deadlock, not re-litigating
// who "should" have won it. Empty string if this node doesn't operate
// any validator of its own.
func localAuthorizedValidator() string {
	for _, status := range validatorStore.List() {
		if status.AuthorizedNodeID == config.NodeID && status.Active {
			return status.Address
		}
	}
	return ""
}

// startConsensusLoop runs automatic PoSR leader election: every tick,
// pick the weighted leader from the current chain tip's hash. If this
// node has that address registered locally, it produces the block
// itself and gossips it. Otherwise it waits for the block to arrive
// via gossip; if the chain hasn't advanced after a couple of ticks,
// the elected validator is slashed for a missed block.
func startConsensusLoop() {
	go func() {
		ticker := time.NewTicker(consensusTickInterval)
		defer ticker.Stop()

		var awaitingSince string
		missedRounds := 0

		for range ticker.C {
			// Swept on the same ticker as everything else here - no
			// need for a second timer just to drop stale mempool
			// entries every few seconds too.
			mempool.Prune(time.Now().Unix())

			latest := blockchain.LatestBlock()

			leader := currentLeader()
			if leader == nil {
				// Nobody is an eligible validator yet - most commonly a
				// brand new network's very first moments, where the
				// only pending transactions are someone's initial
				// stake/validator-registration. Without this, the
				// network could never bootstrap itself: no leader can
				// be elected until a validator is confirmed, and a
				// validator can't be confirmed without a block. Mine a
				// neutral bootstrap block instead of waiting forever.
				if len(mempool.Transactions) > 0 {
					// "creditchain-validator" is the same neutral
					// placeholder /mine has always used - it's in
					// nonAddressValidators (apply.go) so no reward
					// gets minted to a non-address, and RecordBlock is
					// a safe no-op for it too.
					produceBlockAsLeader("creditchain-validator")
				}
				continue
			}

			if awaitingSince == latest.Hash {
				missedRounds++
				if missedRounds >= missedRoundsBeforeFallback {
					reportMissedBlock(leader.Wallet.Address, latest.Hash)
					missedRounds = 0
					awaitingSince = ""

					// One last chance for the "stuck" leader's own
					// block to show up via ordinary sync before this
					// node produces a competing one - see
					// trySyncBeforeFallback's doc comment for why this
					// check exists at all (a real fork it would have
					// prevented).
					if trySyncBeforeFallback() {
						continue
					}

					// The elected leader is stuck: since selection is
					// deterministic from the (unchanged) chain tip,
					// nothing will ever re-elect anyone else until a
					// new block actually lands - including the block
					// that would confirm the report just submitted and
					// lower the stuck leader's weight. Without a
					// fallback, an offline "elected forever" validator
					// deadlocks the whole network. If this node has its
					// own authorized validator, it steps in with a
					// recovery block so the report (and everything
					// else waiting) actually gets confirmed.
					if fallback := localAuthorizedValidator(); fallback != "" {
						produceBlockAsLeader(fallback)
					}
				}
				continue
			}

			awaitingSince = latest.Hash
			missedRounds = 0

			status, ok := validatorStore.Get(leader.Wallet.Address)
			if !ok || status.AuthorizedNodeID != config.NodeID {
				continue // this validator didn't delegate production to this node
			}

			produceBlockAsLeader(leader.Wallet.Address)
		}
	}()
}

// trySyncBeforeFallback gives every known peer one last chance to
// supply the missing block via ordinary sync (syncChainFromPeer,
// called synchronously here rather than backgrounded) before this
// node resorts to self-producing a "recovery" block. This is a direct
// fix for a live incident: the recovery path fired during a routine
// restart of the actual elected leader's node, which finished
// reconnecting and gossiping its own next block only moments later -
// by then another node had already self-produced a competing one at
// the same height, an unrecoverable fork under this codebase's
// no-rollback reorg policy (see syncChainFromPeer's doc comment).
// Returns true if any peer's chain turned out to be longer (meaning
// this node adopted it and should NOT also self-produce this round).
func trySyncBeforeFallback() bool {
	before := len(blockchain.Blocks)
	for _, p := range peerStore.List() {
		syncChainFromPeer(p.Address)
	}
	return len(blockchain.Blocks) > before
}

// produceBlockAsLeader mines, signs, applies and gossips a block on
// behalf of the given (locally-registered) validator address.
func produceBlockAsLeader(address string) core.Block {
	latest := blockchain.LatestBlock()

	block := core.NewBlock(
		latest.Index+1,
		latest.Hash,
		address,
		transactionsForNextBlock(),
	)

	block.ProducerNodeID = config.NodeID
	block.Signature = nodeIdentity.SignBlock(block)

	blockchain.Blocks = append(blockchain.Blocks, block)
	ApplyBlock(block) // also mints this validator's reward and records the block - see apply.go
	removeFromMempool(block.Transactions)

	if err := blockchain.Save(dataPath("blockchain.json")); err != nil {
		fmt.Println("block save failed:", err)
	}

	// Automatic production happens outside any HTTP request, so
	// corsMiddleware's POST-triggered SaveState() never fires here -
	// without this, a crash before the next unrelated POST would lose
	// the dedup-guard/state snapshot for a block the chain file
	// already shows as applied.
	if err := SaveState(); err != nil {
		fmt.Println("state save failed:", err)
	}

	go gossipBlock(block)

	return block
}

// reporterAddress identifies THIS node as the submitter of a slash
// report - a system/network action, not a wallet action, but reusing
// the exact same Transaction/gossip/ApplyBlock machinery as
// everything else rather than inventing a parallel path.
func reporterAddress() string {
	return core.GenerateAddress(nodeIdentity.PublicKey)
}

// reportMissedBlock submits a slash report instead of mutating state
// directly - the actual penalty only takes effect once the report is
// confirmed in a block (see api/apply.go's applySlashReport), so
// every node ends up with the same reputation/slash-count for the
// affected validator instead of just whichever node noticed first.
func reportMissedBlock(address, roundHash string) {
	data, _ := json.Marshal(slashReportData{
		Reason:           "missed_block",
		ValidatorAddress: address,
		RoundHash:        roundHash,
	})

	fmt.Println("reporting missed block for", address, "round", roundHash)

	recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxSlashReport, string(data))
}
