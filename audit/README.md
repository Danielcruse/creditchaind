# CreditChain — Pre-Audit Package

This directory is the audit-preparation package referenced as "Phase 5" in
this project's production-readiness plan. It exists so a third-party
security audit can start reviewing the actual protocol logic on day one,
instead of spending billable time reconstructing context that already
exists.

**This package does not replace a professional audit.** It is preparation
for one. Nothing in here should be read as a claim that the system has
been independently verified as secure.

## Contents

1. **`ARCHITECTURE.md`** — what the system is, how it's laid out, the
   transaction-authenticity model, consensus/networking model, the token
   model, the CCT order book, and a pointer to the five areas an auditor
   should look at first.
2. **`HARDENING_HISTORY.md`** — a chronological record of the security work
   already done, including the real production incidents this testnet
   phase has produced (chain forks, a supply-drift bug, and node2's
   OOM-driven fork/instability cycle — see `KNOWN_GAPS.md`) and exactly
   what each one caused to be fixed.
3. **`KNOWN_GAPS.md`** — every deliberate simplification, limitation, or
   unfinished path currently disclosed in the codebase, consolidated from
   ~30 files' worth of scattered doc comments into one place, plus a
   handful of known-but-unfixed bugs tracked outside the code.
4. **`THREAT_MODEL.md`** — actors, assets, attack surfaces, current
   mitigations, and residual risk per surface, cross-referenced against the
   other three documents.

## Test suite (referenced, not duplicated here)

- `core/request_auth_test.go` — signed-envelope verification (9 tests)
- `core/p2p_offer_test.go` — EXTERNAL settlement state machine (23 tests)
- `core/replace_chain_test.go` — fork-resolution primitives (12 tests)
- `core/order_book_test.go` — order-matching algorithm: price-time
  priority, self-trade prevention, partial fills, determinism (10 tests)
- `core/interest_test.go` — price-adjusted borrow interest rate (6 tests)
- `api/apply_test.go` — money-movement core (11 tests)
- `api/fees_test.go` — CCT transfer fee + registration fees (7 tests)
- `api/order_book_test.go` — order-book settlement/escrow, price
  improvement refund, cancel-releases-escrow (5 tests)
- `api/hub_operator_test.go` — multi-operator inventory isolation (1 test)
- `api/hub_agents_test.go` — agent request routing (3 tests)
- `api/eth_signer_test.go` / `api/eth_localdev_signer_test.go` — ETH
  custodial signing (3 tests)

Run `go test ./...` from the repo root; `go build ./...` and `go vet ./...`
should also be run as a baseline before any review.

## What's genuinely NOT ready and shouldn't be treated as in-scope yet

- **Mainnet ETH custody.** The Sepolia-testnet-only ETH custodial corner
  *is* built (real AWS KMS-backed signing key per account — see
  `ARCHITECTURE.md` §10) but is explicitly and deliberately scoped away
  from mainnet (the RPC layer refuses to enable at all against a
  non-Sepolia chain ID). Moving any of this to real value needs its own
  dedicated review pass, not a retrofit onto this package.
- **Postgres migration (Phase 3) — not yet wired up, but no longer
  capacity-blocked.** The dual-write code is ready (`api/db.go`); it
  wasn't wired up while node1 was on the smallest Lightsail RAM tier
  (`free -h` showed ~50Mi available, some in swap), since installing
  Postgres there would have risked the same OOM/fork failure mode already
  seen on node2. Node1 has since been resized to 4GB RAM with real
  headroom (~2.3GB available) — see `KNOWN_GAPS.md` for the full resize
  writeup and a firewall hazard worth remembering for any repeat of it.
  Wiring up `DATABASE_URL` is now a real to-do, not a blocked one.
- **Monitoring/alerting (Phase 4) — partially done, external pieces
  explicitly declined.** The local health-check (process/disk/stall/fork
  detection) is deployed and confirmed active on node1. An external
  heartbeat monitor and CDN/WAF were both offered with exact setup steps
  and declined — both require third-party account creation, which this
  assistant doesn't do on a user's behalf. Neither is a code-security item
  either way; both are relevant to a broader production-readiness review,
  not a narrow protocol-security audit.
- The unresolved AWS credential-boundary question in `KNOWN_GAPS.md`'s
  Infrastructure section — worth resolving before an infra-focused review
  specifically, separate from the application-code audit this package is
  mainly prepared for.
- **Node2 (13.40.131.231)** is currently unreachable and, per explicit
  instruction, is not being investigated as part of ordinary session
  work — its chain state has drifted from node1's (missing the order
  book and CCT fee changes entirely as of this writing). Any review
  involving live two-node behavior should confirm current node2 status
  first rather than assuming the historical convergence testing still
  reflects today's production topology. See `KNOWN_GAPS.md`.
