# CreditChain — Changes Since the 2026-08-15 Audit

Scope document prepared for the next external security review. Covers
everything that's changed since `creditchain-audit-initial-findings.md`
was remediated (phase 1, all 5 Critical / 3 High / 2 Medium findings
fixed, `DepositsHeldForSecurityRemediation` lifted 2026-08-16). Every
item below has a full write-up in `audit/KNOWN_GAPS.md` under the
matching dated section header — this document is a prioritized index
into that detail, not a replacement for it.

Live deployments in scope: mainnet (`3.39.124.210`, real ETH/BTC/USDC
value) and node1/testnet (`3.11.72.137`, no real value).

---

## 1. Highest priority — real, currently unfixed financial risk

### Vault contract's on-chain `threshold()` = 1
**`KNOWN_GAPS.md` → "CRITICAL, STILL OPEN: mainnet vault contract's
on-chain threshold is 1..." (2026-08-19/20)**

Verified directly against the live deployed `CreditChainVault.sol` at
`0xE3C27466943903B390d06Cddb265263091b2d752` via three independent
public RPC providers, not just backend config. Two validators are
registered on-chain; the deployed `threshold()` is 1. Either registered
validator — compromised, dishonest, or physically coerced — can today
unilaterally:
- Sign a `release()` call moving the vault's entire real ETH/USDC
  balance to any address, with zero cooperation from the second
  validator (`require(distinctValid >= threshold, ...)` is satisfied by
  one signature).
- Attest to a deposit that never happened (`TxVaultLockAttest`, same
  `BRIDGE_VALIDATOR_THRESHOLD=1` on the backend side), minting unbacked
  ETH_CCC1/USDC_CCC1/BTC_CCC1.

Current vault balance is small (~0.004 ETH at time of discovery), but
this is a structural configuration gap, not a code bug — it scales
directly with whatever value accumulates in the vault. **This needs
independent audit judgment on remediation priority and approach** —
several mitigations were discussed (raising threshold with genuinely
independent signers, withdrawal delay/veto windows, velocity caps,
duress signals, and a longer-term move to a proof-based/light-client
bridge architecture removing human signers from the mint path
entirely) but none are implemented yet. Full design discussion and a
working proof-of-concept (Helios light client verified live against
mainnet's real endpoint, see §4 below) are recorded in KNOWN_GAPS.md.

---

## 2. New trust boundaries / attack surface introduced since the last audit

These are new code paths that didn't exist at the time of the last
audit and should get first-pass scrutiny for the same class of issue
the original audit was looking for (auth bypass, double-spend,
unbacked minting, replay).

| Change | File(s) | What to check |
|---|---|---|
| New admin action: refund a stuck vault burn | `api/admin_refund_stuck_vault_burn.go`, `core/vault_release_approval.go` (`VaultReleaseRefunded` status) | Moves real value (mints CCC1 back to a burner). Gated by `requireAdminAuthed`/admin multisig. Only reachable for a still-`PENDING` approval — verify this can't be raced/replayed against an approval that's mid-transition to `RELEASED`. |
| New signed tx: connect an existing wallet to a hub | `core/transactions.go` (`TxWalletConnectHub`), `api/wallet_connect_hub.go`, `api/apply.go`'s `applyWalletConnectHub` | Low value-at-stake (UX/recommendation only, not funds), but is a new signed-transaction type — verify `verifyTxAuth` binding is correct (no cross-wallet spoofing of `ConnectedHub`). |
| BTC vault custody moved from local-memory keys toward real KMS | `api/vault_btc_signer.go` (`kmsBtcVaultSigner`) | New signer implementation for real custody. Verify the low-S correction and DER re-encoding logic (`api/vault_btc_signer_test.go` covers this, but independent review of the actual signature-malleability handling is warranted given this now guards real BTC custody once activated). **Not yet activated on mainnet** — `BTC_VAULT_KMS_ENABLED` isn't set yet, still on the local-dev fallback. |
| Hub certificate documents now encrypted at rest | `api/apply.go`'s `applyCompanyUploadCertificate`, reuses `api/hub_kyc.go`'s AES-256-GCM helpers | Was previously plaintext on disk (real gap, now fixed) — verify the key-reuse decision (same `HUB_KYC_ENCRYPTION_KEY_HEX` for both use cases) doesn't create an unintended coupling. |

---

## 3. Fixes to already-audited code — re-review recommended

These touch the vault bridge / release path specifically, which was
central to the original audit's Critical findings (C-01 through C-05).
Even though each is a fix (not new functionality), changes to this
exact code deserve independent re-verification.

- **Stale bridge-validator-signature auto-heal** (`api/vault_lock.go`'s
  `applyVaultReleaseApprove`, `api/vault_release_approve_watch.go`'s
  `scanVaultReleaseApprovalsForAutoSign`) — previously, once a validator
  had ever submitted ANY signature for an approval, a later resubmission
  (e.g. after key rotation) was silently refused, permanently wedging
  that approval. Now allows overwrite. **Reviewer should verify**: this
  doesn't reopen a replay/griefing vector where a validator can
  repeatedly resubmit garbage to keep an approval permanently unresolved,
  and that `tx.AuthAddress` really is independently bound before this
  code runs (it relies on the existing signed-envelope pipeline, not a
  new check).
- **Flat-fee-eats-small-withdrawals fix** (`api/vault_lock.go`'s
  `applyVaultBurn`, `api/vault_fee.go`) — a withdrawal whose fee would
  exceed the requested amount now refuses upfront (no burn happens) at
  both HTTP fast-fail and the authoritative apply-time check, instead of
  burning the user's balance into a $0 release. **Reviewer should
  verify**: the two check sites (`HandleVaultBurn`, `applyVaultBurn`)
  can't diverge (e.g. a race between fee-estimate read and burn).
- **Admin-triggered refund of the one already-affected wallet** (see §2
  above) exists specifically because of the flat-fee bug above having
  already caused one real loss before the fix landed — same underlying
  incident, two remediation angles.

---

## 4. In-progress research/design work — not yet in the codebase

No code changes to review here; listed for context on active direction.

- **Ethereum light-client bridge (Helios)** — proof-of-concept only.
  a16z's Helios (MIT-licensed, audited-in-the-sense-of-widely-used, not
  authored by this project) was manually installed and run in the
  foreground on the mainnet server, pointed at mainnet's real Infura
  endpoint. Confirmed it produces genuinely consensus-verified block
  headers (`receiptsRoot`/`stateRoot` present, checkpoint saved to its
  own DB) and serves working `eth_getLogs` queries — the primitives
  needed to verify a deposit event without trusting a validator's word.
  `eth_getBalance`/`eth_getProof` currently error ("distance to target
  block exceeds maximum proof window"), not yet root-caused, though not
  blocking for the log/receipt-proof path this use case actually needs.
  **Nothing from this is wired into the live backend** — Helios was
  stopped after the test, not persisted as a service.
- **Consensus redesign proposals** (permissionless networking /
  Sybil resistance, and fork-resolution state rollback) — written up as
  design proposals in `audit/consensus_redesign_proposals.md`, not
  implemented. Worth an auditor's read for direction feedback, but
  there's no code behind either yet.

---

## 5. Lower risk — informational only

No change to trust model or fund custody; listed for completeness.

- Dispute fault-choice frontend UI (`website/js/p2p.js`) — pure
  frontend, backend already existed and was covered by the original
  audit's scope indirectly via the marketplace fee work.
- Site-wide clean URLs (no `.html` extension) + a `cmd/staticserve`
  TLS-support fix (the repo's committed source was missing TLS entirely
  despite production actually running HTTPS — a repo/production drift
  bug, now closed).
- Marketplace platform fee redesign (5-way split: burn/validator/
  admin/outernet/moderator) and the associated dispute fault-choice
  pay-or-decline mechanic + biweekly moderator pool sweep — economic
  logic changes, no new custody/auth surface.
- Trading Hub shareable link feature (`website/hub-link.html`,
  `core.Wallet.ConnectedHub`) — see §2 for the one new transaction type
  this introduced; the rest is UI/UX only.
- Postgres dual-write now live on mainnet (`DATABASE_URL` configured,
  `api/db.go`) — durability improvement, `state.json` remains
  authoritative for reads, this is a second write target only.
- Several smaller fixes: moderator stake floor surviving delegation,
  "Trust Score" rename + a Node ID display bug, `/token/mint` refusing
  on mainnet, operational fixes found while re-seeding node1's
  validator.

---

## How to use this document

Section 1 is the item that most warrants urgent independent judgment —
it's real, live, and financially exploitable today, even at today's
small vault balance. Section 2 is where a reviewer should apply the
same scrutiny the original Critical/High findings came from (new
signer code, new admin-gated value movement, new signed transaction
types). Section 3 is fixes to code the last audit already looked at —
worth confirming the fixes are actually correct and didn't introduce a
new subtler bug in the process. Sections 4–5 are lower priority /
informational.
