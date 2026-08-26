package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/creditchain/creditchaind/core"
)

const forkMonitorInterval = 60 * time.Second

// forkStatus is this node's own live view of whether it and its
// peers agree on the chain - checked continuously in the background,
// not just when someone happens to compare them by hand (which is
// how the last fork was found - by accident, during a deploy). Every
// node in the network runs this independently, so losing any single
// node doesn't blind the whole network to a fork.
var forkStatus = struct {
	mu          sync.Mutex
	healthy     bool
	lastChecked time.Time
	details     string
}{healthy: true}

func setForkStatus(healthy bool, details string) {
	forkStatus.mu.Lock()
	defer forkStatus.mu.Unlock()
	forkStatus.healthy = healthy
	forkStatus.lastChecked = time.Now()
	forkStatus.details = details
}

// recordForkEvent is the single chokepoint for "this node just found a
// real fork" - whether noticed by the periodic monitor above or by
// syncChainFromPeer refusing a diverging chain in the moment (see
// api/peer.go). Both call sites used to just fmt.Println and, in
// syncChainFromPeer's case, not even update forkStatus - meaning a
// fork discovered mid-sync stayed invisible on /health/sync until the
// monitor's own next 60s tick happened to also notice it. This
// closes that gap (immediate, not eventually-consistent) and adds a
// durable audit trail + optional email alert - deliberately NOT an
// attempt at automated recovery (see core/replace_chain.go's own
// disclosed reasoning for why this codebase refuses to guess at that)
// - this is purely "make sure a human finds out fast," the same
// stopgap role /health/sync already plays, just wired to fire the
// instant a fork is found rather than only on the next poll.
func recordForkEvent(details string) {
	setForkStatus(false, details)
	fmt.Println("⚠", details)

	line := fmt.Sprintf("%s\t%s\n", time.Now().UTC().Format(time.RFC3339), details)
	f, err := os.OpenFile(dataPath("fork_events.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("fork event: could not open fork_events.log:", err)
	} else {
		if _, err := f.WriteString(line); err != nil {
			fmt.Println("fork event: could not write fork_events.log:", err)
		}
		f.Close()
	}

	to := os.Getenv("FORK_ALERT_EMAIL")
	if to != "" && smtpConfigured() {
		go func() {
			if err := sendEmail(to, "CreditChain: fork detected", details+"\n\nThis will not self-resolve; needs manual intervention (see ops/NODE_OPERATOR.md and audit/HARDENING_HISTORY.md for the resolution process used for prior forks)."); err != nil {
				fmt.Println("fork event: failed to send alert email:", err)
			}
		}()
	}
}

// startForkMonitor periodically compares this node's chain against
// every known peer's, using the same CommonPrefixLen logic the real
// sync path (syncChainFromPeer) uses to tell "peer is just
// ahead/behind" apart from "peer has actually forked from us." A
// length or hash mismatch alone isn't a fork - two chains that share
// every block up to the shorter one's length are just at different
// heights, completely normal for a live network. A fork is when they
// diverge at a shared index - different content for the same block
// number, which should never happen and means the two nodes will
// keep drifying apart forever without human intervention (see
// syncChainFromPeer's doc comment for why this codebase refuses to
// auto-resolve that rather than risk corrupting state).
func startForkMonitor() {
	go func() {
		ticker := time.NewTicker(forkMonitorInterval)
		defer ticker.Stop()

		for range ticker.C {
			checkForFork()
		}
	}()
}

func checkForFork() {
	peers := peerStore.List()
	if len(peers) == 0 {
		setForkStatus(true, "no peers configured")
		return
	}

	localBlocks := append([]core.Block{}, blockchain.Blocks...)

	for _, p := range peers {
		client := http.Client{Timeout: 8 * time.Second}
		resp, err := client.Get(p.Address + "/chain")
		if err != nil {
			fmt.Println("fork monitor: could not reach peer", p.Address, "-", err)
			continue
		}

		var peerBlocks []core.Block
		decodeErr := json.NewDecoder(resp.Body).Decode(&peerBlocks)
		resp.Body.Close()

		if decodeErr != nil || len(peerBlocks) == 0 {
			fmt.Println("fork monitor: invalid response from peer", p.Address)
			continue
		}

		prefix := core.CommonPrefixLen(localBlocks, peerBlocks)
		shorter := len(localBlocks)
		if len(peerBlocks) < shorter {
			shorter = len(peerBlocks)
		}

		if prefix < shorter {
			details := fmt.Sprintf(
				"FORK DETECTED vs peer %s (%s) at block index %d - local hash %s, peer hash %s. "+
					"This will not self-resolve; needs manual intervention.",
				p.ID, p.Address, prefix, localBlocks[prefix].Hash, peerBlocks[prefix].Hash,
			)
			recordForkEvent(details)
			return
		}
	}

	setForkStatus(true, fmt.Sprintf("in sync with %d peer(s)", len(peers)))
}

// HandleHealth (registered at /health/sync - /health itself is
// already a basic liveness check) reports this node's own
// chain-sync status - poll this from outside (a script, a monitoring
// dashboard, or just periodic curl) to catch a fork within minutes
// instead of stumbling onto it by accident.
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	forkStatus.mu.Lock()
	healthy := forkStatus.healthy
	lastChecked := forkStatus.lastChecked
	details := forkStatus.details
	forkStatus.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]any{
		"chainHealthy": healthy,
		"lastChecked":  lastChecked,
		"details":      details,
		"blockHeight":  len(blockchain.Blocks),
	})
}
