package core

import "testing"

func TestPredictionPoolStore_LazyCreationOnFirstInfusion(t *testing.T) {
	s := NewPredictionPoolStore()

	if got := s.Get("prop-1"); got != nil {
		t.Fatalf("expected no pool before any infusion, got %+v", got)
	}

	s.Infuse(&PredictionInfusion{ProposalID: "prop-1", Address: "addr-a", Side: PredictionSideYes, RCAmount: 10, CreatedAt: 1000})

	pool := s.Get("prop-1")
	if pool == nil {
		t.Fatalf("expected the pool to spring into existence on the first infusion")
	}
	if pool.YesPoolRC != 10 || pool.NoPoolRC != 0 {
		t.Fatalf("expected YesPoolRC=10 NoPoolRC=0, got yes=%d no=%d", pool.YesPoolRC, pool.NoPoolRC)
	}
	if len(pool.Infusions) != 1 {
		t.Fatalf("expected exactly 1 recorded infusion, got %d", len(pool.Infusions))
	}
}

func TestPredictionPoolStore_AccumulatesBothSides(t *testing.T) {
	s := NewPredictionPoolStore()

	s.Infuse(&PredictionInfusion{ProposalID: "prop-2", Address: "addr-a", Side: PredictionSideYes, RCAmount: 10, CreatedAt: 1000})
	s.Infuse(&PredictionInfusion{ProposalID: "prop-2", Address: "addr-b", Side: PredictionSideYes, RCAmount: 5, CreatedAt: 1001})
	s.Infuse(&PredictionInfusion{ProposalID: "prop-2", Address: "addr-c", Side: PredictionSideNo, RCAmount: 20, CreatedAt: 1002})

	pool := s.Get("prop-2")
	if pool.YesPoolRC != 15 {
		t.Fatalf("expected YesPoolRC=15 (10+5), got %d", pool.YesPoolRC)
	}
	if pool.NoPoolRC != 20 {
		t.Fatalf("expected NoPoolRC=20, got %d", pool.NoPoolRC)
	}
	if len(pool.Infusions) != 3 {
		t.Fatalf("expected 3 recorded infusions, got %d", len(pool.Infusions))
	}
}

func TestPredictionPoolStore_SnapshotRestoreRoundTrips(t *testing.T) {
	s := NewPredictionPoolStore()
	s.Infuse(&PredictionInfusion{ProposalID: "prop-3", Address: "addr-a", Side: PredictionSideNo, RCAmount: 7, CreatedAt: 1000})

	snap := s.Snapshot()

	s2 := NewPredictionPoolStore()
	s2.Restore(snap)

	pool := s2.Get("prop-3")
	if pool == nil || pool.NoPoolRC != 7 {
		t.Fatalf("expected the restored store to have prop-3 with NoPoolRC=7, got %+v", pool)
	}
}

func TestCurrentPoolPriceScaled_ZeroForEmptyPool(t *testing.T) {
	pool := NewLiquidityPool("BTCTEST")
	if got := CurrentPoolPriceScaled(pool); got != 0 {
		t.Fatalf("expected 0 for an empty/untraded pool, got %d", got)
	}
	if got := CurrentPoolPriceScaled(nil); got != 0 {
		t.Fatalf("expected 0 for a nil pool, got %d", got)
	}
}
