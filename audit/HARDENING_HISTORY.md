# CreditChain — Security Hardening History

This is a narrative record of the security-relevant work done on this
codebase, in roughly chronological order, so an auditor can see not just
the current state but *what problem each piece of hardening was actually
responding to*. Where a pass was a direct response to a real production
incident, that's called out explicitly — those are the highest-signal
data points for judging what's actually been battle-tested versus
theoretically hardened.

## 1. Request-level signature authentication (foundational)

**Starting point**: any HTTP caller could act as any wallet address with
zero proof of key ownership; `/token/mint` was open and unlimited; peer
gossip endpoints accepted input from anyone; the server spoke plain HTTP
only; there was no rate limiting; and a wallet's real public key was never
actually stored server-side in a usable way.

**What was built**:
- `core/request_auth.go`'s `SignedEnvelope` + `Verify()` — self-certifying
  signed requests (address = `SHA256(publicKey)[:20]`, no server-side key
  registry needed), with a ±60s freshness window and full signature
  verification.
- `api/auth_middleware.go`'s `requireSignedRequest` — the shared entry point
  every mutating handler now calls instead of a naked JSON decode, plus a
  short-lived replay cache so an intercepted valid envelope can't be
  resubmitted.
- Applied to every mutating endpoint (stake, borrow, repay, fund, P2P
  offers, governance, token transfer, validator registration/production,
  collateral lock — see the endpoint-by-endpoint task record).
- `/token/mint` gated behind an `ADMIN_TOKEN` (constant-time compare)
  instead of being open to the public internet.
- `/peer/*` gossip endpoints gated behind a shared `NETWORK_SECRET`
  (constant-time compare) — closing the "anyone can inject fake gossip"
  hole.
- TLS support (`TLS_CERT_FILE`/`TLS_KEY_FILE`, falls back to HTTP with an
  explicit startup warning if unset).
- Per-IP token-bucket rate limiting in the CORS middleware chokepoint.
- Input validation sweep: consistent zero-amount rejection, string-length
  caps on user text fields, `http.MaxBytesReader` request-body cap.
- Frontend: `website/js/api.js`'s `apiPostSigned` signs every mutating
  request client-side using the browser's native `crypto.subtle` Ed25519
  support (a deliberate choice over vendoring a third-party JS crypto
  library for the one thing that authenticates real money).

**Verified**: curl-level tests confirming a stripped/corrupted/wrong-key
signature is rejected with 401, a stale timestamp is rejected, and an
identical valid envelope replayed twice is rejected the second time;
two-node convergence re-confirmed with auth enabled; full browser
click-through with signed requests.

## 2. Closing the "signature valid, effect altered" gap

**The subtler problem this session found**: request-level signing alone
doesn't stop a malicious block producer from taking a validly-signed
transaction and altering its actual effect (amount, recipient) before
including it in a block — the original client's signature covers the
*request*, not necessarily whatever a producer chooses to write into the
block.

**What was built**: `core.Transaction` now carries a full copy of the
original signed envelope in its own `Auth*` fields, so *any* node applying
a block — not just the one that first received the request — can
independently re-derive and re-verify that a real signature authorized
exactly this economic effect. `core.VerifyTransactionAuth` does the crypto
check; `api/apply.go`'s `verifyTxAuth` does a second, transaction-type-
specific check (does the claimed amount/recipient/etc. match what was
actually signed).

**Verified**: `core/request_auth_test.go` (9 tests: valid pass, tampered
payload, wrong signer, address/key mismatch, stale/future timestamp,
`VerifySignatureOnly` correctly ignoring staleness for historical replay,
malformed encoding) and `api/apply_test.go` (amount-mismatch and
recipient-mismatch between signed payload and claimed transaction effect
both correctly rejected).

## 3. Validator-to-node binding (closing an impersonation gap)

**The gap**: `consensus_loop.go`'s original leader-authorization check was
just "do I recognize this validator address," not "am I the specific node
this validator's owner actually authorized to produce on their behalf." A
rogue node could produce (and collect relay reward for) blocks claiming to
be a validator it doesn't actually operate.

**What was built**: validator registration now embeds which node the owner
is delegating to (`AuthorizedNodeID`/`AuthorizedNodePublicKey`), asserted
by the receiving node itself (not client-supplied) and only ever changeable
by a fresh signed registration from the validator's own key. Every
production-authorization check — local production gate, manual-nudge
endpoint, and critically `peer.go`'s block-adoption check — now verifies
this binding, not just address recognition.

**Verified**: two-node test explicitly proving node B's hand-crafted block
(correctly signed with node B's own valid key, but claiming to produce for
a validator authorized to node A) is rejected — proving the *authorization*
check, not just the signature check, is what's actually gating adoption.

## 4. Slashing replication (closing a split-brain gap)

**The gap**: the exact same anti-pattern already fixed once for block
rewards — `applyMissedBlockSlash`/`applyDoubleSignSlash` mutated local
state directly, instantly, the moment one node observed a fault, with no
guarantee every other node applied the identical penalty at the identical
time.

**What was built**: a new `TxSlashReport` transaction type, self-signed by
the *reporting node's own node identity* (not a wallet), that goes through
the same gossip/block-confirmation pipeline as every other economic
action. Double-sign reports are independently re-verified from embedded
evidence (both conflicting blocks' hash/signature/producer consistency)
before the penalty is ever applied — a bare claim isn't trusted.
Missed-block reports remain single-reporter-trusted (no cryptographic
proof of absence is possible) — this residual limitation is explicitly
disclosed (see `KNOWN_GAPS.md`) rather than silently accepted as solved.

**Verified**: forced a real missed-block scenario across a live two-node
test setup and confirmed both nodes converged on identical slash
count/reputation for the affected validator — not just the node that
happened to detect it first.

## 5. Per-token sub-addressing (closing a cross-token authorization gap)

**The gap**: ETHTEST/USDCTEST balances originally lived at the same address
as CCT, meaning any action authorized against the CCT address implicitly
had authority over the imported-token balances too — a broader blast
radius than intended for tokens meant to simulate separately-bridged
assets.

**What was built**: `core.DeriveTokenAddress(publicKey, tag)` derives a
distinct sub-address per token per wallet; every balance check/transfer
involving ETHTEST/USDCTEST now resolves through
`api/subaddress.go`'s `resolveBalanceAddress`, which requires the caller's
*actual, currently-authenticated* public key, not merely their CCT address,
to reach the correct sub-address. Applied consistently across pool/swap,
P2P offers, loans, and faucet bridge-in.

**Trade-off accepted, disclosed**: since the server never stores a
wallet's public key beyond the current request, this same design decision
is what makes ETHTEST/USDCTEST balances unreachable during dormant-wallet
sweep, wallet recovery (if the old public key can't be supplied), and
explorer address lookups for a third party. See `KNOWN_GAPS.md`.

## 6. Multi-operator marketplace + hub-panel work (most recent, this
   session)

**What changed**: a Trading Hub's delegated "workers" went from
editing-the-hub's-own-shared-rate to each running a fully independent
listing (`core.HubRate.OperatorAddress`) with its own inventory/balance —
a meaningful expansion of the money-movement surface, since it introduces
a new dimension (which specific listing/inventory a trade settles against)
into code that was previously only ever "trade against the hub itself."

**Hardening applied alongside the feature, not after**:
- `InventoryAddress()`/`InventoryPublicKey` are computed, never
  client-supplied, so an operator can never claim to trade against a
  different inventory than their own.
- Every balance check/transfer in the hub-rate trade path
  (`api/hub_rate.go`, `api/apply.go`) was updated to resolve against
  the correct inventory owner, both at submission-time fast-fail and
  independently again at block-confirmation time.
- `core.P2POffer.HubAddress` was added so a trade against an operator's
  listing still correctly shows as "verified via [parent hub]" without
  incorrectly implying the operator itself is independently verified.
- A dedicated regression test (`api/hub_operator_test.go`) proves the core
  invariant end-to-end: a hub's own listing and an operator's listing for
  the identical symbol coexist as separate records, and a trade against
  either only ever moves that specific listing's own funds — the hub's
  balance is provably untouched by a trade against its operator's listing,
  and vice versa.

## 7. Removing two live unauthenticated-mutation endpoints (Phase 5
   systematic re-audit)

**How found**: a scripted pass over every registered HTTP handler
(`api/server.go`'s full route table, ~137 routes) cross-referenced each
one against whether it called `requireSignedRequest`, an admin/network-
secret check, or was a genuinely read-only GET. Two handlers matched
none of those categories despite mutating live state.

**`HandleCreateLoan` (`POST /loans/create`, `api/loans.go`) — critical,
now removed.** Took a raw, unauthenticated JSON body (`{id, borrower,
amount, interest}`) and wrote a `core.Loan` directly into `loanStore` —
no signature, no `ApplyTransaction`/consensus routing, no gossip. This
was the exact same store `HandleFundLoan` (properly signed) reads from
and pays out to: `HandleFundLoan` verifies the *lender's* signature but
blindly trusts whatever `Borrower` address is already attached to the
loan record it looks up. Chained together, this meant anyone could POST
a fake pending loan attributing an arbitrary `Borrower` address and
`Amount`, and if a real lender was ever led to fund that loan ID (e.g.
via the marketplace listing, which lists straight from `loanStore`), a
genuinely-signed lender transaction would pay out to the attacker's
address — real fund misdirection, not just data pollution. It also
caused the same category of cross-node state divergence already
documented for the old faucet bug (§ below), since the write never went
through gossip.

**`HandleTransaction` (`POST /transactions`, `api/transactions.go`) —
lower severity, also removed.** Built a raw `core.Transaction` from a
caller-supplied `From`/`To`/`Amount`/`Type`/`Data` and added it directly
to the mempool, bypassing the entire signed-envelope model. Verified
this could not actually move funds — `applyTxOnce` calls `verifyTxAuth`
before any transaction type is applied, and `verifyTxAuth`'s `default`
case is `return false`; since this path never populates `AuthAddress`/
`AuthPublicKey`/`AuthPayload`, every case in that switch would fail the
same way. The real exposure was mempool/gossip spam capacity (anyone
could inject arbitrary garbage transactions that would circulate before
being rejected at apply-time), not fund theft — but it's dead code with
zero legitimate callers (confirmed via a repo-wide grep — no website JS,
no ops tooling, no other Go code referenced either route) sitting in a
codebase that otherwise enforces signed requests everywhere, exactly the
kind of inconsistency an audit should not have to find.

**Root cause, both cases**: pre-authentication-era handlers (written
before the signed-envelope model in §1 existed) that were never removed
once the real, authenticated replacement systems shipped — self-funded
`applyBorrow`/the `LoanOffer` marketplace for loans, `recordTx` +
`ApplyTransaction` for general transaction submission. Both routes and
their handler functions were deleted outright (`api/transactions.go`
removed entirely; `HandleCreateLoan`/`CreateLoanRequest` removed from
`api/loans.go`, keeping the legitimate read-only `HandleGetLoans` which
`website/js/borrow.js` actually uses) rather than retrofitted with auth,
since neither had a legitimate remaining use case that the proper
systems didn't already cover. `go build`/`vet`/`test` clean after
removal; no functionality lost (confirmed zero real callers first).

**Everything else the same scan flagged was confirmed legitimate, not a
gap**: `HandlePeerHandshake`/`HandlePeerTx`/`HandlePeerBlock` are gated
by `requireNetworkSecret` (a different but equally real auth check, just
not the wallet-signature kind); `HandleRequestRecoveryCode` is
intentionally unauthenticated by design — a lost-key recovery flow
cannot require a signature from the key that's already lost — and its
weaker (email-code) trust model is already disclosed in
`KNOWN_GAPS.md`; `HandleCreateWallet` is open by design (wallet
creation is the point at which a keypair first comes into existence);
`stakingStore.SyncWallet` calls inside read-path handlers recompute
derived display state from already-authoritative `stakingStore.Stakes`,
they don't accept new input.

## 8. Production incidents actually encountered (not hypothetical)

For calibrating how much confidence to place in the consensus/fork-
resolution hardening above, these are the real incidents this testnet
phase has already produced, both requiring manual operator resolution:

1. **First production fork** (node2 diverged from node1) — resolved by
   syncing node2 to node1's chain. Root cause was a signature/authorization
   gap in fork-resolution logic at the time, since patched (§ above and
   `KNOWN_GAPS.md`).
2. **Second production fork**, at a specific known block index — root cause
   was a race in the fallback/recovery-block production path (the
   node-restart-triggers-spurious-recovery-block problem described in
   `api/consensus_loop.go`'s doc comments); fixed by raising the
   missed-rounds threshold and adding a "sync before falling back to
   self-production" step.
3. **CCT supply drift between the two nodes** (13,740 vs. 13,240 observed
   at one point) — root cause was the original faucet implementation
   minting instantly and node-locally, bypassing the gossip/block-
   confirmation pipeline entirely. Fixed by making faucet claims an
   ordinary signed transaction like any other. The historical drift itself
   was deliberately left unreconciled on production (see `KNOWN_GAPS.md`)
   — a decision, not an oversight.

## 9. Test coverage added specifically to prevent regressions in the above

- `core/request_auth_test.go` — 9 tests, signed-envelope verification.
- `core/p2p_offer_test.go` — 17 tests, the full EXTERNAL settlement state
  machine (accept/cancel race exclusivity, dispute + moderator resolve both
  directions, authorization checks per role, ordering guards, ONCHAIN
  offers correctly excluded from the EXTERNAL-only flow).
- `core/replace_chain_test.go` — 12 tests, `ValidateChain` and
  `CommonPrefixLen` (the fork-resolution primitives directly implicated in
  both production forks above).
- `api/apply_test.go` — 6 tests, the money-movement core (valid transfer,
  duplicate-tx-ID and duplicate-block dedup, tampered signature, amount/
  recipient mismatch between signed and claimed effect).
- `api/hub_operator_test.go` — 1 focused end-to-end test proving operator/
  hub inventory isolation (§6 above).

Before this session's testing pass, the entire Go codebase (~197 files at
the time) had exactly one test file. This is a real, disclosed prior gap
(see `KNOWN_GAPS.md`'s framing of what audit-readiness requires) — the
current suite covers the highest-risk paths identified, not the whole
surface area.
