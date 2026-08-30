# CreditChain Mainnet Audit — Scope Correction and Immediate Risk Addendum

**Date:** 2026-08-30
**Related report:** `creditchain-final-production-security-audit-2026-08-30.md`
**Purpose:** Prevent the supplied archive review from being misrepresented as a complete audit of newly disclosed production assets.

## Material scope change

The subsequently supplied `AUDIT_ENGAGEMENT_BRIEF.md` states that CreditChain has a live, real-value mainnet service at `service.creditchain.site`, and that the Ethereum vault at `0xE3C27466943903B390d06Cddb265263091b2d752` holds real ETH/USDC.

The earlier reports reviewed the supplied archive with SHA-256 `6B14878FC3336C63CC15B1D2558795BD94915138E9BE57CD4491582568CFCA4E` and the publicly reachable **testnet** frontend at `creditchain.site`. They did **not** establish that the archive exactly matches the current mainnet service, its configuration, its database state, its AWS KMS policy, validator identities, bridge evidence, or the deployed Ethereum bytecode.

Therefore, neither earlier report constitutes a production sign-off for the newly disclosed mainnet service or vault.

## Immediate blocker requiring independent verification

The engagement brief states that the disclosed vault has `threshold() == 1` and that the backend bridge-validator threshold is also one. If accurate, one compromised or malicious validator key could authorize release of real locked ETH/USDC. This must be treated as an **unverified Critical production-risk claim** until an auditor independently records:

- Chain ID, exact contract bytecode hash, verified source, compiler settings, constructor arguments, proxy/implementation links, and owner/upgrade roles.
- `threshold()`, validator set, pause status, fee recipient, asset balances, and recent privileged events from an independent RPC/archive node.
- The current backend validator configuration and a commitment proving each running node uses the same configuration.
- The complete release path from external-chain observation through backend authorization, contract call, signer policy, KMS policy, and transaction broadcast.

Until this verification is complete, do not increase deposits, releases, caps, or new user exposure. Consider a temporary pause of new bridge deposits/releases if an authorized, independently reviewed emergency mechanism exists; do not make an emergency contract change without a separately reviewed migration and signer plan.

## What remains true from the archive assessment

The archive-based findings remain unresolved against that exact source baseline unless code-level remediation and a retest prove otherwise. In particular, the generic privileged-report authorization issue, consensus leadership/canonicalization issues, browser key/XSS risk, recovery weakness, and vault validator-set validation must be checked against the actual mainnet commit and deployed bytecode—not assumed fixed because the deployment differs.

The testnet site's "not audited" disclosure should remain in place. The engagement brief correctly states that an independent, named human/firm report is required before any public claim that the production system has been audited.

## Required audit inputs before a genuine mainnet assessment

1. Read access to the private repository at the exact production commit, including submodules and deployment scripts.
2. Signed release manifest mapping source commit, build artifact, Docker image digest, service configuration commitment, and deployed contract bytecode.
3. Contract addresses on every chain, verified source/bytecode, constructor arguments, proxy admin/upgrade roles, current validator set and threshold.
4. Redacted but complete production configuration schema: feature flags, bridge validators, admin quorum, RPC/oracle endpoints, KMS key policies, IAM roles, network segmentation, reverse-proxy/WAF policy, Postgres replication/backups, and monitoring/alerting.
5. Testnet/staging environment with at least three independently configured validator nodes and authority to run adversarial, non-destructive test cases.
6. Explicit rules of engagement: authorized endpoints, allowed hours/rate limits, test accounts, emergency contacts, stop conditions, and incident process.

## Production-gate conclusion

**No production approval is issued.** The mainnet deployment is outside the verified scope of the supplied archive review. A full independent engagement must verify the exact mainnet release and deployed Ethereum state before it can assign closure status to any finding or alter the public "not audited" disclosure.
