# CreditChain — Pre-Audit Package

*Fact-checked against live mainnet/node1 state and the current source tree
on 2026-08-23 — see the "Corrections" note at the bottom for what changed
and why this matters for anyone who read an earlier version.*

This directory is the audit-preparation package for CreditChain. It exists
so a third-party security audit can start reviewing the actual protocol
logic on day one, instead of spending billable time reconstructing context
that already exists.

**This package does not replace a professional audit.** It is preparation
for one. Nothing in here should be read as a claim that the system has
been independently verified as secure.

**This directory holds preparation material, not audit results.** Actual
dated audit reports — findings from a specific review pass, what was
checked, what was fixed, what remains open — live in `../outputs/`, one
file per pass (e.g. `outputs/creditchain-security-audit-2026-08-24.md`).
Read the documents in *this* directory to get oriented before a review;
read `outputs/` to see what past reviews actually found.

## Contents

0. **`AUDIT_ENGAGEMENT_BRIEF.md`** — start here if you're a prospective
   auditor deciding whether to take this on. A short, standalone cover
   note: current audit status stated plainly, the one Critical finding
   that can't wait for a full engagement, suggested scope, and what
   "audited" will actually mean once this is done. Everything below is
   the deeper material it points into.
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
5. **`STETH_CCC1_COLLATERAL_AUDIT_SCOPE.md`** — focused addendum for one
   feature (STETH_CCC1 as loan collateral, 2026-08-11): where to start
   reading, what to independently re-derive rather than trust, disclosed
   gaps specific to this feature, the test coverage map, and handoff notes
   for the upcoming app migration. Read alongside the four documents above,
   not instead of them.
6. **`full-scale-audit-program.md`** — the actual engagement roadmap
   (2026-08-23): a 9-stage program (Protocol Architecture → Cryptography →
   Bridge → Smart Contracts → Economic → Infrastructure → Penetration
   Testing → Public Testnet → Mainnet Readiness), each stage mapped to the
   exact files/docs in this repo an auditor working that stage should
   start from. Read this first if the engagement is "audit the whole
   system," not one feature - it's the map that points into everything
   else listed here.

## Test suite (referenced, not duplicated here)

Counts below are current as of 2026-08-23 (`grep -c "^func Test"` against
each file) — several had drifted upward significantly from an earlier
version of this document, most notably `apply_test.go` (11 → 25) and
`p2p_offer_test.go` (23 → 27). Re-run the grep yourself before trusting a
specific number for anything beyond a rough sense of coverage depth — this
list itself will drift the same way again as the codebase keeps moving.

- `core/request_auth_test.go` — signed-envelope verification (9 tests)
- `core/p2p_offer_test.go` — EXTERNAL settlement state machine (27 tests)
- `core/replace_chain_test.go` — fork-resolution primitives (12 tests)
- `core/order_book_test.go` — order-matching algorithm: price-time
  priority, self-trade prevention, partial fills, determinism (10 tests)
- `core/interest_test.go` — price-adjusted borrow interest rate (6 tests)
- `core/trust_test.go` — Trust Score Index math: `ComputeTSI`, lazy
  per-dimension seeding, dimension-scoped increase/decrease (8 tests)
- `api/apply_test.go` — money-movement core (25 tests)
- `api/fees_test.go` — CCT transfer fee + registration fees (9 tests)
- `api/order_book_test.go` — order-book settlement/escrow, price
  improvement refund, cancel-releases-escrow (7 tests)
- `api/hub_operator_test.go` — multi-operator inventory isolation (1 test)
- `api/hub_agents_test.go` — agent request routing (3 tests)
- `api/eth_signer_test.go` / `api/eth_localdev_signer_test.go` — ETH
  custodial signing (3 tests)
- `api/trust_cutover_test.go` — flat-Reputation → 6-dimension TSI cutover
  correctness, both sides of the boundary (11 tests)
- `api/contact_email_test.go` — notification-email verification flow,
  signature-binding on both new transaction types (13 tests)
- `core/node_admission_test.go` — governance-gated peer admission (4 tests)
- `api/bridge_validator_posr_eligibility_test.go` — bridge-validator
  eligibility tied to real PoSR validator status (3 tests)
- `api/vault_bridge_test.go` — vault release confirmation-depth gating
  (12 tests)
- `cmd/staticserve/main_test.go` — clean-URL routing, cache-control
  headers (9 tests)

Run `go test ./...` from the repo root; `go build ./...` and `go vet ./...`
should also be run as a baseline before any review.

## What's actually live vs. what's genuinely not ready

**Corrected 2026-08-23 — the two items below were previously (and
incorrectly) listed as "not ready." Both are confirmed live on mainnet
right now, holding or authorizing real value. This is exactly the kind
of drift this whole section exists to prevent, and it had already
happened once.**

- **Mainnet ETH custody is live, not scoped away.** Verified directly
  against the running mainnet process: `ETH_EXPECTED_CHAIN_ID=1` is set
  in `/etc/systemd/system/creditchain-mainnet.service` (real Ethereum
  mainnet, not Sepolia), the node's own startup log confirms `ETH RPC
  connected: chain ID 1`, and the custodial signer is explicitly AWS
  KMS-backed (`ETH custodial signer: AWS KMS (production-grade key
  custody)` — not the local-dev fallback). The chain-ID gate this
  package previously described (`api/eth_rpc.go`'s `InitEthRPC`,
  `expectedEthChainID`) is real and does work as designed — it's just
  that mainnet has deliberately configured itself to pass that gate
  against real Ethereum, not been blocked by it. **This means real
  custodial ETH signing is active and in-scope for review now** — treat
  it as a live surface, not a future one. `HandleCreateEthCustodialAccount`
  (`api/eth_custodial.go`) and the KMS signing path are exactly where to
  start.
- **Postgres dual-write is live on mainnet, not pending.** Confirmed via
  the running process's own startup log: `Postgres dual-write enabled
  for node: mainnet`. `state.json` remains authoritative for reads —
  Postgres is a durability target, not yet a read path — but the "not
  yet wired up" framing this document previously used is no longer
  accurate.
- **Monitoring/alerting** — the local health-check (process/disk/stall/fork
  detection) was confirmed active on node1 as of an earlier check; **not
  independently re-verified on mainnet specifically for this pass** —
  confirm current status on both hosts rather than assuming node1's
  status generalizes. An external heartbeat monitor and CDN/WAF were both
  offered with exact setup steps and declined (third-party account
  creation, outside what this project's own tooling does unassisted) —
  still genuinely not done, this part of the original claim held up.
- The unresolved AWS credential-boundary question in `KNOWN_GAPS.md`'s
  Infrastructure section — worth resolving before an infra-focused review
  specifically, separate from the application-code audit this package is
  mainly prepared for. Not independently re-verified this pass.
- **Node2** — status not independently re-verified for this pass. It was
  last confirmed unreachable, with chain state drifted from node1's, and
  the user has explicitly deprioritized it ("ignore node2 if it has no
  effect on mainnet" — it's testnet-only infrastructure, not part of
  mainnet). Confirm current reachability before any review involving
  live two-node behavior rather than trusting this note or the
  historical convergence testing it refers to. See `KNOWN_GAPS.md`.

## Corrections

This file previously claimed mainnet ETH custody was "explicitly and
deliberately scoped away from mainnet" and that Postgres was "not yet
wired up." Both were true when originally written and are false now —
neither claim had been re-verified against live state before this pass.
Test counts in the section above had also drifted (most significantly,
`api/apply_test.go` had grown from 11 tests to 25 without this document
being updated). None of this reflects a code problem — it reflects this
*document* not being re-verified as the system it describes kept moving.
Treat every specific claim in this package the same way: as accurate at
the time it was written, not guaranteed current, and worth an
independent `grep`/live-check before relying on it for anything that
matters. Live values were verified by connecting directly to node1
(`3.11.72.137`) and mainnet (`3.39.124.210`) via SSH and reading each
service's actual running configuration and startup log — not by
re-reading this project's own prior documentation about itself.
