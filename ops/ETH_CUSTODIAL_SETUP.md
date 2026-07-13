# ETH Custodial Corner setup (Sepolia testnet)

This is the operator-facing setup guide for the ETH custodial DeFi corner
(`core/eth_custodial.go`, `api/eth_signer.go`, `api/eth_rpc.go`,
`api/eth_custodial.go`, `website/defi.html`). Read
`audit/ARCHITECTURE.md` §9 and `audit/KNOWN_GAPS.md`'s custodial-key entry
first - this document is setup steps, not the security rationale.

**Two things must both be true before this feature does anything beyond
returning a clear "not enabled" error: a signer (KMS or local dev
fallback) and a Sepolia RPC connection.** Neither is required to run the
rest of CreditChain - every other feature works identically with this
entirely disabled.

## 1. Signer: AWS KMS (recommended) or local dev fallback

**Local dev fallback (default, no setup)**: if `ETH_CUSTODIAL_KMS_ENABLED`
is unset or not `"true"`, the node generates real secp256k1 keys but holds
them only in its own process memory (`api/eth_signer.go`'s
`localDevEthSigner`). Fine for a developer testing the feature locally.
**Never use this in a real deployment** - keys don't survive a restart,
and there's no HSM/KMS security boundary at all.

**AWS KMS (production)**:

1. In IAM, create a policy scoped to exactly what this feature needs -
   nothing broader:
   ```json
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Action": [
           "kms:CreateKey",
           "kms:GetPublicKey",
           "kms:Sign",
           "kms:TagResource"
         ],
         "Resource": "*"
       }
     ]
   }
   ```
   (KMS doesn't support resource-level scoping on `CreateKey` itself since
   the key doesn't exist yet - `GetPublicKey`/`Sign` could be scoped
   further to specific key ARNs once keys exist, as a tightening pass
   later.)
2. Attach this policy to whichever IAM user/role the node's AWS
   credentials resolve to (see `ops/POSTGRES_SETUP.md` for the same
   credential-handling pattern - never paste an access key directly into
   an SSH command).
3. Set on each node (via the systemd service's environment, same pattern
   as every other env var in this project):
   ```
   Environment=ETH_CUSTODIAL_KMS_ENABLED=true
   Environment=AWS_ACCESS_KEY_ID=...
   Environment=AWS_SECRET_ACCESS_KEY=...
   Environment=AWS_DEFAULT_REGION=eu-west-1
   ```
4. Restart `creditchain.service`. You should see `ETH custodial signer:
   AWS KMS (production-grade key custody)` in the log - if you instead see
   the "LOCAL DEV FALLBACK" line, something in steps 1-3 didn't take;
   check the log line right above it for why (bad AWS config, etc.).

**Cost note**: each custodial account creates one real KMS key
(~$1/month per key at standard AWS pricing, plus per-signature request
cost). For many users this doesn't scale cheaply - see
`audit/ARCHITECTURE.md` §9 for the standard "shared hot wallet + internal
ledger + sweep" architecture real custodians use at scale, which this
first pass deliberately does not implement (see that section for why: one
key per user is far simpler and was the right tradeoff for a first,
KMS-pattern-proving version).

## 2. Sepolia RPC connection

Needs a free API key from a provider - CreditChain doesn't run its own
Ethereum node. Either works:

- **Infura**: https://infura.io - create a project, copy its Sepolia
  endpoint (`https://sepolia.infura.io/v3/YOUR_KEY`).
- **Alchemy**: https://alchemy.com - same idea
  (`https://eth-sepolia.g.alchemy.com/v2/YOUR_KEY`).

Set on each node:
```
Environment=SEPOLIA_RPC_URL=https://sepolia.infura.io/v3/YOUR_KEY
```

Restart `creditchain.service`. You should see `ETH custodial corner
enabled: connected to Sepolia testnet` in the log. If the URL is wrong or
unreachable, or - importantly - if it points at the wrong network (e.g. a
mainnet URL pasted by mistake), the node logs a clear refusal and leaves
the corner disabled rather than silently operating against the wrong
chain; this check exists specifically to prevent an operator mistake from
accidentally pointing this feature at real mainnet ETH.

## 3. Getting test ETH into a custodial account

Sepolia ETH is free from a faucet - CreditChain doesn't mint or provide
it. Once a user has created a custodial account (visible on
`website/defi.html`), send Sepolia ETH to their shown custodial address
from any Sepolia faucet (e.g. https://sepoliafaucet.com) or your own
Sepolia wallet.

## 4. Known limitations of this first pass (see audit/KNOWN_GAPS.md)

- **Node-local, not replicated.** A custodial account only reliably works
  from whichever node it was created on - `core.EthCustodialStore` is
  persisted to that node's own `state.json` but deliberately not gossiped
  to the other node (see `api/eth_custodial.go`'s top-of-file comment for
  why). If your traffic can hit either node interchangeably, a user may
  see "no custodial account" on the node they didn't create it on.
- **One KMS key per user**, not the shared-hot-wallet-plus-ledger
  architecture real custodians use at scale (see §1's cost note).
- **Ethereum mainnet requires a separate, deliberate decision** - do not
  set `SEPOLIA_RPC_URL` to a mainnet endpoint. The chain-ID check in
  `api/eth_rpc.go`'s `InitEthRPC` refuses to enable against anything but
  Sepolia specifically to make this mistake loud rather than silent, but
  it is not a substitute for the licensing/money-transmission review
  flagged in `audit/KNOWN_GAPS.md` before ever pointing this at real
  value.
