package core

import "testing"

// TestValidatorStoreList_DeterministicAcrossInsertionOrder is the
// regression test for audit finding C-02's first half: List() used to
// range directly over the Validators map, whose iteration order Go
// deliberately randomizes - two nodes holding byte-identical validator
// state could still build differently-ordered slices, and since
// selectFrom (consensus.go) walks its pool in order, a different order
// can select a different leader from the same previous-block hash.
// This builds the same validator set via several different insertion
// orders and confirms List() always comes back identically sorted.
func TestValidatorStoreList_DeterministicAcrossInsertionOrder(t *testing.T) {
	addresses := []string{"cc-charlie", "cc-alpha", "cc-echo", "cc-bravo", "cc-delta"}

	insertionOrders := [][]string{
		{"cc-charlie", "cc-alpha", "cc-echo", "cc-bravo", "cc-delta"},
		{"cc-delta", "cc-bravo", "cc-echo", "cc-alpha", "cc-charlie"},
		{"cc-alpha", "cc-bravo", "cc-charlie", "cc-delta", "cc-echo"},
	}

	var want []string
	for _, order := range insertionOrders {
		vs := NewValidatorStore()
		for _, addr := range order {
			vs.Register(addr, addr+"-node", addr+"-pubkey")
		}

		list := vs.List()
		if len(list) != len(addresses) {
			t.Fatalf("expected %d validators, got %d", len(addresses), len(list))
		}

		got := make([]string, len(list))
		for i, v := range list {
			got[i] = v.Address
		}

		if want == nil {
			want = got
			continue
		}

		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("List() order depends on insertion order: first insertion order produced %v, this one produced %v", want, got)
			}
		}
	}

	for i := 1; i < len(want); i++ {
		if want[i-1] >= want[i] {
			t.Fatalf("expected List() sorted ascending by address, got %v", want)
		}
	}
}
