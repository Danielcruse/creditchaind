# CreditChain — Security & Documentation Audit Report

**Date:** 2026-08-23 to 2026-08-24
**Scope:** Live mainnet (`3.39.124.210` / `service.creditchain.site`) and
node1/testnet (`3.11.72.137` / `creditchain.site`), plus this repository's
own audit-preparation documentation.
**Method:** Direct, independent verification against running systems —
SSH into both hosts, reading actual systemd unit files and live journald
output, `curl` against both public domains, and `grep`/build/test against
the current source tree. Nothing here was accepted from prior
documentation without re-checking it against the system it describes.
**Status of this document:** an internal review, not a substitute for the
professional third-party audit tracked in `audit/full-scale-audit-program.md`.

---

## Summary

| # | Finding | Severity | Status |
|---|---|---|---|
| 1 | `audit/README.md` claimed mainnet ETH custody was "scoped away from mainnet" — false, it's live | 🟠 High (documentation) | Fixed, corrected in place |
| 2 | `audit/README.md` claimed Postgres dual-write was "not yet wired up" — false, it's live | 🟡 Medium (documentation) | Fixed, corrected in place |
| 3 | Test suite counts in `audit/README.md` had drifted, some by 2x+, and 7 real test files (60 tests) were undocumented | 🟢 Low (documentation) | Fixed, recounted and added |
| 4 | `website/security.html`'s peer-admission section described a system that had already been replaced (shared-secret-only → governance-gated) | 🟡 Medium (documentation, user-facing) | Fixed, rewritten to match current mechanism |
| 5 | Security disclosures page was live but only linked from FAQ — easy to miss | 🟢 Low (product/UX) | Addressed, then reverted per explicit instruction — see "Decisions," below |
| 6 | Submitting AWS credentials via the admin panel only ever activated ETH custodial KMS signing, silently leaving the BTC vault signer on its insecure in-memory fallback | 🟠 High (real custody gap) | Fixed and deployed; activation is a pending operator action |
| 7 | No prior data existed on this network's real request-handling capacity under load | — (informational) | Measured; see "Load Test Results" |
| 8 | External review (separate archive, see "External Audit Cross-Check" below) — 10 of 12 findings already fixed in this tree, 2 genuinely open | Mixed | 10 verified already-fixed; 2 fixed this pass — see below |
| — | Bridge vault contract's on-chain `threshold()` = 1 | 🔴 Critical | **Confirmed still open — not addressed this pass.** See `audit/KNOWN_GAPS.md`. |

---

## 8. External audit cross-check (separate review, 2026-08-24)

An independently-produced report (`creditchain-security-audit-2026-08-24.md`,
same filename, different location — a separate working tree at
`C:\Users\User\Documents\Codex\2026-08-11\fix\`) surfaced mid-session,
listing 12 findings (6 Critical, 4 High, 2 Medium) and concluding the
codebase was "not suitable for a value-bearing or mainnet deployment."
Every finding was checked directly against this repository's current
source, not accepted at face value.

**10 of 12 were already fixed**, each independently confirmed by reading
the actual code (not by trusting a comment alone): forgeable
node-authored reports (bound to `isAuthorizedNodeIdentity`), nondeterministic
leader election (deterministic `sort.Slice` ordering + `verifyLeaderOrFallback`
enforced at block adoption), stored XSS (both cited locations use
`escapeHtml`/`escapeHtmlMultiline`), wallet recovery bypass (`mainnetMode()`
check in `applyRecoverWallet`), vault attestation front-running (identity-bound
attestation IDs, conflict rejection), block hash not committing to
timestamp (`tx.Timestamp == tx.AuthTimestamp` enforced), a valid tx ID
being permanently consumable by an invalid transaction (replay key only
recorded after `verifyTxAuth` passes), mempool DoS via authenticated
peers (`HandlePeerTx` now validates timestamp and full auth before
insertion), a world-readable node identity key (written `0600`, with an
active `chmod` migration for pre-existing `0644` files), and duplicate
Solidity validator addresses (explicit zero/duplicate rejection in the
shared validator-list helper). The external report's own cited line
numbers (e.g. `api/apply.go:5843-5864` for code that sits at line 6706
in this tree) indicate it was run against an older, pre-remediation
archive — almost certainly the same source that produced this project's
own `creditchain-audit-initial-findings.md` (2026-08-12), already
documented as remediated in `audit/creditchain-audit-2-scope.md`.

**2 of 12 were genuinely open and have been fixed this pass:**

- **Consensus-critical config lived only in per-node environment
  variables** (admin wallet address, admin multisig signers/threshold,
  bridge validator addresses/threshold, mainnet mode) — confirmed via
  direct `os.Getenv` reads with no cross-node consistency guarantee.
  Two differently-configured nodes could apply the identical block
  differently (e.g. disagreeing on whether a bridge release approval
  had reached "enough" signatures).
- **No single, fail-closed "what's active on mainnet" record** —
  `mainnetMode()` was checked ad hoc at individual risk points rather
  than from one committed source of truth.

**Fix**: `core.ProtocolManifest` (`core/protocol_manifest.go`) — a
consensus-replicated struct committing all of the above, written via a
new admin-authed transaction type (`TxSetProtocolManifest`,
`api/protocol_manifest.go`) and mirroring `core.ActivatedProtocolVersion`'s
established precedent exactly (plain mutable core-package var, one
apply-time writer, dedicated `ChainSnapshot` persistence field). Every
affected config getter (`adminWalletAddress`, `adminMultisigSigners`,
`adminMultisigThreshold`, `bridgeValidatorAddresses`,
`bridgeValidatorThreshold`, `mainnetMode`) now reads the manifest once
it's been committed, falling back to the local environment variable
only in the pre-manifest bootstrap state — genuine cross-node
consistency, not merely a detectable mismatch.

Deliberately did **not** reuse the generic `verifyAdminTxAuth` fallback
for authorizing this new transaction type: that function, by design,
accepts any signed transaction once neither an admin wallet nor
multisig is configured — acceptable for an ordinary admin action, but
would let any wallet self-appoint as admin on a freshly-deployed node
for *this* transaction type specifically, since it decides who the
admin even is. A dedicated `verifySetProtocolManifestAuth` closes that:
requires a genuinely pre-configured admin wallet for the first commit,
and once a manifest exists, only the address *the manifest itself*
names as admin may change it further — self-referential authority, the
same way a real access-control list can only be edited by someone
already on it. This gap was caught by a test written to prove the
opposite (that the generic fallback correctly rejected an
unconfigured signer) — it didn't, which is what prompted the dedicated
function.

10 new tests (`api/protocol_manifest_test.go`): manifest commit writes
every field correctly, every getter falls back to env pre-manifest,
every getter uses the manifest (not env) post-manifest even when they
disagree, admin-auth accepted/rejected at both the transaction and HTTP
layers, the self-appoint rejection, self-referential-authority once
set, and persistence survives a simulated restart. Full
`go build`/`go vet`/`go test ./...` + linux/amd64 cross-compile clean.

## 1–2. `audit/README.md`: two false claims about live production state

Read fresh, without relying on this session's own earlier context, and
checked against the actual running systems rather than trusted as
written.

**ETH custody claim.** The document read: *"the Sepolia-testnet-only ETH
custodial corner... is explicitly and deliberately scoped away from
mainnet (the RPC layer refuses to enable at all against a non-Sepolia
chain ID)."* Verified false: mainnet's own systemd unit sets
`ETH_EXPECTED_CHAIN_ID=1` (real Ethereum, not Sepolia), and the running
process's own startup log confirms `ETH RPC connected: chain ID 1` with
`ETH custodial signer: AWS KMS (production-grade key custody)` — not the
local-dev fallback the document implied. The chain-ID gate
(`api/eth_rpc.go`'s `InitEthRPC`) is real and functions correctly; it's
simply that mainnet has deliberately configured itself to pass it
against real Ethereum. Real, KMS-custodied ETH signing has been live and
in-scope for review this whole time — the document said otherwise.

**Postgres claim.** The document read: *"Postgres migration (Phase
3) — not yet wired up."* Verified false via the same live log:
`Postgres dual-write enabled for node: mainnet`.

**Fix applied:** both claims corrected in place in `audit/README.md`,
with a dated "Corrections" section added explaining what was wrong and
how it was verified, so the correction itself doesn't silently
re-decay into unverified fact over time.

## 3. Stale and missing test-suite documentation

`audit/README.md`'s test-suite list had drifted significantly from the
actual repository state:

- `api/apply_test.go`: documented as 11 tests, actually 25.
- `core/p2p_offer_test.go`: documented as 23, actually 27.
- `api/fees_test.go`: documented as 7, actually 9.
- `api/order_book_test.go`: documented as 5, actually 7.
- Seven real test files — `core/trust_test.go`, `api/contact_email_test.go`,
  `api/trust_cutover_test.go`, `core/node_admission_test.go`,
  `api/bridge_validator_posr_eligibility_test.go`, `api/vault_bridge_test.go`,
  `cmd/staticserve/main_test.go` — existed in the repository (60 tests
  combined) but were entirely absent from the document.

**Fix applied:** recounted every listed file via `grep -c "^func Test"`
against the actual source, corrected all figures, and added the seven
missing entries.

## 4. `website/security.html`: peer-admission disclosure described a retired mechanism

The live disclosure page (confirmed reachable on both networks, not
taken down) still read: *"Nodes join this network's peer-to-peer gossip
layer using a shared network secret rather than an economic or
reputation-based admission cost."* That was the mechanism before this
session's own node-admission work; it has since been replaced with a
governance-gated system (`core/node_admission.go` — a passed
`NODE_ADMISSION` community vote is now required, publicly attributing a
sponsor address and a proposal ID, with a small number of
already-connected nodes grandfathered in once and visibly marked as
such). Confirmed `NODE_ADMISSION_ENFORCE_HANDSHAKE=true` is actively set
and enforced on both node1 and mainnet.

**Fix applied:** rewrote the section to describe the actual current
mechanism, including the honest limitation that this is real progress on
accountability but not a complete Sybil-resistance guarantee (governance
voting power itself isn't a hard economic barrier to acquire).

## 5. Security disclosures page visibility — addressed, then reverted

The page was confirmed live and unmodified-in-substance on both
networks throughout (never taken down), but was only ever linked from
the FAQ page — no homepage nav entry, no sitewide footer link (the
footer link code existed in `website/js/api.js` but was gated to
mainnet-only, so testnet visitors never saw it at all).

Initial fix added a homepage nav link and an unconditional sitewide
footer link (fixing the mainnet-only gating bug in the process). **This
was explicitly reverted per direct instruction** after deployment: the
user asked that known weaknesses not be surfaced to users, was informed
this assistant would not remove the underlying disclosure of a real,
active fund-safety risk (the vault threshold issue, item — below) while
it remains unfixed, and the agreed middle ground was reducing
*prominence* rather than removing the *content*. Current, final state:
no homepage nav link, no sitewide footer link, FAQ link only — matching
the page's original placement before this pass began. The page itself,
and everything it discloses, remains fully intact and reachable by
direct URL and via FAQ.

## 6. BTC vault KMS activation gap

`api/vault_btc_signer.go`'s production KMS-backed signer was already
built (confirmed via source and via mainnet's own startup log still
reporting `BTC vault signer: LOCAL DEV FALLBACK`), but nothing ever
activated it: `HandleAdminSetAWSCredentials` (`api/admin_aws_credentials.go`)
only ever wrote `ETH_CUSTODIAL_KMS_ENABLED=true` to the server-local
credentials file, never `BTC_VAULT_KMS_ENABLED=true` — even though both
signers use the identical secp256k1 KMS key type and the same
CreateKey/GetPublicKey/Sign calls, so one credential legitimately
activates both.

**Fix applied:** the handler now writes both flags; `website/admin.html`'s
credentials card updated to describe both signers and to note the IAM
policy must allow `kms:CreateKey` broadly (not scoped to one existing
key ARN), since the BTC signer provisions its own fresh KMS key on first
use. Four new tests (`api/admin_aws_credentials_test.go`): both flags
written, non-admin caller rejected, missing-field rejected, secret never
echoed in the response. Full `go build`/`go vet`/`go test ./...` +
linux/amd64 cross-compile clean. Deployed to node1 and mainnet, verified
live (`/admin/aws-credentials/set` reachable and correctly 403s an
unauthenticated request on both).

**Not yet active on mainnet.** Activation requires re-submitting the
existing AWS credentials once through the mainnet admin panel (the
credential value itself is never entered by this assistant, per standing
practice) and restarting `creditchain-mainnet.service`. Pending operator
action.

## 7. Load test results (informational, not a finding)

Built a dedicated, read-only load-testing tool (`cmd/loadtest`) and ran
it against node1 from this development environment.

| Concurrency | Req/sec | P50 | P95 | P99 | Max | Errors |
|---|---|---|---|---|---|---|
| 1 | 3.4 | 294ms | 362ms | 381ms | 444ms | 0% |
| 5 | 16.8 | 294ms | 421ms | 436ms | 451ms | 0% |
| 10 | 32.8 | 295ms | 431ms | 466ms | 496ms | 0% |
| 25 | 83.8 | 291ms | 431ms | 509ms | 527ms | 0% |
| 50 | 160.6 | 301ms | 467ms | 504ms | 516ms | 0% |
| 100 | 272.4 | 329ms | 590ms | 737ms | 3325ms | 0.28% |

The ~290–300ms floor present even at concurrency 1 is network
round-trip distance between the test origin and node1, not node1's own
processing time — not representative of a real user's actual latency.
The meaningful signal is the trend: throughput scales cleanly with zero
errors through 50 concurrent requests; the first sign of stress (a
0.28% error rate and a 3.3-second max-latency outlier) appears at 100
concurrent. Re-running from a location closer to the server (or from
mainnet's own region) is needed for a latency figure that can actually
be trusted; this run is sufficient to say the current single-machine
setup has real, if modest, headroom before ~100 concurrent connections,
and that this specific data point should be re-collected as real load
grows rather than treated as a permanent ceiling.

**Note found in passing, not this pass's focus:** a first invocation of
this tool from a Git Bash shell silently corrupted its own `/health`
argument into a Windows path (`C:/Program Files/Git/health`) before it
reached the program, producing a nonsensical result (a physically
impossible 27,449 req/sec row) that was caught and discarded before
being reported. Re-run via PowerShell, which does not have this
behavior. Documented here only so the same artifact isn't mistaken for
a real finding if this tool is run again from a similar shell.

---

## Confirmed correct — not just what was wrong

In the interest of an honest report, not just a list of problems: the
majority of `website/security.html`'s content was independently
re-verified as accurate on this pass, including the vault threshold
disclosure itself, the $10,000 deposit cap explanation, the BTC vault
signer status, the relayer-wallet-funding dependency, the no-rollback-
from-deep-fork disclosure, and the economic-parameters-still-early-stage
note. `audit/ARCHITECTURE.md` §10's ETH custody description held up
under direct re-verification. The audit-preparation package's core
structure and most of its content were sound; the two false claims in
§1–2 above were real exceptions, not representative of the whole
document.

---

## Explicitly not addressed this pass

- **Bridge vault contract's on-chain `threshold()` = 1** — confirmed
  still open, still the single highest-priority item across every
  document in this repository's audit material. Not touched this pass;
  see `audit/KNOWN_GAPS.md` and `audit/full-scale-audit-program.md` §3
  for full detail and remediation options under discussion.
- Admin/bridge-validator key custody hardening (hardware wallet /
  secrets manager) — flagged as a prerequisite for mobile-app-scale
  distribution, not started.
- Emergency pause mechanism on the vault contract — flagged as needing
  its own dedicated design/plan given the contract has no upgradeability
  today; not started.
- Economic constants tuning — a business decision, not started.
- Second-validator infrastructure redundancy — needs a genuinely
  separate operator/machine; not started.
