package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
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

// nodeAdmissionEnforced gates whether HandlePeerHandshake actually
// REJECTS an unadmitted public key, vs. only logging what it would
// have done - see the node-admission plan's own recommended log-only
// sub-phase before flipping this on for real, given a bug here could
// disconnect every legitimate peer at once. Deliberately the OPPOSITE
// default direction from networkSecret()'s fail-closed convention:
// this feature's risk is in enforcing too early on live infrastructure,
// not in being open too long, so it defaults to log-only (false) until
// explicitly turned on, not the reverse.
func nodeAdmissionEnforced() bool {
	return os.Getenv("NODE_ADMISSION_ENFORCE_HANDSHAKE") == "true"
}

type handshakeMsg struct {
	NodeID    string `json:"nodeId"`
	PublicKey string `json:"publicKey"`
	Address   string `json:"address"`

	// Timestamp/Signature prove the caller genuinely holds the private
	// key matching the claimed PublicKey - see handshakeCanonicalMessage/
	// signHandshake/verifyHandshakeSignature. Before this,
	// HandlePeerHandshake accepted ANY NodeID/PublicKey pair a
	// NETWORK_SECRET holder claimed, unconditionally recording it via
	// peerStore.Add - letting an attacker impersonate a specific known
	// identity (e.g. a real validator's public NodeID) in every node's
	// peer list with zero proof. This alone doesn't defend consensus -
	// verifyBlockProducer (this file) deliberately never trusts
	// peerStore's PublicKey for signature verification, checking
	// ValidatorStore's own on-chain-verified AuthorizedNodePublicKey
	// instead - it defends the peer-list/gossip layer itself against
	// cheap identity spoofing.
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`

	// KnownPeers lets both sides of a handshake learn about each
	// other's OTHER peers, not just each other - the actual mechanism
	// behind dynamic peer discovery (see discoverPeers). Without this,
	// the network's topology could only ever be exactly what's listed
	// in every node's own static PEERS config, and a new node could
	// never be introduced to the network by just one other node
	// vouching for it.
	KnownPeers []core.Peer `json:"knownPeers"`
}

// handshakeCanonicalMessage is exactly what gets signed/verified - see
// core.SignedEnvelope.CanonicalMessage's identical reasoning for why
// byte-for-byte reconstruction of the claimed fields (not a hash of
// separately-trusted state) is what the signature actually has to
// bind to.
func handshakeCanonicalMessage(nodeID, publicKeyHex, address string, timestamp int64) []byte {
	return []byte(nodeID + "|" + publicKeyHex + "|" + address + "|" + strconv.FormatInt(timestamp, 10))
}

func signHandshake(nodeID, publicKeyHex, address string, timestamp int64) string {
	sig := core.SignMessage(nodeIdentity.PrivateKey, handshakeCanonicalMessage(nodeID, publicKeyHex, address, timestamp))
	return hex.EncodeToString(sig)
}

// verifyHandshakeSignature proves the caller genuinely holds the
// private key for the PublicKey it claims to be presenting - same
// ±60s freshness window every other signed request in this codebase
// uses (core.RequestTimestampWindow), so a captured/replayed handshake
// can't be reused indefinitely either. Does NOT prove the claimed
// NodeID is "genuine" in any deeper sense (NodeID is an
// operator-chosen label here, not derived from the key) - that's why
// verifyBlockProducer never trusts peerStore for anything
// consensus-critical regardless of this check passing.
func verifyHandshakeSignature(msg handshakeMsg) bool {
	age := time.Since(time.Unix(msg.Timestamp, 0))
	if age < -core.RequestTimestampWindow || age > core.RequestTimestampWindow {
		return false
	}

	pub, err := hex.DecodeString(msg.PublicKey)
	if err != nil {
		return false
	}
	sig, err := hex.DecodeString(msg.Signature)
	if err != nil {
		return false
	}

	return core.VerifySignature(pub, handshakeCanonicalMessage(msg.NodeID, msg.PublicKey, msg.Address, msg.Timestamp), sig)
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
	timestamp := time.Now().Unix()
	publicKeyHex := hex.EncodeToString(nodeIdentity.PublicKey)
	msg := handshakeMsg{
		NodeID:     config.NodeID,
		PublicKey:  publicKeyHex,
		Address:    selfBaseURL,
		Timestamp:  timestamp,
		Signature:  signHandshake(config.NodeID, publicKeyHex, selfBaseURL, timestamp),
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
	if !verifyHandshakeSignature(reply) {
		fmt.Println("handshake reply from", addr, "failed signature verification - not adding as peer")
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
		if !verifyHandshakeSignature(msg) {
			http.Error(w, "handshake signature invalid or stale", http.StatusUnauthorized)
			return
		}
		if !nodeAdmissionStore.IsAdmitted(msg.PublicKey) {
			if nodeAdmissionEnforced() {
				fmt.Printf("node admission (ENFORCED): rejected handshake from unadmitted public key %s (NodeID %q) from %s\n", msg.PublicKey, msg.NodeID, r.RemoteAddr)
				http.Error(w, "node not admitted - requires a passed NODE_ADMISSION proposal (or the grandfather seed list)", http.StatusForbidden)
				return
			}
			// Log-only sub-phase (see the node-admission plan's own
			// recommendation): observe what enforcement WOULD reject
			// against real production traffic before actually turning
			// it on - NODE_ADMISSION_ENFORCE_HANDSHAKE unset/false never
			// rejects here, only logs.
			fmt.Printf("node admission (log-only): would reject handshake from unadmitted public key %s (NodeID %q) - set NODE_ADMISSION_ENFORCE_HANDSHAKE=true to actually enforce\n", msg.PublicKey, msg.NodeID)
		}
		peerStore.Add(core.Peer{ID: msg.NodeID, Address: msg.Address, PublicKey: msg.PublicKey})
		discoverPeers(msg.KnownPeers)
	}

	timestamp := time.Now().Unix()
	publicKeyHex := hex.EncodeToString(nodeIdentity.PublicKey)
	json.NewEncoder(w).Encode(handshakeMsg{
		NodeID:     config.NodeID,
		PublicKey:  publicKeyHex,
		Address:    selfBaseURL,
		Timestamp:  timestamp,
		Signature:  signHandshake(config.NodeID, publicKeyHex, selfBaseURL, timestamp),
		KnownPeers: peerStore.List(),
	})
}

// HandlePeerTx used to insert any deserialized transaction from a
// party holding NETWORK_SECRET straight into the mempool with zero
// signature or semantic validation - audit finding M-01. The shared
// secret proves the caller is a handshook peer, never that the
// transaction it's relaying is genuinely signed, well-formed, or even
// plausible: an attacker with that secret (or a peer relaying
// something it never itself validated) could continually fill the
// FIFO mempool with invalid/future-dated transactions, evicting
// legitimate pending ones and burning block capacity on garbage that
// only fails once a producer gets around to including it. Every
// admitted transaction now has to pass the exact same authentication
// verifyTxAuth (api/apply.go) will independently re-run at apply time -
// rejecting here, before admission, means a forged transaction never
// occupies mempool space at all - plus a future-timestamp bound so a
// transaction can't dodge Prune's expiry by claiming to be from later
// than it plausibly could be.
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

	if !core.ValidateMempoolTimestamp(tx, time.Now().Unix()) {
		fmt.Println("rejected peer transaction with implausible future timestamp:", tx.ID)
		http.Error(w, "Timestamp too far in the future", http.StatusBadRequest)
		return
	}

	if !verifyTxAuth(tx) {
		fmt.Println("rejected peer transaction with invalid/mismatched authentication:", tx.ID, tx.Type)
		http.Error(w, "Invalid transaction authentication", http.StatusBadRequest)
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

		if !verifyLeaderOrFallback(block, latest) {
			fmt.Println("rejected block", block.Index, "- producer", block.Validator, "was not the elected leader and the missed-block fallback window has not elapsed")
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
// Verified against ValidatorStore's own AuthorizedNodeID/
// AuthorizedNodePublicKey - set ONLY via a cryptographically-verified
// on-chain TxValidatorRegister (core.ValidatorStore.Register, called
// from applyValidatorRegister, which independently re-checks the
// registration was actually signed by the validator address's own
// key - see verifyTxAuth's TxValidatorRegister case, api/apply.go).
// Deliberately NEVER falls back to peerStore's self-asserted PublicKey
// for this check (an earlier version of this function did, and it was
// a real hole): HandlePeerHandshake accepts any NodeID/PublicKey a
// NETWORK_SECRET holder claims - now with a proof-of-possession
// signature (see verifyHandshakeSignature), but still no proof the
// claimed NodeID itself is genuine, since NodeID is an operator-chosen
// label, not derived from the key. Trusting peerStore here would let
// an attacker who merely knows a real validator's public
// AuthorizedNodeID (visible via GET /validators) self-sign a forged
// block, hand-craft a handshake claiming that same NodeID under their
// OWN PublicKey, and have the forged block wrongly verify against it.
// Anchoring to ValidatorStore instead closes that off entirely - the
// only way to become a validator's AuthorizedNodePublicKey is for that
// validator's own wallet key to sign a TxValidatorRegister naming it.
func verifyBlockProducer(block core.Block) bool {
	if nonAddressValidators[block.Validator] {
		// No ValidatorStore entry exists for genesis / the legacy
		// "mine" placeholder - every node mints its own genesis
		// locally (never gossiped from a peer), so the only
		// legitimate producer for one of these is this node itself.
		return block.ProducerNodeID == config.NodeID &&
			core.VerifyBlockSignature(block, nodeIdentity.PublicKey, block.Signature)
	}

	status, ok := validatorStore.Get(block.Validator)
	if !ok || status.AuthorizedNodeID != block.ProducerNodeID {
		return false
	}

	pub, err := hex.DecodeString(status.AuthorizedNodePublicKey)
	if err != nil {
		return false
	}
	return core.VerifyBlockSignature(block, pub, block.Signature)
}

// verifyLeaderOrFallback closes audit finding C-02's second half:
// verifyBlockProducer above only proves the block is genuinely signed
// by a validator's own authorized node - it says nothing about
// whether that validator was actually the one PoSR selection elected
// for this round. Without this, any registered validator could
// produce arbitrary consecutive blocks out of turn, collecting every
// reward and pre-empting the real leader. This recomputes the same
// deterministic selection every honest node reaches (now that
// core.ValidatorStore.List sorts by address instead of relying on Go's
// randomized map order) and requires an exact match for the normal
// case.
//
// An out-of-turn block is only ever legitimate as the missed-block
// liveness fallback (see consensus_loop.go's startConsensusLoop/
// localAuthorizedValidator) - production intentionally does not
// re-run the weighted draw for that path, so this can't check for a
// SPECIFIC expected fallback producer, only that the fallback
// condition itself is real. That's verified against this node's own
// wall clock, never the block's self-reported Timestamp field: a
// producer fully controls Timestamp before signing (it's part of
// core.ComputeBlockHash's preimage), so trusting the block's own claim
// about how much time has passed would let an attacker simply assert
// the fallback window elapsed and produce out-of-turn immediately -
// exactly the attack this function exists to close. Requiring THIS
// node's independently-ticking clock to agree the window has passed
// means a forged early timestamp gains an attacker nothing.
func verifyLeaderOrFallback(block, previous core.Block) bool {
	if nonAddressValidators[block.Validator] {
		return true // genesis/bootstrap placeholder - no PoSR leader exists to compare against
	}

	if leader := currentLeader(); leader != nil && leader.Wallet.Address == block.Validator {
		return true
	}

	fallbackWindowSeconds := int64(missedRoundsBeforeFallback) * int64(consensusTickInterval.Seconds())
	return time.Now().Unix()-previous.Timestamp >= fallbackWindowSeconds
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
		// the block list without the state to match it. recordForkEvent
		// (fork_monitor.go) makes this visible on /health/sync
		// immediately, rather than only on the periodic monitor's next
		// tick - a refused reorg here means this node is now stuck
		// behind, which needs the same manual intervention as any other
		// detected fork.
		details := fmt.Sprintf(
			"refusing chain from %s - diverges from an already-applied block at index %d (reorg-with-rollback not supported)",
			addr, prefix,
		)
		recordForkEvent(details)
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

		prevBlock := blockchain.LatestBlock()

		if !blockWithinLimits(b, prevBlock) {
			fmt.Println("aborting chain sync from", addr, "- block", b.Index, "exceeds size or timestamp limits")
			peerStore.RecordFailure(addr)
			return
		}

		if !verifyLeaderOrFallback(b, prevBlock) {
			fmt.Println("aborting chain sync from", addr, "- block", b.Index, "producer", b.Validator, "was not the elected leader and the missed-block fallback window has not elapsed")
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
