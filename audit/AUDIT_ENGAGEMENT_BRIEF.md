# CreditChain — Audit Engagement Brief

*A short, standalone cover document meant to be sent directly to a
prospective independent security firm or auditor — read this first,
then follow its links into the deeper material in `audit/` and
`contracts/AUDIT_PACKAGE.md`. If you only read one page before deciding
whether to scope this engagement, make it this one.*

---

## What CreditChain is

CreditChain is a live blockchain protocol: its own consensus (Proof of
Stake with Reputation), a native token (CCT), a lending/borrowing
market, a P2P exchange, and a lock-and-mint bridge that lets real
Ethereum-side assets (ETH, USDC) move onto CreditChain as 1:1-backed
tokens (`ETH_CCC1`/`USDC_CCC1`) and back.

**Mainnet is already live and holds real value.** This is not a
pre-launch review. A real validator is producing blocks, a real
Ethereum-mainnet contract holds real ETH/USDC, and real users hold
real balances today.

## Current audit status — stated plainly

**No independent, professional third-party security audit has been
performed on this codebase.** The public site says so directly (a
persistent disclosure banner on both network domains).

What *has* been done: several rounds of thorough internal review,
carried out by AI-assisted engineering sessions (Claude Code) working
directly against the live systems and source — not a substitute for
independent human review, and not represented as one anywhere in this
repository. See `outputs/creditchain-security-audit-2026-08-24.md` for
the most recent pass and `audit/HARDENING_HISTORY.md` for the full
history of what those passes have found and fixed. Treat every claim in
this repository's own documentation as a starting point to verify
independently, not as ground truth — that is this project's own stated
convention (see `audit/README.md`'s "Corrections" section for a
concrete example of that convention catching real drift).

**We are seeking a genuine third-party engagement now.** This document
exists to make that engagement fast to scope, not to argue that one is
unnecessary.

## The one finding that can't wait for a full engagement to start

**`CreditChainVault.sol`, deployed on Ethereum mainnet at
`0xE3C27466943903B390d06Cddb265263091b2d752`, currently has an on-chain
`threshold()` of 1.** One compromised or malicious bridge-validator
signature is sufficient to release real locked ETH/USDC from the
vault. The backend's own configured threshold
(`BRIDGE_VALIDATOR_THRESHOLD=1`) matches — both layers agree, this is
not a display bug. Full detail, including the on-chain read used to
confirm it independently across three separate public RPC providers,
is in `audit/KNOWN_GAPS.md` (search for `threshold()`) and
`audit/full-scale-audit-program.md`'s Stage 3.

We are surfacing this ourselves, unprompted, because a real engagement
should start knowing about it on day one rather than spending billable
hours rediscovering it. It does not mean the project considers this
acceptable risk — it means this specific finding is the single highest
priority in any scope you propose.

## Suggested scope

`audit/full-scale-audit-program.md` lays out a full 9-stage program
(Protocol Design → Cryptography → Bridge → Smart Contracts → Economic
Model → Infrastructure → Penetration Testing → Public Testnet →
Independent External Audit / Mainnet Readiness Sign-off) with exact
file/doc pointers for each stage. You do not need to take on all nine
at once — the bridge/smart-contract surface (Stages 2–3,
`contracts/CreditChainVault.sol` plus `contracts/AUDIT_PACKAGE.md`) is
the most urgent standalone scope given the finding above and the real
value already flowing through it.

Live deployments in scope:
- **Mainnet** — `service.creditchain.site` (real value; real AWS
  KMS-backed ETH custodial signing; Postgres dual-write for
  durability).
- **Testnet (node1)** — `creditchain.site` (no real value; safe to
  attack freely, and already public).

## What we can hand over on day one

- `audit/ARCHITECTURE.md` — system layout, transaction-authenticity
  model, consensus/networking, token model, order book.
- `audit/THREAT_MODEL.md` — actors, assets, attack surfaces, current
  mitigations, residual risk per surface.
- `audit/KNOWN_GAPS.md` — every disclosed simplification, limitation,
  and known-but-unfixed issue in one place (large; searchable).
- `audit/HARDENING_HISTORY.md` — chronological record of past
  incidents (chain forks, a supply-drift bug, node instability) and
  exactly what each caused to be fixed.
- `contracts/AUDIT_PACKAGE.md` — the bridge contract's design, trust
  model, and hard preconditions for real-value use, written for a
  smart-contract auditor specifically.
- A working test suite (`go test ./...`, currently several hundred
  tests across `core/` and `api/`) and `go vet ./...`/`go build ./...`
  as a baseline — run these yourself rather than trusting a stale
  count; see `audit/README.md` for how test counts have drifted before.

## Repository access

This is a private GitHub repository. Access will need to be granted
per-auditor/per-firm once an engagement is agreed — raise this when you
respond.

## What "audited" will mean once this is done

The public disclosure will only change once there is a dated, signed
report from a named individual or firm, listing findings by the
severity model in `audit/full-scale-audit-program.md` (Critical / High
/ Medium / Low), with Critical and High findings either resolved or
explicitly accepted as a disclosed, sign-off risk — not silently
dropped. Until then, the site's current "not audited" / "preliminary
internal review only" language stays as-is, and that's intentional: the
banner is a safety claim real users rely on, and it should only ever
move in the direction the evidence actually supports.

## Contact

luciferdell11star@gmail.com
