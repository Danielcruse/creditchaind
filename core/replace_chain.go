package core

// ValidateChain checks that a candidate chain is internally
// consistent: every non-genesis block's Hash matches its own content
// (ComputeBlockHash) and links to the previous block's Hash. Genesis
// (index 0) uses the fixed "GENESIS" sentinel rather than a computed
// hash, so it's only checked for position/index.
//
// This alone does NOT prove the chain was actually produced by real,
// authorized validators - it only proves internal self-consistency.
// A dishonest peer can self-sign an entirely fabricated but
// hash-consistent chain with its own keys. Per-block producer
// signature/authorization verification is done separately by the
// caller (see api/peer.go's syncChainFromPeer), interleaved with
// applying each block, since a block's producer-authorization can
// depend on a validator-registration transaction earlier in the very
// same candidate chain - checking it all up front against a stale
// snapshot would incorrectly reject legitimate catch-up syncs.
func ValidateChain(blocks []Block) bool {
	if len(blocks) == 0 {
		return false
	}

	if blocks[0].Index != 0 || blocks[0].Hash != "GENESIS" {
		return false
	}

	for i := 1; i < len(blocks); i++ {
		b := blocks[i]

		if b.Index != blocks[i-1].Index+1 {
			return false
		}

		if b.PreviousHash != blocks[i-1].Hash {
			return false
		}

		if ComputeBlockHash(b) != b.Hash {
			return false
		}
	}

	return true
}

// CommonPrefixLen returns how many leading blocks of a and b are
// identical (compared by Hash, which is itself a function of the
// block's full content - see ComputeBlockHash). Used by fork
// resolution to tell "the candidate simply extends what we already
// have" (safe to adopt incrementally, block by block) apart from "the
// candidate replaces some suffix we already applied" (a reorg, which
// would require rolling back the state changes from our orphaned
// blocks - not implemented, see api/peer.go's syncChainFromPeer).
func CommonPrefixLen(a, b []Block) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	for i := 0; i < n; i++ {
		if a[i].Hash != b[i].Hash {
			return i
		}
	}

	return n
}
