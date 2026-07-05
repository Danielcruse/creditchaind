package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var selfBaseURL string

const peerHTTPTimeout = 3 * time.Second

const networkSecretHeader = "X-Network-Secret"

// networkSecret gates every /peer/* endpoint - without it, this
// would be an open relay anyone on the internet could feed forged
// transactions/blocks into, not just nodes that actually handshook.
// Fails closed like ADMIN_TOKEN: unset means P2P is unreachable
// entirely rather than silently permissionless.
func networkSecret() string {
	return os.Getenv("NETWORK_SECRET")
}

func requireNetworkSecret(r *http.Request) bool {
	expected := networkSecret()
	if expected == "" {
		return false
	}

	got := r.Header.Get(networkSecretHeader)
	if got == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

type handshakeMsg struct {
	NodeID    string `json:"nodeId"`
	PublicKey string `json:"publicKey"`
	Address   string `json:"address"`

	// KnownPeers lets both sides of a handshake learn about each
	// other's OTHER peers, not just each other - the actual mechanism
	// behind dynamic peer discovery (see discoverPeers). Without this,
	// the network's topology could only ever be exactly what's listed
	// in every node's own static PEERS config, and a new node could
	// never be introduced to the network by just one other node
	// vouching for it.
	KnownPeers []core.Peer `json:"knownPeers"`
}

// peerDiscoveryInterval is how often a node re-handshakes with every
// peer it already knows (see startPeerDiscoveryLoop) - both a
// liveness probe (a peer that stops responding accumulates failures
// via RecordFailure and eventually gets suspended) and a fresh
// opportunity to learn about any peers-of-peers introduced since the
// last round.
const peerDiscoveryInterval = 60 * time.Second

// loadPeersFromConfig dials every peer listed in config.json's
// "peers" field (or the PEERS env var, comma-separated, which takes
// precedence so multiple local node processes can be wired together
// without editing files) and exchanges identities with each. This is
// only the SEED list - startPeerDiscoveryLoop is what lets the
// network's actual topology grow beyond it.
func loadPeersFromConfig() {
	selfBaseURL = os.Getenv("SELF_URL")

	peers := config.Peers

	if raw := os.Getenv("PEERS"); raw != "" {
		peers = strings.Split(raw, ",")
	}

	for _, addr := range peers {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		go handshakeWith(addr)
	}

	startPeerDiscoveryLoop()
}

// startPeerDiscoveryLoop periodically re-handshakes with every
// currently known peer (including suspended ones, so they get a
// genuine chance to recover - see PeerStore.List vs ActiveList). Each
// round both refreshes liveness and exchanges peer lists, so a node
// gradually learns the full reachable network rather than being
// permanently limited to its own static seed list.
func startPeerDiscoveryLoop() {
	go func() {
		ticker := time.NewTicker(peerDiscoveryInterval)
		defer ticker.Stop()

		for range ticker.C {
			for _, p := range peerStore.List() {
				go handshakeWith(p.Address)
			}
		}
	}()
}

// discoverPeers attempts a fresh handshake with any candidate address
// this node doesn't already know about - the actual expansion step of
// peer discovery. Skips addresses already known (handshaking them
// again happens on its own schedule via startPeerDiscoveryLoop, not
// redundantly here) and this node's own address.
func discoverPeers(candidates []core.Peer) {
	for _, p := range candidates {
		if p.Address == "" || p.Address == selfBaseURL {
			continue
		}
		if _, known := peerStore.Get(p.Address); known {
			continue
		}
		go handshakeWith(p.Address)
	}
}

func handshakeWith(addr string) {
	msg := handshakeMsg{
		NodeID:     config.NodeID,
		PublicKey:  hex.EncodeToString(nodeIdentity.PublicKey),
		Address:    selfBaseURL,
		KnownPeers: peerStore.List(),
	}

	body, _ := json.Marshal(msg)

	resp, err := postToPeer(addr+"/peer/handshake", body)
	if err != nil {
		fmt.Println("handshake with", addr, "failed:", err)
		peerStore.RecordFailure(addr)
		return
	}
	defer resp.Body.Close()

	var reply handshakeMsg
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		peerStore.RecordFailure(addr)
		return
	}

	peerStore.Add(core.Peer{ID: reply.NodeID, Address: addr, PublicKey: reply.PublicKey})
	discoverPeers(reply.KnownPeers)

	// A fresh peer may already be ahead of us - catch up immediately
	// rather than waiting for the next gossiped block.
	go syncChainFromPeer(addr)
}

func HandlePeerHandshake(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if !requireNetworkSecret(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var msg handshakeMsg
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if msg.Address != "" {
		peerStore.Add(core.Peer{ID: msg.NodeID, Address: msg.Address, PublicKey: msg.PublicKey})
		discoverPeers(msg.KnownPeers)
	}

	json.NewEncoder(w).Encode(handshakeMsg{
		NodeID:     config.NodeID,
		PublicKey:  hex.EncodeToString(nodeIdentity.PublicKey),
		Address:    selfBaseURL,
		KnownPeers: peerStore.List(),
	})
}

func HandlePeerTx(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if !requireNetworkSecret(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var tx core.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if mempool.Has(tx.ID) {
		json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
		return
	}

	mempool.Add(tx)
	go gossipTx(tx)

	json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
}

func HandlePeerBlock(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if !requireNetworkSecret(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var block core.Block
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	accepted := adoptBlock(block)

	json.NewEncoder(w).Encode(map[string]bool{"accepted": accepted})
}

func HandlePeerChain(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if !requireNetworkSecret(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	json.NewEncoder(w).Encode(blockchain.Blocks)
}

// blockWithinLimits gates every block a node RECEIVES (gossiped or
// pulled during chain sync) against the two structural bounds this
// node enforces on its own production too (see consensus_loop.go's
// maxTransactionsPerBlock and core.NewBlock's timestamp) - checking
// them here is what makes them real limits rather than something a
// misbehaving peer could simply ignore.
func blockWithinLimits(block, previous core.Block) bool {
	if len(block.Transactions) > maxTransactionsPerBlock {
		return false
	}
	return core.ValidateBlockTimestamp(block, previous)
}

// adoptBlock verifies and applies a single gossiped block if it
// extends our current chain tip, and detects double-signing (a
// conflicting block already exists at the same height). Anything
// more complex than "the very next block" falls back to a full
// chain sync from the sender.
func adoptBlock(block core.Block) bool {
	if !verifyBlockProducer(block) {
		fmt.Println("rejected block from unverifiable producer:", block.ProducerNodeID)
		return false
	}

	latest := blockchain.LatestBlock()

	if block.Index <= latest.Index {
		if block.Index < uint64(len(blockchain.Blocks)) {
			existing := blockchain.Blocks[block.Index]
			if existing.Hash != block.Hash {
				reportDoubleSign(existing, block)
			}
		}
		return false
	}

	if block.Index == latest.Index+1 && block.PreviousHash == latest.Hash && core.ComputeBlockHash(block) == block.Hash {
		if !blockWithinLimits(block, latest) {
			fmt.Println("rejected block", block.Index, "- exceeds size or timestamp limits")
			return false
		}

		blockchain.Blocks = append(blockchain.Blocks, block)
		ApplyBlock(block)
		removeFromMempool(block.Transactions)
		_ = blockchain.Save(dataPath("blockchain.json"))
		_ = SaveState() // see consensus_loop.go's produceBlockAsLeader for why this matters here too
		go gossipBlock(block)
		return true
	}

	// Out of order or we're behind - pull the sender's full chain and
	// let the longest-valid-chain rule decide.
	for _, p := range peerStore.List() {
		if p.ID == block.ProducerNodeID {
			go syncChainFromPeer(p.Address)
			break
		}
	}

	return false
}

// verifyBlockProducer checks two separate things that are both
// required: (1) the block's signature is genuinely valid for the
// node it claims to come from, and (2) - the part a signature alone
// doesn't prove - that node is actually the one block.Validator's
// key-holder authorized to produce on their behalf. A rogue node has
// a perfectly valid signing key of its OWN; that doesn't entitle it
// to produce for somebody else's validator.
func verifyBlockProducer(block core.Block) bool {
	if !isAuthorizedProducer(block.Validator, block.ProducerNodeID) {
		return false
	}

	for _, p := range peerStore.List() {
		if p.ID == block.ProducerNodeID {
			pub, err := hex.DecodeString(p.PublicKey)
			if err != nil {
				return false
			}
			return core.VerifyBlockSignature(block, pub, block.Signature)
		}
	}

	// Self-produced blocks (this node is both producer and the one
	// calling adoptBlock, e.g. via chain sync) verify against our own key.
	if block.ProducerNodeID == config.NodeID {
		return core.VerifyBlockSignature(block, nodeIdentity.PublicKey, block.Signature)
	}

	return false
}

// isAuthorizedProducer reports whether nodeID is the specific node
// validatorAddress's key-holder delegated block-production to.
// Non-address producer placeholders (genesis, the bootstrap "mine"
// path) have no ValidatorStore entry and aren't gated by this at all.
func isAuthorizedProducer(validatorAddress, nodeID string) bool {
	if nonAddressValidators[validatorAddress] {
		return true
	}

	status, ok := validatorStore.Get(validatorAddress)
	if !ok {
		return false
	}

	return status.AuthorizedNodeID == nodeID
}

// reportDoubleSign submits a slash report carrying both conflicting
// blocks as self-contained evidence, instead of mutating state
// directly - applySlashReport (api/apply.go) independently re-derives
// the fraud proof from the evidence alone before the penalty applies,
// and every node that confirms the report reaches the same result,
// not just whichever node happened to receive both blocks first.
func reportDoubleSign(existing, conflicting core.Block) {
	data, _ := json.Marshal(slashReportData{
		Reason:           "double_sign",
		ValidatorAddress: existing.Validator,
		Evidence:         &doubleSignEvidence{BlockA: existing, BlockB: conflicting},
	})

	fmt.Println("reporting double-sign for", existing.Validator, "at index", existing.Index)

	recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxSlashReport, string(data))
}

// syncChainFromPeer pulls a peer's full chain and, if it's longer
// than ours, adopts the new blocks - but only ones we don't already
// have (see below for why), and only after independently verifying
// each one's producer, not just trusting that the sender's chain is
// internally hash-linked.
func syncChainFromPeer(addr string) {
	req, err := http.NewRequest(http.MethodGet, addr+"/peer/chain", nil)
	if err != nil {
		return
	}
	req.Header.Set(networkSecretHeader, networkSecret())

	client := http.Client{Timeout: peerHTTPTimeout}

	resp, err := client.Do(req)
	if err != nil {
		peerStore.RecordFailure(addr)
		return
	}
	defer resp.Body.Close()

	var blocks []core.Block
	if err := json.NewDecoder(resp.Body).Decode(&blocks); err != nil {
		peerStore.RecordFailure(addr)
		return
	}

	if len(blocks) <= len(blockchain.Blocks) {
		peerStore.RecordSuccess(addr) // reachable and responded validly - just nothing new
		return
	}

	// Hash-linking alone only proves the candidate is internally
	// self-consistent, not that a real, authorized validator produced
	// any of it - a dishonest peer can self-sign an entirely
	// fabricated but hash-consistent chain with its own keys. Every
	// new block also needs to pass the same producer verification a
	// single gossiped block gets (verifyBlockProducer) - see the loop
	// below for why that has to happen one block at a time rather
	// than as a single up-front pass.
	if !core.ValidateChain(blocks) {
		peerStore.RecordFailure(addr)
		return
	}

	prefix := core.CommonPrefixLen(blockchain.Blocks, blocks)
	if prefix < len(blockchain.Blocks) {
		// The candidate diverges from a block we've already applied -
		// a reorg. Adopting it would mean rolling back the state
		// changes our now-orphaned blocks already made, which this
		// codebase doesn't do yet (a disclosed fork-resolution gap -
		// simple longest-chain-wins isn't fully BFT-robust). Refuse
		// rather than risk silently corrupting state by only replacing
		// the block list without the state to match it.
		fmt.Println("refusing chain from", addr, "- diverges from an already-applied block (reorg-with-rollback not supported)")
		return
	}

	// Verify and apply the new suffix one block at a time, exactly
	// like normal block-by-block confirmation (adoptBlock) - NOT all
	// at once against a stale snapshot. A block's producer
	// authorization can depend on a validator-registration
	// transaction earlier in this SAME candidate suffix (e.g. a
	// freshly-registered validator's very next block); checking
	// everything up front, before any of those registrations are
	// actually applied, would incorrectly reject a perfectly
	// legitimate catch-up sync.
	for i := prefix; i < len(blocks); i++ {
		b := blocks[i]

		if !verifyBlockProducer(b) {
			fmt.Println("aborting chain sync from", addr, "- block", b.Index, "failed producer verification")
			peerStore.RecordFailure(addr)
			return
		}

		if !blockWithinLimits(b, blockchain.LatestBlock()) {
			fmt.Println("aborting chain sync from", addr, "- block", b.Index, "exceeds size or timestamp limits")
			peerStore.RecordFailure(addr)
			return
		}

		blockchain.Blocks = append(blockchain.Blocks, b)
		ApplyBlock(b)
		removeFromMempool(b.Transactions)
	}

	_ = blockchain.Save(dataPath("blockchain.json"))
	_ = SaveState() // this is exactly the resync path that exposed the double-apply bug - always persist right after

	peerStore.RecordSuccess(addr)
}

func removeFromMempool(confirmed []core.Transaction) {
	confirmedIDs := make(map[string]bool, len(confirmed))
	for _, tx := range confirmed {
		confirmedIDs[tx.ID] = true
	}

	kept := make([]core.Transaction, 0, len(mempool.Transactions))
	for _, tx := range mempool.Transactions {
		if !confirmedIDs[tx.ID] {
			kept = append(kept, tx)
		}
	}
	mempool.Transactions = kept
}

// postToPeer sends a POST to a peer with the network secret attached
// - the shared way every outgoing /peer/* call authenticates itself.
func postToPeer(url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(networkSecretHeader, networkSecret())

	client := http.Client{Timeout: peerHTTPTimeout}
	return client.Do(req)
}

func gossipTx(tx core.Transaction) {
	body, _ := json.Marshal(tx)

	for _, p := range peerStore.ActiveList() {
		go func(addr string) {
			resp, err := postToPeer(addr+"/peer/tx", body)
			if err != nil {
				peerStore.RecordFailure(addr)
				return
			}
			resp.Body.Close()
			peerStore.RecordSuccess(addr)
		}(p.Address)
	}
}

func gossipBlock(block core.Block) {
	body, _ := json.Marshal(block)

	for _, p := range peerStore.ActiveList() {
		go func(addr string) {
			resp, err := postToPeer(addr+"/peer/block", body)
			if err != nil {
				peerStore.RecordFailure(addr)
				return
			}
			resp.Body.Close()
			peerStore.RecordSuccess(addr)
		}(p.Address)
	}
}
