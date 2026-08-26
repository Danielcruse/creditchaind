// loadtest is a read-only HTTP load-testing tool for a running
// creditchaind node - built to answer a real question (what's this
// network's actual capacity under concurrent load?) rather than
// guessing. Deliberately GET-only: it never signs or submits a
// transaction, so it can't spend real CCT, spam the mempool, or mutate
// any wallet's real state - the risk profile of hammering a node this
// way is "the node gets slow," never "something moves that shouldn't."
//
// Ramps through increasing concurrency levels against a fixed set of
// endpoints, running each level for a fixed duration, and reports
// throughput/latency/error-rate per level - the ramp is what surfaces
// where things actually start to degrade, rather than a single
// concurrency number that could sit comfortably under or blindly past
// the real limit.
//
// -target has NO default on purpose: pointing this at the wrong host by
// accident (a live mainnet node with real users on it) is exactly the
// mistake this tool should make structurally hard to make, not just
// warn about.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("target", "", "base URL of the node to test, e.g. http://3.11.72.137:8080 (REQUIRED, no default - never point this at a node with real users/value on it)")
	endpoints := flag.String("endpoints", "/health,/network", "comma-separated list of GET-only endpoint paths to test")
	levels := flag.String("levels", "1,5,10,25,50,100", "comma-separated concurrency levels to ramp through")
	perLevel := flag.Duration("per-level", 10*time.Second, "how long to run each concurrency level")
	flag.Parse()

	if *target == "" {
		fmt.Fprintln(os.Stderr, "ERROR: -target is required and has no default. Example:")
		fmt.Fprintln(os.Stderr, "  go run ./cmd/loadtest -target http://3.11.72.137:8080")
		fmt.Fprintln(os.Stderr, "Never point this at a node carrying real users or real value.")
		os.Exit(1)
	}

	eps := strings.Split(*endpoints, ",")
	for i := range eps {
		eps[i] = strings.TrimSpace(eps[i])
	}

	var concurrencyLevels []int
	for _, s := range strings.Split(*levels, ",") {
		var n int
		fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
		if n > 0 {
			concurrencyLevels = append(concurrencyLevels, n)
		}
	}

	fmt.Printf("loadtest: target=%s endpoints=%v per-level=%s\n\n", *target, eps, *perLevel)
	fmt.Printf("%-12s %-10s %-10s %-10s %-10s %-10s %-8s\n", "CONCURRENCY", "REQ/SEC", "P50(ms)", "P95(ms)", "P99(ms)", "MAX(ms)", "ERR%")

	client := &http.Client{Timeout: 10 * time.Second}

	for _, c := range concurrencyLevels {
		result := runLevel(client, *target, eps, c, *perLevel)
		fmt.Printf("%-12d %-10.1f %-10.1f %-10.1f %-10.1f %-10.1f %-8.2f\n",
			c, result.reqPerSec, result.p50Ms, result.p95Ms, result.p99Ms, result.maxMs, result.errPct)
	}
}

type levelResult struct {
	reqPerSec           float64
	p50Ms, p95Ms, p99Ms float64
	maxMs               float64
	errPct              float64
}

func runLevel(client *http.Client, target string, endpoints []string, concurrency int, duration time.Duration) levelResult {
	var wg sync.WaitGroup
	var successCount, errCount int64
	var mu sync.Mutex
	var latenciesMs []float64

	stop := make(chan struct{})
	time.AfterFunc(duration, func() { close(stop) })

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			i := workerID
			for {
				select {
				case <-stop:
					return
				default:
				}

				path := endpoints[i%len(endpoints)]
				i++

				start := time.Now()
				resp, err := client.Get(target + path)
				elapsed := time.Since(start).Seconds() * 1000

				if err != nil {
					atomic.AddInt64(&errCount, 1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				if resp.StatusCode >= 500 {
					atomic.AddInt64(&errCount, 1)
				} else {
					atomic.AddInt64(&successCount, 1)
					mu.Lock()
					latenciesMs = append(latenciesMs, elapsed)
					mu.Unlock()
				}
			}
		}(w)
	}

	wg.Wait()

	total := successCount + errCount
	result := levelResult{
		reqPerSec: float64(total) / duration.Seconds(),
	}
	if total > 0 {
		result.errPct = float64(errCount) / float64(total) * 100
	}
	if len(latenciesMs) > 0 {
		sort.Float64s(latenciesMs)
		result.p50Ms = percentile(latenciesMs, 50)
		result.p95Ms = percentile(latenciesMs, 95)
		result.p99Ms = percentile(latenciesMs, 99)
		result.maxMs = latenciesMs[len(latenciesMs)-1]
	}
	return result
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100)
	return sorted[idx]
}
