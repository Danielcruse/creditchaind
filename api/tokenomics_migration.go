package api

import (
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// HandleTokenomicsDecimalMigration triggers the one-time conversion of
// every CCT balance from the pre-migration whole-unit ledger
// (tokenStore) into the 18-decimal ledger (cctStore) - admin-only,
// recorded as a self-signed TxTokenomicsDecimalMigration (same trust
// model as HandleAllocateGenesisPools) so it replicates identically to
// every node rather than only mutating whichever node received this
// HTTP request.
func HandleTokenomicsDecimalMigration(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	signedAuth, mode, err := requireAdminAuthed(r, nil)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if treasuryStore.Treasury.TokenomicsDecimalMigrationApplied {
		http.Error(w, "the CCT decimal migration has already been applied", http.StatusConflict)
		return
	}

	data := []byte("{}")
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxTokenomicsDecimalMigration, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

// applyTokenomicsDecimalMigration is HandleTokenomicsDecimalMigration's
// apply-side counterpart - idempotent via
// core.Treasury.TokenomicsDecimalMigrationApplied, same pattern
// applyGenesisPoolAllocation already uses.
//
// tokenStore.Balances already contains every address that ever held a
// CCT balance under the old ledger - real wallets AND every synthetic
// sink/escrow address (STAKE:CCT, FEE:CCT_TRANSFER, FEE:REGISTRATION,
// the 4 synthetic genesis pools, the Foundation Treasury address) -
// since they're all just ordinary map keys in that one store. This
// means the migration needs no separate enumeration of "which
// addresses matter" - it converts the whole map, address for address,
// preserving every existing balance and position (including
// currently-staked/escrowed CCT) exactly.
//
// Computed into a temporary map first and only committed to cctStore
// if the sum-of-converted-balances invariant holds exactly against
// the old ledger's own recorded TotalSupply - if it doesn't, the whole
// migration aborts with nothing written and the flag left unset, so a
// corrupted/inconsistent old ledger can never silently produce a
// wrong starting supply for the new one.
func applyTokenomicsDecimalMigration(tx core.Transaction) {
	if treasuryStore.Treasury.TokenomicsDecimalMigrationApplied {
		return
	}

	converted := make(map[string]string, len(tokenStore.Balances))
	sum := big.NewInt(0)
	for address, oldBalance := range tokenStore.Balances {
		if oldBalance == 0 {
			continue
		}
		smallest := core.CctWholeToSmallest(oldBalance)
		converted[address] = core.FormatCctSmallest(smallest)
		sum.Add(sum, smallest)
	}

	expected := core.CctWholeToSmallest(tokenStore.Token.TotalSupply)
	if sum.Cmp(expected) != 0 {
		// Invariant violated - abort without writing anything. This
		// should never happen against a consistent ledger; treated as
		// a hard stop rather than a best-effort partial migration,
		// since silently under/over-crediting the new ledger's starting
		// supply is exactly the kind of bug this check exists to catch
		// before it ever reaches a live balance.
		return
	}

	for address, smallest := range converted {
		cctStore.Mint(address, smallest)
	}

	treasuryStore.Treasury.TokenomicsDecimalMigrationApplied = true
}
