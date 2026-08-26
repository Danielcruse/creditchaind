package core

import "testing"

// validChain builds n blocks (including genesis) that are internally
// consistent - each linked to the previous by hash, each Hash actually
// matching ComputeBlockHash for its own content.
func validChain(n int) []Block {
	blocks := []Block{NewGenesisBlock()}
	for i := 1; i < n; i++ {
		blocks = append(blocks, NewBlock(uint64(i), blocks[i-1].Hash, "validator", nil))
	}
	return blocks
}

func TestValidateChain_EmptyFails(t *testing.T) {
	if ValidateChain(nil) {
		t.Fatal("expected an empty chain to be invalid")
	}
}

func TestValidateChain_GenesisOnlyPasses(t *testing.T) {
	if !ValidateChain([]Block{NewGenesisBlock()}) {
		t.Fatal("expected a lone valid genesis block to pass")
	}
}

func TestValidateChain_WrongGenesisIndexFails(t *testing.T) {
	g := NewGenesisBlock()
	g.Index = 1
	if ValidateChain([]Block{g}) {
		t.Fatal("expected a genesis block with the wrong index to fail")
	}
}

func TestValidateChain_WrongGenesisHashFails(t *testing.T) {
	g := NewGenesisBlock()
	g.Hash = "not-genesis"
	if ValidateChain([]Block{g}) {
		t.Fatal("expected a genesis block without the GENESIS sentinel hash to fail")
	}
}

func TestValidateChain_ValidMultiBlockChainPasses(t *testing.T) {
	if !ValidateChain(validChain(5)) {
		t.Fatal("expected a properly linked 5-block chain to pass")
	}
}

func TestValidateChain_NonSequentialIndexFails(t *testing.T) {
	blocks := validChain(3)
	blocks[2].Index = 5 // skips an index
	if ValidateChain(blocks) {
		t.Fatal("expected a non-sequential block index to fail validation")
	}
}

func TestValidateChain_BrokenPreviousHashLinkFails(t *testing.T) {
	blocks := validChain(3)
	blocks[2].PreviousHash = "some-unrelated-hash"
	if ValidateChain(blocks) {
		t.Fatal("expected a broken PreviousHash link to fail validation")
	}
}

func TestValidateChain_TamperedBlockContentFails(t *testing.T) {
	blocks := validChain(3)
	// Change the validator after the hash was computed - Hash no
	// longer matches ComputeBlockHash for the block's actual content,
	// simulating a peer that fabricated/altered a block.
	blocks[2].Validator = "attacker"
	if ValidateChain(blocks) {
		t.Fatal("expected a block whose Hash doesn't match its own content to fail validation")
	}
}

func TestCommonPrefixLen_IdenticalChains(t *testing.T) {
	a := validChain(5)
	b := validChain(5)
	// Two independently built "identical" chains only actually share a
	// hash-identical prefix if their content (including timestamps) is
	// literally the same - simulate that by copying, not rebuilding.
	got := CommonPrefixLen(a, a)
	if got != 5 {
		t.Fatalf("expected full-length common prefix of 5, got %d", got)
	}
	_ = b
}

func TestCommonPrefixLen_DivergesPartway(t *testing.T) {
	a := validChain(5)
	b := append([]Block{}, a[:3]...)
	b = append(b, NewBlock(3, a[2].Hash, "different-validator", nil))
	b = append(b, NewBlock(4, b[3].Hash, "validator", nil))

	got := CommonPrefixLen(a, b)
	if got != 3 {
		t.Fatalf("expected common prefix of 3 (diverging at index 3), got %d", got)
	}
}

func TestCommonPrefixLen_NoOverlap(t *testing.T) {
	a := []Block{{Hash: "a"}, {Hash: "b"}}
	b := []Block{{Hash: "x"}, {Hash: "y"}}
	if got := CommonPrefixLen(a, b); got != 0 {
		t.Fatalf("expected 0 common prefix for completely divergent chains, got %d", got)
	}
}

func TestCommonPrefixLen_DifferentLengthsBoundedByShorter(t *testing.T) {
	a := validChain(5)
	b := a[:3]
	if got := CommonPrefixLen(a, b); got != 3 {
		t.Fatalf("expected common prefix bounded by the shorter chain's length (3), got %d", got)
	}
}
