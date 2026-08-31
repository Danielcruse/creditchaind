# CreditChain — Known Disclosed Gaps

This is a consolidated collection of every deliberate simplification,
limitation, or unfinished path already documented as an inline comment
throughout the codebase, plus a handful of known bugs/incidents tracked in
project notes but not yet fixed in code. Nothing here is a surprise to the
team — the point of this document is to hand an auditor a single place to
read all of it, instead of finding it scattered as doc comments across ~30
files. Each entry names the file so it can be verified directly against
source.

Entries are grouped by category. Where a source location is given, treat it
as a pointer to re-verify — line numbers drift as the code changes.

## Identity / KYC

- **"Verified" Trading Hub badge is not KYC.** (`core/company.go`,
  `core/transactions.go`, `api/companies.go`) A hub applies with free-text
  self-description (`DocumentNote` — never an uploaded/verified document); a
  registered moderator reads it and grants or denies a badge. The badge
  means "a human read this and vouched for it," nothing more. Must never be
  represented to users or regulators as real KYC/AML/licensing verification.

## Custody / Key Management

- **Server never stores a wallet's real public key beyond the current
  request.** This is a deliberate design choice (self-certifying addresses,
  see `ARCHITECTURE.md` §4), but it has real, disclosed side effects:
  - `api/explorer.go` — an address profile can't show ETHTEST/USDCTEST held
    balances for an arbitrary queried address, because deriving the
    sub-address requires the public key, which isn't on file.
  - `api/apply.go` (dormant-wallet reclaim) — the same reason means a
    dormant-wallet sweep cannot reach ETHTEST/USDCTEST balances at all.
  - `api/recovery.go` / `api/apply.go` (wallet recovery) — ETHTEST/USDCTEST
    migration during recovery only happens if the user supplies and proves
    their *old* public key; if they can't, those balances are permanently
    stranded on the frozen old address. Explicitly called out in code as "a
    real and disclosed limitation."
- **ETH custodial DeFi corner: built, but scoped to Sepolia testnet only,
  never mainnet.** (`core/eth_custodial.go`, `api/eth_signer.go`,
  `api/eth_rpc.go`, `api/eth_custodial.go`, `website/defi.html`) - a real
  AWS KMS-backed signer (secp256k1, private key material never leaves KMS)
  with a clearly-labeled local-dev-only fallback, real Sepolia transaction
  construction/signing/broadcast via go-ethereum, and a chain-ID check that
  refuses to enable at all if the configured RPC URL doesn't resolve to
  Sepolia specifically (guards against an operator mistake pointing this at
  mainnet). See `ops/ETH_CUSTODIAL_SETUP.md` for setup and every disclosed
  limitation of this first pass, most importantly:
  - **Not replicated across nodes.** A custodial account only reliably
    works from whichever node created it - deliberately not gossiped
    through the consensus pipeline, to avoid two nodes racing to broadcast
    the same real (if testnet) withdrawal transaction. See
    `api/eth_custodial.go`'s top-of-file comment.
  - **One KMS key per user**, not the shared-hot-wallet-plus-internal-
    ledger architecture real custodians use at scale - a deliberate
    simplification for a first pass proving the KMS signing pattern, not
    the final production architecture.
  - **Real Ethereum mainnet custody remains explicitly out of scope** and
    would need its own dedicated security review plus actual money-
    transmission licensing review before any code change points this at
    real value - the Sepolia-only chain-ID check exists specifically to
    make that boundary hard to cross by accident.
- **Multi-Chain HD Wallet (Phase A): hand-implemented crypto primitives,
  not a vendored/audited library.** (`website/js/vendor/hd-crypto.js`,
  `website/js/vendor/bip39-wordlist.js`, `website/js/hdwallet.js`) One
  recovery phrase now derives a CreditChain (SLIP-10 Ed25519), Ethereum
  (BIP-32 secp256k1, testnet), and Bitcoin (BIP-32 secp256k1, BIP-84
  testnet bech32) account. Vendoring an audited library (`@scure/bip39`
  + `@scure/bip32` + `@noble/curves`, per the original design) was
  blocked - no download capability, no Node/npm in the build
  environment - so secp256k1 point arithmetic, Keccak-256, RIPEMD-160,
  and Bech32 are hand-implemented instead (CreditChain's own Ed25519
  path only needs HMAC-SHA512 chaining plus native WebCrypto). Every
  primitive was cross-checked against an independent Go reference
  implementation (`cmd/hdoracle`, built only from dependencies already
  shipped in production - `golang.org/x/crypto`, go-ethereum's own
  secp256k1 curve) against known BIP-32/BIP-39 test vectors, including
  a full real Ed25519 signing round-trip. The bundled BIP-39 English
  wordlist was hand-typed (same download restriction) and has since
  been diffed word-for-word against the authoritative copy at
  `github.com/bitcoin/bips/blob/master/bip-0039/english.txt` - zero
  differences across all 2048 entries, matching SHA-256 hashes. Treat
  this exception as scoped narrowly to HD derivation math and address
  encoding; it does not extend to any other crypto in this codebase.
- **Ethereum/Bitcoin accounts in the HD wallet were receive-only in
  Phase A; Multi-Chain HD Wallet Phase B made the self-custodial
  Ethereum account able to really send/receive real (testnet) Sepolia
  ETH — Bitcoin remains receive-only, unchanged.** (`website/js/hdwallet.js`,
  `website/asset.html`, `website/send-eth.html`) Bitcoin still has no
  balance lookup, transaction construction, or broadcast - no RPC
  wiring exists for it yet. This is architecturally separate from the
  existing KMS-custodial Sepolia DeFi corner above (different trust
  model: user-held keys, not server-held) and is not related to it
  beyond both touching Ethereum.
- **Multi-Chain HD Wallet Phase B: hand-implemented RFC 6979
  deterministic ECDSA signing + RLP encoding, not a vendored/audited
  library — same disclosed exception class as Phase A's derivation
  math, now extended to transaction signing.** (`website/js/vendor/eth-tx.js`)
  Gives the self-custodial Ethereum account (private key held only in
  the browser, never sent to the server) the ability to sign and
  broadcast a real legacy Sepolia transaction: RFC 6979 deterministic
  nonce generation (no randomness, so a weak/predictable/reused
  per-signature `k` — the exact vulnerability class that leaked Sony's
  PS3 signing key — can't occur from a bad RNG), r/s ECDSA math done
  mod the curve order (`SECP256K1_N`, never the field prime used for
  point coordinates), EIP-2 low-S normalization, and a hand-rolled RLP
  encoder. Verified via the same oracle-cross-check methodology as
  Phase A's derivation math: a throwaway Go reference tool
  (`cmd/ethsignoracle`, built only from go-ethereum functions this
  project's own KMS-custodial signer already trusts in production -
  `crypto.Sign`, `types.NewTx`, `types.LatestSignerForChainID`,
  `tx.WithSignature`) produced 4 fixed test vectors (including a
  zero-value transfer and a `gasPrice=128` value sitting exactly on
  the RLP single-byte/length-prefixed encoding boundary), each
  independently re-verified via `crypto.Ecrecover` to confirm the
  signature genuinely recovers to the known sender - not just "the
  numbers matched." The JS implementation's output was byte-for-byte
  identical to the oracle's raw signed transaction hex on all 4
  vectors. `cmd/ethsignoracle` is deleted once no longer needed for
  further spot-checks, same lifecycle as the deleted `cmd/hdoracle`.
  **`/eth-selfcustody/broadcast` deliberately has no CreditChain
  request-signature auth** (`requireSignedRequest` verifies a
  CreditChain Ed25519 envelope tied to a CreditChain address, which
  has no relationship to the Ethereum address that actually signed
  the transaction being relayed) - the real authorization boundary is
  Ethereum's own signature verification, independently re-checked by
  every Sepolia node the transaction reaches; an attacker without the
  private key can only submit garbage that fails RLP-decode or the
  network's own check. Residual spam/abuse risk is covered by the
  existing global per-IP rate limiter, same as every other route.
  **Read path (balance/nonce/gas-price) has since been verified
  against real live Sepolia infrastructure** - `SEPOLIA_RPC_URL` wired
  to a public keyless Sepolia RPC endpoint, chain-ID-verified by
  `InitEthRPC` same as production, `/eth-selfcustody/balance` returns
  genuine on-chain data (confirmed nonce/balance/live gas price for
  the real derived test address). **The write path (an actual signed
  broadcast reaching real Sepolia) remains unverified against a live
  network** - the user explicitly declined to fund a throwaway test
  account via a faucet, so this specific step is accepted as an
  intentional, disclosed risk rather than a completed check: the
  broadcast code has only ever been proven correct via the oracle
  byte-for-byte match (§ above) and a from-the-real-page signing dry
  run, never a live send. First real signed Sepolia transaction from
  this code, whenever it happens, is the actual first live-fire test.
- **The spec's "USDT (CCC1)" has no backend equivalent.** `USDCTEST`
  (`core/address.go`'s `USDCTestAddressTag`) is CreditChain's existing
  imported-test-USDC token, not USDT - Phase A does not rename or
  repurpose it as a USDT stand-in. No USDT row/asset exists anywhere
  in the app.
- **Legacy-wallet migration is user-driven, not automatic.** (`api/apply.go`'s
  `applyHDWalletMigrate`, `website/js/hd_migrate.js`) A self-custodial
  wallet can't be silently upgraded server-side (the server never has
  a private key to derive a new mnemonic from) - every pre-Phase-A
  wallet is prompted on login and must explicitly walk through
  migration, signed by its own still-controlled old key. Until a user
  does this, their wallet keeps working exactly as before, just
  without ETH/BTC accounts.
- **Auto-bridge feature: deposit detection inherits the ETH custodial
  corner's non-gossip limitation.** (`api/eth_bridge.go`'s
  `startEthBridgeDepositLoop`/`pollAndReportBridgeDeposit`) Deposit
  polling only runs against `ethCustodialStore.Snapshot()` - the same
  node-local, not-replicated store documented above under "Not
  replicated across nodes." A custodial account's real Sepolia balance
  is therefore only ever polled and reported by the one node that
  created that account; other nodes never independently notice a
  deposit. Once a report *is* submitted, though, it's an ordinary
  signed `TxBridgeDeposit` that gossips and confirms like any other
  transaction (reusing the exact `TxSlashReport`
  single-node-attestation pattern - see `applyBridgeDeposit`'s doc
  comment), so every node converges on the same ETHB credit once
  confirmed. The gap is detection-side only, not settlement-side.
- **Auto-bridge feature: ERC-network withdrawal's ETHB debit and its
  real Sepolia broadcast are two separate, non-atomic steps.**
  (`api/eth_bridge_withdraw.go`'s `HandleBridgedEthWithdraw`) Same
  disclosed risk class as the ETH custodial corner's own withdrawal
  path above, now extended to the bridge: the handler records the
  ETHB debit through the normal signed-tx/block-confirmation pipeline,
  then - deliberately not waiting for that confirmation - broadcasts
  the real Sepolia transaction synchronously on the same node. A crash
  between those two steps leaves a debited-but-not-broadcast state
  (recoverable - nothing was actually sent); a crash or non-confirmation
  of the debit after a successful broadcast leaves a
  broadcast-but-not-debited state (not automatically recoverable). A
  node-local, best-effort reservation guard (`bridgeWithdrawReserved`)
  closes the more urgent double-broadcast race (two rapid requests
  passing the same stale balance check), but this single-request
  crash-timing edge case is accepted as a disclosed, testnet-scoped
  simplification, not solved - matching this project's own precedent
  at `eth_custodial.go` for why a two-phase-commit/outbox pattern is
  judged disproportionate here.
- **ETHB fee model overhaul: every ETH-denominated flat fee
  (`ETHBCCTNetworkFeeWei`, `UsdcTestTransferFeeWei`,
  `ethbWithdrawBridgeFeeBps`) is an illustrative testnet value, a
  judgment call, not derived from a specified model.** (`api/apply.go`,
  `api/eth_bridge_withdraw.go`) Deposits stay free; a CCT-network ETHB
  send now pays a flat ETH fee (no longer CCT); an ERC-network
  withdrawal pays live-tracked gas price (`ethNonceAndGasPrice`) plus a
  locked 0.05% bridge fee; USDCTEST transfers now also pay a flat ETH
  fee from the sender's ETHB balance. None of these percentages/flat
  amounts were specified by the user beyond "0.05% for the ERC-network
  bridge fee" (implemented exactly) - the rest follow this codebase's
  existing precedent of small, clearly-labeled illustrative constants.
- **ETHB address unification migration note.** (`api/subaddress.go`'s
  `resolveBalanceAddress`) The ETHB ledger key changed from an always-
  derived hash to "the real custodial Sepolia address once one exists,
  falling back to the derived hash otherwise" - this makes the deposit
  address, internal ledger key, and send-from address the same string,
  closing a real gap between "same address in both networks" and what
  the code actually did. Any ETHB balance that was already sitting at
  the *old* derived-hash address before this change does not
  automatically move to the new key - accepted as a testnet-scoped
  limitation (same "no zero-downtime migration needed at this stage"
  precedent as every other non-backward-compatible change this
  session), not a data-loss risk in practice since this is pre-launch
  testnet balance only.
- **USDCTEST has no real bridge or second-network concept.** Its new
  ETH-denominated transfer fee (see above) applies to the one send path
  that exists today - there is no "ERC network" equivalent for
  USDCTEST to extend a live-tracked fee to yet.
- **BTC bridge (BTCB): self-custodial by design, deposit detection is
  node-local/non-gossiped, same shape as the ETH bridge's own disclosed
  gap above.** (`api/btc_bridge.go`'s `startBtcBridgeDepositLoop`/
  `pollAndReportBtcDeposit`, `core/btc_address.go`) Unlike ETHB,
  CreditChain never holds a BTC private key at all - `BtcWatchedAddress`
  only tracks which address (the user's own self-custodial, BIP-84-
  derived testnet address) to poll for a deposit. That polling only
  ever runs on the node the address was registered through
  (`POST /btc-bridge/register-address`), so a deposit is only detected
  and reported by that one node - once a report *is* submitted, though,
  it's an ordinary signed `TxBridgeBtcDeposit` (same self-signed-
  report/gossip/dedup pattern as `TxSlashReport`/`TxBridgeDeposit`) that
  every node applies identically once confirmed. Detection-side only,
  not settlement-side - matches the ETH bridge's own disclosed
  limitation exactly.
- **BTC-network send only supports native segwit (bech32 `tb1...`)
  recipient addresses.** (`website/js/vendor/btc-tx.js`'s
  `signAndBuildTx`) A legacy base58 (`1.../3.../m.../n...`) recipient
  address throws a clear "not supported right now" error rather than
  constructing a legacy P2PKH scriptSig - a deliberate, disclosed scope
  narrowing, not an oversight. `send-bridged-btc.js`'s address
  validation only recognizes `tb1...` as a valid BTC-network address for
  the same reason.
- **BTC-network send uses naive greedy coin selection and an
  approximated fee estimate, neither optimized for privacy or dust
  avoidance.** (`website/js/vendor/btc-tx.js`'s `signAndBuildTx`,
  `send-bridged-btc.js`'s `estimateBtcNetworkFee`) Coin selection
  accumulates confirmed UTXOs largest-first until the amount+fee target
  is covered - not private, not dust-minimizing, acceptable for a
  testnet demo send flow. The fee shown before signing is a two-pass
  estimate (live `satsPerVByte` from Blockstream's fee-estimates
  endpoint x an assumed 2-output P2WPKH transaction's vsize for the
  input count the coin-selection pass would use) - close enough for a
  demo, not a precision fee engine; the transaction actually broadcasts
  successfully or fails cleanly on `HandleBtcBroadcast`'s relay either
  way, so an imprecise estimate only ever costs a slightly wrong-sized
  fee, never a stuck or double-spent transaction.
- **BTC bridge's signing math is hand-implemented (BIP143 segwit,
  RFC6979 deterministic ECDSA, DER encoding), same disclosed exception
  as the ETH bridge's `eth-tx.js`.** (`website/js/vendor/btc-tx.js`)
  Verified byte-for-byte against BIP143's own officially-published
  P2WPKH test vector (sighash preimage, sighash, DER-encoded signature,
  and final signed-tx serialization all matched exactly) before any
  real-network code path was written - the same blocking-gate discipline
  `eth-tx.js` was held to against its own Go oracle. Reuses `eth-tx.js`'s
  already-verified secp256k1/RFC6979 math directly (same curve, same k-
  generation), differing only in hash algorithm (double-SHA256, not
  Keccak256), no recovery-id (Bitcoin's witness carries the pubkey
  directly), and DER instead of r/s/v signature encoding.
- **BTCB fee/threshold constants (`BTCBCCTNetworkFeeSats`) are
  illustrative testnet values, a judgment call, not derived from a
  specified model** - same framing as ETHB's own fee constants above.
- **Vault gas price oracle key now has a real AWS KMS option (2026-08-16),
  the relayer and bridge-validator keys still don't.** Three keys beyond
  the ETH custodial corner's own signer (which already had KMS support -
  see this section's earlier entries) held real signing authority as
  plain hex environment variables with no HSM/KMS boundary: the vault
  gas price oracle key (`VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX`), the
  release relayer key (`RELAYER_PRIVATE_KEY_HEX`), and the bridge
  validator key (`BRIDGE_VALIDATOR_PRIVATE_KEY_HEX`). All three are
  already bounded-blast-radius by design even in the leaked-key case -
  see `api/vault_gas_price_oracle.go`'s step/rate limits,
  `api/vault_release_relay.go`'s doc comment (gas-only, no fund
  authority, permissionless to relay anyway), and the bridge validator
  system's own M-of-N threshold requirement (one compromised validator
  key alone can never mint or release anything) - so this was always a
  defense-in-depth gap, not an unbounded one.
  - **Fixed for the gas price oracle key**: `api/vault_gas_price_oracle_signer.go`
    adds an AWS KMS-backed `vaultOracleSigner` (Ed25519 via KMS's
    `ECC_NIST_EDWARDS25519` key spec, raw-message signing since Ed25519
    has no digest-signing mode), mirroring `api/eth_signer.go`'s exact
    "prefer KMS, degrade gracefully to the original env var" pattern.
    Opt-in via `VAULT_GAS_PRICE_ORACLE_KMS_ENABLED`/
    `VAULT_GAS_PRICE_ORACLE_KMS_KEY_ID` - see
    `ops/VAULT_GAS_PRICE_ORACLE_KMS_SETUP.md`. Chosen first because it's
    the smallest, cleanest surface: one fixed key, exactly 2 call sites,
    fully operator-held (unlike the bridge validator key below).
  - **Fixed for the relayer key too, as of 2026-08-17**:
    `api/vault_relayer_signer.go` adds a `relayerSigner` interface (KMS
    or raw-hex, same shape as the oracle signer) that reuses
    `api/eth_signer.go`'s existing secp256k1 KMS machinery directly -
    `kmsSecp256k1KeyAddress` was factored out of `kmsEthSigner.NewKey`
    so a caller with an already-existing, operator-provisioned key ID
    (not "create a new one") can resolve its address without going
    through key creation. All 6 former `relayerPrivateKey()` call sites
    across 4 files (`vault_release_relay.go` x3,
    `vault_set_token_allowed_relay.go` x2, `vault_set_validators_relay.go`,
    `vault_admin_sweep.go`) now resolve one `relayerSigner` via
    `resolveRelayerSigner()` and route through the new
    `buildAndBroadcastContractCallWithRelayerSigner` (which supersedes
    and replaces `buildAndBroadcastContractCallWithPrivateKey` - deleted
    outright rather than kept alongside, since nothing needed the raw-
    private-key-only path anymore once every caller went through the
    interface). Opt-in via `RELAYER_KMS_ENABLED`/`RELAYER_KMS_KEY_ID` -
    see `ops/RELAYER_KMS_SETUP.md`. New tests:
    `api/vault_release_relay_test.go` - malformed/unset hex rejected,
    KMS-enabled-without-key-ID falls back to hex (same misconfiguration
    coverage as the oracle signer), and a signature produced through the
    interface is confirmed to cryptographically recover to the signer's
    own reported address.
  - **Deliberately not attempted for the bridge validator key**: this is
    each bridge validator's own EXTERNAL signing infrastructure by
    design (Phase A), not something this server should custody on
    validators' behalf - centralizing it into one operator's KMS would
    undermine the entire point of an M-of-N validator set being
    independent of any single custody failure.
  - **Admin/multisig keys were re-verified during this pass and are
    NOT a gap**: `ADMIN_WALLET_ADDRESS`/`ADMIN_MULTISIG_ADDRESSES`
    (`api/admin.go`) are public addresses only - the server never holds
    an admin private key at all. An admin signs from their own wallet
    software, fully self-custodial already, the same as any ordinary
    wallet-originated transaction.

## Consensus / Validator Security

- **No state rollback on reorg.** `core/replace_chain.go` /
  `api/peer.go`'s `syncChainFromPeer` refuse outright if a candidate chain
  diverges from an already-applied block — there is no mechanism to roll
  back locally-applied state and adopt a competing fork past that point.
  Documented in code as "not fully BFT-robust." This is a real, live-tested
  gap: two genuine node1/node2 forks occurred during this project's testnet
  phase and both required manual operator intervention to resolve (see
  `HARDENING_HISTORY.md`). **Phase 1 stopgap added (2026-07-20):** a refused
  reorg is no longer only a `fmt.Println` — `recordForkEvent`
  (`api/fork_monitor.go`) is now the single chokepoint both
  `syncChainFromPeer`'s refusal path and the periodic 60s fork monitor call
  through, so a fork found mid-sync immediately flips `forkStatus` (visible
  on `GET /health/sync`), appends a durable timestamped line to
  `fork_events.log` in `DATA_DIR`, and optionally emails `FORK_ALERT_EMAIL`
  (see `ops/NODE_OPERATOR.md`) — instead of waiting up to 60s for the next
  monitor tick to notice, or relying on someone reading server stdout.
  This does **not** attempt automated recovery or rollback — it only makes
  an already-existing refusal visible/alertable faster, closing the "found
  by accident during a deploy" failure mode the two prior incidents both
  hit. A real fix (checkpoint state periodically, replay `ApplyBlock` from
  the nearest checkpoint into an isolated copy of every store, atomically
  swap in only after a full successful replay) was designed and explicitly
  **deferred as Phase 2** — not attempted in this pass — because roughly 50
  `apply*` functions in `api/apply.go` mutate package-level singleton
  stores in-place with no delta/undo capability, and this codebase's own
  `resetApplyTestState()` test helper (the only existing "reset everything"
  routine) is itself first-party evidence that enumerating every store
  correctly is already error-prone here. Phase 2 needs its own dedicated
  review pass, not a rushed addition alongside this alerting stopgap.
- **Recovery/fallback block production is a deadlock-breaker, not proper
  leader election.** `api/consensus_loop.go`'s fallback path (triggered
  after `missedRoundsBeforeFallback` rounds with no block) exists because an
  earlier version of this logic caused "an unrecoverable fork" in production
  when it fired at the wrong time; the current mitigation (sync-before-
  fallback, raised threshold) is a best-effort fix for a documented past
  incident, not a formally-proven-correct recovery protocol.
- **RESOLVED (2026-07-20): Missed-block slashing now requires M-of-N
  corroboration, not a single node's word.** Double-sign slash reports are
  independently re-verified from embedded cryptographic evidence before
  being applied (see `ARCHITECTURE.md` §5). Missed-block reports have no
  equivalent proof — absence can't be cryptographically demonstrated — so
  previously, once *any* node reported a miss and it was confirmed in a
  block, the penalty applied network-wide on that one node's word alone.
  `api/missed_block_confirmation.go` closes this: every node that
  independently detects the same missed round submits its own report (as
  before), but `applySlashReport` (`api/apply.go`) now only applies the
  actual penalty (reputation, slash count, stake seizure) once a *strict
  majority* of currently known, independently-operated nodes have
  corroborated the *same* event (`requiredMissedBlockConfirmations`,
  `M = N/2 + 1`). `N` is deterministically derived from already-applied,
  consensus-agreed state (the count of distinct `AuthorizedNodeID` values
  among currently-active validators — every node computes the identical
  value from the same transaction history, which matters since the
  threshold check itself must never depend on any one node's own local,
  non-replicated view). Floored at 1 so a genuinely single-node network
  (nobody else exists to corroborate with) can still slash an unresponsive
  validator rather than deadlocking entirely — a deliberate, disclosed
  degradation for small/bootstrap networks, not a bypass.
  **Disclosed limitation, not solved by this fix**: `N`'s derivation
  (distinct `AuthorizedNodeID` among active validators) undercounts the
  true node count — a node with no validator currently delegated to it is
  invisible to this count even though it independently runs the consensus
  loop and would also report a miss. This is the safer direction for a
  threshold (harder to reach, not easier to game) but a real node registry
  independent of validator delegation would be a more accurate fix, and is
  named as future work, not attempted here.
- **`/peer/*` gossip endpoints are gated by one static shared secret**
  (`NETWORK_SECRET`), not per-peer identity or authorization. If unset, the
  network fails closed (unreachable) rather than falling back to any
  weaker-but-functional state — a deliberate fail-safe choice, but it means
  there is currently no path to a self-serve/permissionless join flow (see
  `ops/NODE_OPERATOR.md`).
  **RESOLVED, in part (2026-08-10): the peer-identity-spoofing escalation
  into an actual block-forgery vulnerability is closed.** Previously,
  `verifyBlockProducer` (`api/peer.go`) verified a gossiped block's
  signature against whatever `PublicKey` `peerStore` had on file for the
  claimed `ProducerNodeID` — and `peerStore.Add` (via `HandlePeerHandshake`)
  recorded any `NodeID`/`PublicKey` a `NETWORK_SECRET` holder claimed with
  zero proof of possession. An attacker who merely knew a real validator's
  public `AuthorizedNodeID` (visible via `GET /validators`) could
  self-sign a forged block, claim that same `NodeID` under their own
  `PublicKey` at handshake, and have the forged block wrongly verify.
  Fixed two ways: (1) `verifyBlockProducer` now checks
  `ValidatorStore`'s own `AuthorizedNodePublicKey` — set only via a
  cryptographically-verified on-chain `TxValidatorRegister` — and never
  falls back to `peerStore` for signature verification at all; (2)
  `HandlePeerHandshake`/`handshakeWith` now require a proof-of-possession
  signature over the claimed `NodeID`/`PublicKey`/`Address`/`Timestamp`
  (±60s freshness window, mirroring `core.RequestTimestampWindow`), so a
  `NETWORK_SECRET` holder can no longer claim an identity it doesn't
  actually control. Covered by `api/peer_security_test.go`, including a
  regression test that poisons `peerStore` with an attacker-controlled
  entry for a real validator's `NodeID` and confirms the forged block is
  still rejected.
  **Disclosed limitation, NOT solved by this fix**: this closes identity
  *spoofing* (claiming to be someone you're not), not Sybil resistance
  itself. `NETWORK_SECRET` is still the only admission cost to becoming a
  gossip peer at all — there is no economic/reputation/registration cost
  to holding it, no cap on how many peer-store entries one secret-holder
  can create, and no per-peer rate limiting on `/peer/tx`/`/peer/block`
  (a well-formed-but-rejected flood still costs the receiving node a full
  JSON decode + Ed25519 verification each time, with no reactive
  suspension for that class of behavior — only for unreachable/invalid-
  chain failures). A genuine self-serve/permissionless join flow with
  real Sybil-resistance economics is still future work.
- **RESOLVED (2026-07-20): `TxTokenMint` and every other admin-only
  transaction type now carry a real, independently-reverifiable
  signature, not just a self-attesting node identity.** `api/admin.go`'s
  `requireAdminAuthed`/`verifyAdminTxAuth` add a network-wide-configured
  admin wallet (`ADMIN_WALLET_ADDRESS`, an ordinary CreditChain wallet
  address, not a special key type) — once set, every node applying an
  admin transaction independently checks its signature against that
  exact address (`core.VerifyTransactionAuth` + an address-pinning
  check), closing the single-attestor gap this entry used to describe.
  `ADMIN_TOKEN` (the old shared-bearer-secret model) is kept only as a
  fallback for networks that haven't set `ADMIN_WALLET_ADDRESS` yet —
  once one is configured, the token path stops working entirely (by
  design, not an oversight — see `requireAdminAuthed`'s doc comment),
  so there's no live parallel-bypass. Verified end-to-end in this
  session: a real signed admin action (posting an announcement) landed
  on-chain and was independently re-verifiable via the public API. See
  `ops/NODE_OPERATOR.md`'s env-var table for setup. The remaining gap
  is key custody itself (how the admin wallet's own private key is
  stored/managed) — real hardware/multisig/KMS-backed custody for that
  key is still the user's/operator's responsibility, not something this
  codebase can enforce.
- **RESOLVED (2026-07-20): the "key custody" gap immediately above is
  now closed by an on-chain M-of-N multisig, replacing the single admin
  wallet key for all 9 admin actions (mint, treasury controls, genesis
  pool allocation/withdraw, admin transfer, announcements) — no single
  stolen/compromised key can act alone anymore.** `api/admin.go`/
  `api/admin_multisig.go`: set `ADMIN_MULTISIG_ADDRESSES` (comma-
  separated signer addresses) and `ADMIN_MULTISIG_THRESHOLD` (M) to
  enable — once configured (and only if `threshold <= len(signers)`,
  otherwise multisig refuses to enable rather than silently making every
  admin action permanently unexecutable), it supersedes
  `ADMIN_WALLET_ADDRESS` entirely, the same "coexistence window, not a
  permanent second door" pattern the original admin-wallet work
  established — `verifyAdminTxAuth` unconditionally rejects a direct,
  unwrapped admin transaction once multisig is enabled, closing the
  parallel-bypass class of bug this project has fixed before.
  **Client-transparent design**: the HTTP client for all 9 endpoints is
  unchanged — it still signs the same plain payload shape it always did
  (e.g. `{address, amount, symbol}` for `/token/mint`); `recordAdminTx`
  is the one server-side chokepoint that decides, per the matched auth
  tier, whether to wrap the action into a `TxAdminMultisigPropose`
  transaction instead of applying it directly. The first signer's
  action counts as approval #1 (a `TxAdminMultisigPropose`); each
  further signer submits an independent `TxAdminMultisigApprove`
  (`POST /admin/multisig/approve`); the underlying action executes
  exactly once, only when approvals reach the configured threshold, via
  a single chokepoint (`executeAdminMultisigAction`) guarded by a
  `Status != Pending` check. Execution synthesizes an unsigned
  `core.Transaction` and calls the target `apply*` function directly,
  bypassing the normal signature re-check — deliberately, since
  authorization was already independently established through the real,
  cryptographically-verified propose/approve signature chain; this was
  only safe to do after confirming none of the 9 target `apply*`
  functions depend on `tx.AuthAddress`/`tx.From`/`tx.To`/`tx.Amount`,
  only `tx.Data`/`tx.ID`/`tx.Timestamp`.
  **Bug found and fixed during this work, not merely inherited**: the
  pre-existing `adminWalletAddress()` unconditionally stripped a leading
  "cc" from any configured address (meant only to undo the website's
  cosmetic 42-char display-prefix, task #267) — this silently corrupted
  any *raw* 40-char address whose own genuine first byte happened to be
  0xcc (~1-in-256 probability, confirmed to actually occur with a live
  randomly-generated test address during this session's verification).
  Fixed via a shared `normalizeAdminAddress` helper that only strips
  "cc" when the input is exactly 42 characters; applied consistently to
  `adminWalletAddress()`, `adminMultisigSigners()`, and
  `isAdminMultisigSigner()`. Verified end-to-end this session via a
  throwaway HTTP smoke-test client (3 independent signers, real signed
  propose/approve requests against a live server): a 2-of-3 proposal
  correctly stays unexecuted at 1 approval, executes exactly once at 2
  approvals, a late 3rd approval is a harmless no-op (rejected, already-
  resolved), and a direct legacy `ADMIN_TOKEN` submission is rejected
  outright once multisig is configured.
  **Disclosed limitations, not solved by this change**: (1)
  `ADMIN_MULTISIG_ADDRESSES`/`ADMIN_MULTISIG_THRESHOLD` must be
  configured identically on every node, the same operational requirement
  `NETWORK_SECRET`/`ADMIN_WALLET_ADDRESS` already carry — a node with a
  different configuration would disagree with the rest of the network
  about which admin transactions are valid; (2) key custody for each
  individual signer's own private key is still that signer's own
  responsibility — multisig removes the single-point-of-failure risk of
  one compromised key acting alone, it does not make any individual
  signer's key itself unstealable; (3) there is no mechanism in this
  pass for rotating signers or the threshold without redeploying every
  node's environment configuration — a live, on-chain-governed
  signer-set-change is a natural follow-up, not built here. See
  `ops/NODE_OPERATOR.md`'s env-var table for setup.
- **RESOLVED: validator min-stake mismatch.** `HandleRegisterValidator`
  (`api/validators.go`) now rejects registration below `core.MinValidatorStake`
  (1,000) with a clear error, matching the actual leader-selection threshold
  (`core.isEligible`) exactly — a validator can no longer register
  successfully and show `"active": true` while never actually being
  eligible to produce. Re-verified directly against source this session
  (this entry previously said "not yet fixed" — that was stale).
- **RESOLVED: `GET /peers` always returned `[]`.** `api/peers.go` now
  encodes `peerStore.List()`, the real live peer registry (`api/
  node_identity.go`), not the old dead placeholder variable. Re-verified
  directly against source this session (this entry previously said "not yet
  fixed" — that was stale).

## Proof of Stake with Reputation (PoSR)

Formalizes CreditChain's consensus model per the approved spec (2026-07):
validation rights require Economic Stake (CCT) + Protocol Trust (Trust
Score), not wealth alone. Most of the selection/reputation machinery
already existed from earlier work (`core/consensus.go`'s weighted
stake/reputation/uptime leader selection, continuous Reputation Credit
mining — see the Reputation/Mining section above); this pass closed the
remaining gaps: a real correctness bug in validator eligibility gating,
literal stake slashing, a permanent-ban mechanism, a decaying block
reward, and a genesis token allocation.

- **RESOLVED, deeper than the earlier "min-stake mismatch" entry above:
  validator eligibility was gated on `Wallet.StakedETH` (ETHTEST), not
  CCT.** Confirmed via `core/staking_sync.go`'s own doc comment
  ("predates multi-token staking"). `api/apply.go`'s
  `applyValidatorRegister` — the actual consensus-applied check, not
  just the HTTP pre-check — now requires `wallet.StakedCCT >=
  core.MinValidatorStake`, matching PoSR's literal "Stake the minimum
  required amount of CCT." `api/consensus_loop.go`'s `hydrateValidator`
  (the function that actually feeds `core.Validator.Stake` into
  selection weight) was updated the same way, so registration
  eligibility and selection weight are now consistent with each other
  and with the spec. ETHTEST staking's own yield/unbonding mechanics are
  completely unaffected — only the validator-eligibility *read* moved.
- **Stake-slash and permanent-ban magnitudes are judgment calls, not
  specified numbers.** `core/slashing.go`'s `MissedBlockStakeSlashBps`
  (0.50%) and `DoubleSignStakeSlashBps` (10.00%) are sized proportional
  to the existing, already-locked reputation penalties (2 vs. 50 points)
  but weren't independently derived from an economic model.
  `PermanentBanSlashCountThreshold` (6, reusing the existing strike
  weights — roughly 2 double-signs, or 1 double-sign + 3 missed blocks)
  is the same kind of judgment call. Slashed stake is burned (removed
  from `TokenStore.TotalSupply` via `stakeEscrowAddress`), never
  redistributed to the reporting node — a deliberate choice to avoid
  incentivizing false reports for profit.
- **SUPERSEDED (2026-08-25): the block reward is no longer a flat or
  decaying schedule at all — it's now tied directly to real fee-burn
  activity.** The paragraph below (the relative-decay curve applied to
  a 10 CCT Year-1 baseline) described the mechanism that shipped
  before this date and has been fully removed from the code
  (`blockRewardSchedule`/`blockRewardEra`/`blockRewardForTimestamp`/
  `ProduceBlockReward` no longer exist). Per explicit operator
  instruction ("the reward should be 90% of burned revenue from
  fees"), every block's reward is now `core.Treasury.CCTBurnedPendingReward
  * 90 / 100` (`api/apply.go`'s `pendingBurnBasedBlockReward`) — 90% of
  however much CCT was burned from marketplace platform fees
  (`splitPlatformFee`'s existing 50%-of-fee burn share) since the last
  block, reset to 0 every time it's paid out. A block produced with no
  fee-burn activity since the last one now correctly pays a validator
  zero reward, unlike the old schedule, which always paid something
  regardless of real economic activity. Net effect per fee-burn cycle:
  −10% of the burned amount to total CCT supply, +90% to the
  validator (split with delegators pro-rata exactly as before). Left
  the original paragraph below struck through rather than deleted, so
  the *reasoning* for the old design (fractional-CCT avoidance, whole-
  unit `uint64` amounts) stays visible for history.
- ~~**The controlled-inflation block reward schedule applies a
  *relative* decay curve to the existing real reward, not the spec's
  literal decimal numbers.** The spec's example (1.00/0.80/0.60/0.45/
  0.30 CCT) uses fractional CCT, which this ledger's whole-unit
  `uint64` amounts don't support anywhere (adding fractional-CCT
  support would be a much larger, separate migration touching every
  balance/fee in the system). `api/tokenomics.go`'s
  `blockRewardSchedule` instead applies the same
  1.00x/0.80x/0.60x/0.45x/0.30x curve to the already-live
  `ProduceBlockReward` (10 CCT) as the Year-1 baseline: 10 → 8 → 6 → 5
  → 3 CCT, held flat at the Year-5 floor thereafter (the spec only
  defines 5 years). Anchored to the chain's real genesis block
  timestamp (`blockchain.Blocks[0].Timestamp`), never wall-clock.~~
- **Genesis pool allocation is a one-time, explicitly-triggered admin
  action, not a true genesis-block mechanism.** This chain has been
  running for a long time already (148,000+ blocks at the time this was
  built), so there's no true "genesis" moment to hook into
  programmatically. `POST /admin/genesis-pools/allocate`
  (`api/tokenomics.go`) mints 1,000,000,000 CCT once, split 40% Validator
  Reward Pool / 25% Foundation Treasury / 15% Ecosystem Development / 10%
  Governance Treasury / 10% Community Incentives (locked amounts, per
  explicit user approval 2026-07) — a slice of the new
  `MaxTotalCCTSupply` cap (5,000,000,000), not the whole cap, leaving
  ~4B headroom for the burn-based block reward above to mint into over
  subsequent years. Guarded idempotent via
  `core.Treasury.GenesisPoolsAllocated` so it can only ever fire once.
- **PARTIALLY RESOLVED (2026-07-20): the 4 genesis pools with no
  controlling keypair of their own (Validator Reward Pool, Ecosystem
  Development, Governance Treasury, Community Incentives — synthetic
  `"POOL:*"` addresses, same convention as `STAKE:*`/`FEE:*` escrow
  addresses) now have a real, admin-wallet-signature-gated spend path:
  `POST /admin/genesis-pools/withdraw` (`api/genesis_pool_withdraw.go`,
  `core.TxGenesisPoolWithdraw`) moves funds from one of these 4 pools
  to a real destination, verified the same way as every other admin
  action (see the `TxTokenMint` entry above). `isKeylessGenesisPool`
  explicitly whitelists only these 4 — Foundation Treasury is
  deliberately excluded from this path (it already has its own real
  address and its own admin controls) and covered by a dedicated test
  (`TestApplyGenesisPoolWithdraw_RejectsFoundationTreasuryAndUnknownPools`).
  This is still single-admin-key-gated, not governance-vote-gated — a
  real governance-controlled disbursement mechanism (e.g. requiring a
  passed proposal before a withdrawal takes effect) remains a natural,
  larger follow-up, not attempted here.
- **`TxAdminTransfer` (`api/admin_transfer.go`) is a deliberate, narrow
  emergency-recovery capability, not a general admin override.** Added
  specifically for this deploy: two production validators (node-001,
  node-002) were seeded by an early bootstrap process with no
  recoverable private key, so once the CCT-stake eligibility fix above
  went live, neither could sign a normal `/stake` transaction to meet
  the new requirement. `TxAdminTransfer` lets the operator move CCT an
  address *already holds* to a new, key-controlled address - it can
  never create new supply (unlike `TxTokenMint`, which already could),
  only relocate existing balance, and only when triggered by the same
  `ADMIN_TOKEN` already gating the mint faucet. Intended purely as a
  one-time migration tool for this specific situation, not a
  general-purpose fund-movement endpoint - each use is visible on-chain
  as an ordinary transaction with `tx.From` empty (node-authored, per
  `verifyTxAuth`'s `TxAdminTransfer` case).

## Governance

Makes votes actually worth something (weighted by stake + reputation,
not a flat 1-per-wallet count), gives real incentives for proposals
that reach quorum and pass, and requires a real passed vote before a
`PARAM_CHANGE`/`VERSION_ACTIVATION` proposal takes effect. See
`api/governance_weight.go`, `api/apply.go`'s
`processProposalFinalization`, and `core/governance_params.go`.

- **`DefaultProposalQuorum` (500), `ProposalVotingWindowSeconds` (7
  days), and `votingWeight`'s 60%-reputation/40%-stake split are all
  judgment calls, not derived from a specified model.** Calibrated for
  this network's current small scale and for philosophical consistency
  with PoSR's own reputation-dominant weighting
  (`core/consensus.go`'s 55% reputation), not independently validated.
  A future tuning pass may want to revisit all three once real
  participation data exists.
- **The governance-params registry (`core/governance_params.go`)
  deliberately starts with only two low-blast-radius fee constants**
  (`ValidatorRegisterFeeCCT`, `CCTTransferFee`) — migrating PoSR's own
  freshly-hardened consensus/slashing constants into this same registry
  the same night they were stabilized would repeat the exact scope-creep
  pattern that caused a real production incident earlier in this
  session. That migration is named as deliberate future work, not
  attempted here.
- **`VERSION_ACTIVATION` proposals cannot prevent an operator from
  deploying unapproved code — this is architecturally impossible for
  an off-chain binary swap, and the mechanism doesn't pretend
  otherwise.** `core.ActivatedProtocolVersion` is a feature-flag a
  future consensus-critical change can check before turning itself on
  (`if core.ActivatedProtocolVersion >= "vX" { ... }`) — it gates
  *behavior a future deploy chooses to gate*, not the deploy itself.
  Nothing currently shipped is retroactively gated by it.
- **Retrofitting admin actions (token mint, genesis pool allocation/
  withdrawal, `TxAdminTransfer`) to require a passed governance vote
  first was explicitly left out of scope** — a much bigger change to
  operational/incident-response tooling that deserves its own dedicated
  conversation with the user, not a unilateral bundling decision made
  while building the voting mechanism itself. This is now a
  single-admin-key gate (see the `TxTokenMint` entry above), not a
  shared-secret one, but it's still a single key, not a quorum.

## Economic / Game-Theoretic

- **Several demo-scale timing constants stand in for real values.**
  `UnbondingPeriodSeconds` (60s, vs. a real deployment's likely days),
  `DormancyPeriodSeconds` (300s, vs. an intended ~50-year real dormancy
  period), and `MaxTestStakePerAccount` (explicitly "a shared-pilot fairness
  cap, not an economic security mechanism") all need real-world values set
  before mainnet — they exist at demo scale purely so their lifecycles could
  be exercised quickly during development.
- **RESOLVED: default reputation penalty is shortfall-proportional, and
  late-but-repaid is now wired up too.** `core/credit_engine.go`'s
  `shortfallReputationDelta` + `DefaultLoan(loan, shortfall, owed)`, driven by
  `api/apply.go`'s `processLoanDefaults` (due-date sweep) and
  `processCollateralLiquidations` (proactive price-crash sweep, OETH
  collateral only) — a real default now scales the borrower's penalty (and
  the lender's mirror-image gain) to how much of the debt liquidation
  actually failed to recover, floored/capped between 1 and the historical
  flat `-30`, with zero penalty if collateral fully covered the shortfall.
  The separate `+3` (repaid-but-late, distinct from default) path now has
  lateness detection wired up too — `CreditEngine.RepayLoan` takes the
  repayment's own timestamp and compares it against `loan.DueAt` via the new
  `loanRepaymentDelta` helper: on time (or no due date set) pays the full
  `+10`, anything repaid after `DueAt` but before the loan actually defaults
  (i.e. during Warning/Margin Call/Grace Period - `loan.Status` stays
  `LoanFunded` through all three, see `loan_risk.go`) pays the smaller `+3`
  instead. `api/apply.go`'s `applyRepay` now logs the correct delta/reason to
  `reputationEventStore` for either case. Confirmed against current
  `/reputation-math` API output: both default and late-but-paid are now
  `Wired: true`.
- **Reputation-delta magnitudes are judgment calls, not a locked spec.**
  E.g. `ReputationDeltaP2PTrade = 14` is documented in code as "40% above
  another reward, arbitrarily chosen," not derived from any modeled
  incentive analysis. `ReputationDeltaLoanFunded`'s continued existence is
  flagged in its own doc comment as an open question ("flag if this should
  be removed or changed").
- **`TPS` and `GasPrice` shown to users are non-functional stub values.**
  `api/network.go` hardcodes both with literal `// TODO` comments — no real
  throughput or dynamic gas calculation exists yet. Cosmetic/informational
  only; nothing depends on these values being real.
- **No atomic multi-store transaction primitive.** Every two-leg trade
  (e.g. `settleHubTradeOnChain`) is settled by explicitly reversing the
  first leg if the second fails — there is no database-style all-or-nothing
  transaction across the separate in-memory token stores. Disclosed and
  consistent everywhere it's used, but worth an auditor's attention as a
  systemic pattern rather than a one-off.
- **CCT's price against ETHTEST/USDCTEST now comes from a real order book
  (`core/order_book.go`), not an AMM.** Deliberately scoped narrowly for
  this pass — see `THREAT_MODEL.md` §3.12 for the full attack-surface
  writeup. Summarized here: no trading fee, no order expiry (orders rest
  indefinitely until filled/cancelled), no protection against wash trading
  across two wallets controlled by the same actor (only literal
  same-address self-trading is blocked), and the AMM pools for these two
  symbols are hidden from new swaps/liquidity but not deleted — they stay
  live internally for unstake-redemption crediting. An auditor should
  confirm nothing else in the codebase still treats the AMM's reserve
  ratio as authoritative pricing for these two symbols now that the order
  book is the intended source of truth.
- **The 1 CCT flat transfer fee is a disclosed rounding substitution, not
  the originally-requested amount.** `core/token_transactions.go`'s
  balance model is exclusively `uint64` with no sub-unit precision
  (confirmed via exhaustive search — no `float`/`Decimal`/`big.Float`/
  `big.Rat` type touches any token amount anywhere in the codebase), so a
  0.5 CCT fee is architecturally unrepresentable as specified. Rounded up
  to 1 CCT flat instead, applied only to CCT-denominated `TxExchange`
  transfers, swept into `cctTransferFeePoolAddress()` during transaction
  processing and paid to the block-producing validator during
  `applyBlockReward` (same block, deterministic across nodes since both
  steps happen inside `ApplyBlock`). A full system-wide precision rescale
  (e.g. moving to a smaller base unit, "milli-CCT") would allow exact
  fractional fees but wasn't undertaken as part of this change; worth
  considering before mainnet if precise fee amounts matter economically.
- **Lending marketplace redesign: Borrowing Power Score weights and the
  client-side Risk Rating are judgment calls, not a locked economic
  model.** (`api/borrowing_power.go`, `website/js/lending-shared.js`)
  The composite score (reputation 40% / repayment history 25% / staked
  CCT 15% / collateral 10% / governance participation 10%) and its
  normalization caps (`stakedCCTNormalizationCap=10000`,
  `governanceParticipationCap=5`) are a reasonable starting formula
  documented in code as exactly that — not derived from a specified
  economic model, and worth revisiting with real usage data before
  the weights are treated as authoritative. The sixth named factor,
  validator endorsements, has no real mechanism anywhere in this
  codebase (`core.Validator.CommunityTrust` is scaffolded, always 0,
  never written) and is reported at an explicit weight of 0 with a
  "not yet active" status — never a fabricated number. The per-loan
  Risk Rating (Very Low–Very High) shown on the marketplace pages is
  a pure frontend display convenience computed from already-fetched
  real data (collateral ratio, reputation, loan size, duration) — it
  is not a new backend source of truth and is not used anywhere in
  the actual lending/liquidation logic.
- **`GET /lender/stats` (`core.LenderStats`) is a known-orphaned
  endpoint from a superseded funding path — do not build new features
  on it.** Confirmed by reading both funding paths in full: the only
  code that ever writes to `lenderStats` is the legacy direct
  `/loans/fund` → `applyFundLoan` path (`api/apply.go`), which no
  current frontend calls. The actual funding path every real lender
  uses today — `applyLoanOfferDraw` (negotiation-confirm) and the new
  `applyLoanRequestFulfill` (see below) — both inline their own
  reputation bookkeeping and never touch `lenderStats` at all, so this
  endpoint returns all-zero/stale data for every real lender. The new
  Lender Dashboard (`website/lend.html`) is deliberately built on a
  new `GET /lender/portfolio` endpoint instead, which computes every
  figure live from `loanStore.Loans` on each read rather than relying
  on this stale running counter. `core.LenderStats.InterestEarned`
  specifically was also found to be mislabeled even on the path that
  does write it — it sums the loan's rate *percentage* at fund time,
  not a currency amount, and fires regardless of whether the loan is
  ever actually repaid; `/lender/portfolio`'s `interestEarned` field
  computes the real collected-currency figure instead
  (`repayAmountFor(loan) - loan.Amount`, summed only for loans that
  reached `REPAID`). Not fixed in place — `lenderStats`/
  `applyFundLoan` are left exactly as they were, matching this
  project's "don't delete, disclose" convention for superseded code.
- **Borrower-initiated loan requests (`core.LoanRequest`) allow real
  over-collateralization; the older self-funded/offer-draw borrow
  paths still don't.** A deliberate, disclosed enhancement, not an
  inconsistency to be "fixed" — `applyBorrow`/`applyLoanOfferDraw`
  both still reserve collateral exactly equal to the loan amount
  (1:1, no choice), while `applyLoanRequestCreate` lets the borrower
  choose any `CollateralAmount >= Amount` in their chosen symbol.
  Verified end-to-end in a live local browser session (two real
  registered wallets, real signed transactions, real fund movement):
  a request for 20 YEN-e against 30 CCT collateral was created,
  discovered by a second wallet, negotiated at an 8% rate (within the
  request's 10% max), confirmed, and settled — the borrower received
  exactly 20 YEN-e, the lender's balance dropped by exactly 20, the
  request flipped to `FULFILLED`, and the resulting `Loan` carried the
  correct negotiated rate and the borrower's original collateral.

## Reputation / Mining

- **RESOLVED (2026-07-20): Continuous Reputation Credit (REPT) mining no
  longer does all wallets in a single block.** `processReputationMining`
  (`api/reputation_mining.go`) still runs on the same coarse
  `miningEpochBlocks` cadence (10,800 blocks, ~1 day at the existing ~8s
  consensus tick) and the sweep is still O(all wallets) in *aggregate*
  cost per epoch — that part is unchanged and not the problem being
  solved. What changed: the first block of a new epoch used to mint to
  *every* eligible wallet synchronously, in that one block — an unbounded
  per-block cost that would only grow as the wallet count grows over the
  network's lifetime. Now the epoch's eligible-wallet list is snapshotted
  once (sorted, for the same cross-node determinism every other sweep in
  this codebase already relies on) and drained in bounded batches
  (`miningBatchSize=250`, a judgment call) across as many subsequent
  blocks as it takes — one block's added cost is now capped regardless of
  total wallet count. The in-progress batch cursor is persisted
  (`reputationMiningSweepSnapshot` in `api/persistence.go`) — this is
  required for correctness, not just durability: without it, a restart
  mid-epoch would re-snapshot the full wallet list and double-mint
  whichever wallets were already processed earlier that epoch. New tests
  in `api/reputation_mining_batching_test.go` cover: a wallet set larger
  than one batch spreads correctly across multiple blocks with no
  double-mint once drained; a new epoch boundary correctly starts a fresh
  sweep; and a simulated restart mid-epoch (snapshot → reset → restore)
  resumes the cursor instead of re-processing the first batch. All 4
  pre-existing tests in `api/reputation_mining_test.go` still pass
  unchanged, since at today's wallet count the whole epoch still finishes
  within the very first post-boundary block, same as before this fix.
  `baseEpochMiningEmission=20` (REPT/epoch at MiningPower==100) remains a
  judgment call sized only to make continuous mining meaningfully
  outpace the reduced one-time tier rewards (see below), not derived
  from any specified economic model — unaffected by this fix.
- **Mining Power's factor weights and two normalization caps are
  judgment calls, not a locked economic model.** `api/mining_power.go`'s
  `miningPowerScore` (Trust Score 30% / Repayment History 20% /
  Staking 15% / Governance 10% / Validator Participation 10% /
  Successful Lending 10% / Account Age 5%) reuses three factors
  verbatim from `api/borrowing_power.go`'s existing Borrowing Power
  Score (via shared helpers) but is otherwise a new, independently
  weighted formula — worth revisiting with real usage data. The eighth
  named factor from the Reputation Credit design, Network
  Contributions, has no real backing data anywhere in this codebase and
  is reported at an explicit weight of 0 with a "not yet active"
  status — never a fabricated number, same convention Borrowing Power
  already uses for Validator Endorsements.
- **Warning/Margin Call dwell times in the Default Protection sequence
  are judgment calls; only Grace Period and Final Redemption are
  spec-locked.** `core/loan_risk.go`'s `LoanRiskWarningDwellSeconds`
  (24h) and `LoanRiskMarginCallDwellSeconds` (48h) were not specified
  by the product design and may need tuning; `LoanRiskGracePeriodSeconds`
  (72h) and `LoanRiskFinalRedemptionWindowSeconds` (24h) are explicit,
  locked values from that design and should not be casually changed.
- **The one-time REPT tier-crossing reward was reduced ~10x
  (`core/reputation_reward.go`: Silver 50→5, Gold 200→20, Diamond
  750→75, Genesis 500→50), a judgment call on the exact scale-down
  factor**, made because continuous mining (above) is now intended as
  the dominant, ongoing REPT source — the tier reward is a secondary
  bonus, not the primary earning path, as it was before this change.
  Relative ordering (including the pre-existing, intentional quirk that
  Genesis pays less than Diamond) is preserved unchanged.
- **New notification types for the Default Protection sequence
  (`LOAN_RISK_WARNING`, `LOAN_MARGIN_CALL`, `LOAN_GRACE_PERIOD_STARTED`,
  `LOAN_LIQUIDATED`, `LOAN_FINAL_REDEMPTION_NOTICE`, `LOAN_REDEEMED`)
  are in-app only.** No email/SMS/push delivery infrastructure exists
  anywhere in this codebase beyond the one already-disclosed email
  placeholder (`api/recovery.go`, entry above) — consistent with that
  existing gap, not a new one, and the product spec itself named SMS as
  explicitly optional.

## Data Integrity / Replication

- **Real incident, since fixed: faucet-caused CCT supply drift. Decision
  made 2026-08-16: resolve via node2's restoration, not a standalone
  reconciliation.** An earlier version of the faucet minted tokens
  instantly and node-locally with no signature at all, which caused the
  two production nodes' CCT total supply to genuinely diverge (observed
  drift: 13,740 vs. 13,240 at one point). The underlying cause (bypassing
  the gossip/`ApplyBlock` pipeline) was fixed — faucet claims are now an
  ordinary signed, gossiped, block-confirmed transaction like any other.
  **Investigated 2026-08-16**: node1's *current* live CCT supply is only
  500 CCT smallest-unit-equivalent whole tokens - nowhere near the
  13,240/13,740 range from the original measurement, and node2 (the
  other side of that original comparison) is currently out of the
  network (see the node2 capacity entry above), so a live two-node diff
  isn't possible right now. The gap between "500 today" and "~13,000+
  back then" is too large to confidently attribute to organic burns
  (transfer-fee burns, platform-fee burns, slashing) versus a state
  reset at some point since, without a full historical ledger replay
  (every mint/burn transaction type, from genesis) - judged not worth
  doing standalone for a testnet-value discrepancy this small, especially
  since it's likely moot once node2 is actually dealt with. **Decision**:
  fold this into node2's restoration - when node2 comes back, wipe its
  old (drifted) local state and do a fresh sync from node1 rather than
  resuming node2's own history, which resolves the two-node divergence
  as a side effect rather than needing a separate reconciliation
  transaction. Revisit only if node1's own supply ever looks internally
  inconsistent against its own blockchain history independent of node2
  entirely.
- **Same root cause, same fix, different feature:** `TxTokenMint`
  (admin/operator minting) had the identical "mints only on the node that
  received the HTTP call" bug, which would silently diverge derived state
  (e.g. AMM pool reserves) across nodes; fixed the same way (routed through
  the gossip/`ApplyBlock` pipeline).
- **Real incident, since fixed: two live unauthenticated-mutation
  endpoints, found via a Phase 5 systematic route audit, not a live
  exploit report.** `POST /loans/create` (`HandleCreateLoan`) took a raw,
  unsigned JSON body and wrote a `core.Loan` directly into the same
  `loanStore` the real, authenticated borrow/lend system reads from and
  pays out against — since `HandleFundLoan` verifies the *lender's*
  signature but trusts whatever `Borrower` address is already attached
  to the loan record it looks up, this was a genuine fund-misdirection
  vector (create a fake loan naming an attacker's address as borrower,
  wait for a real lender to fund it) in addition to the usual
  bypasses-gossip/causes-cross-node-divergence pattern already documented
  above. `POST /transactions` (`HandleTransaction`) built a raw
  `core.Transaction` and injected it straight into the mempool with none
  of the `Auth*` fields the signed-envelope model requires — confirmed
  this specific one could not actually mutate consensus state
  (`verifyTxAuth`'s `default` case rejects anything it doesn't recognize,
  and every case requires fields this path never sets), so its real
  impact was unauthenticated mempool/gossip spam capacity, not fund
  theft. Both were pre-authentication-era handlers (predating the signed-
  envelope model entirely) left in place after the real, authenticated
  systems (self-funded `applyBorrow`/`LoanOffer` marketplace; `recordTx`
  + `ApplyTransaction` generally) superseded them — confirmed zero
  legitimate callers (no website JS, no ops tooling) before removing both
  routes and their handler functions outright. See
  `HARDENING_HISTORY.md` §7 for the full writeup, including everything
  else the same audit pass checked and confirmed was *not* a gap.
- **Swap-based reward crediting falls back to a guaranteed 1:1 rate only
  when the pool can't be quoted.** The normal path returns real pool pricing
  (including fees/slippage) — this is a disclosed pricing-risk simplification
  for the fallback case, not a guaranteed-return promise in general.

## Recovery / Account Security

- **Wallet recovery is a testnet-only convenience, explicitly not real
  account-recovery infrastructure.** (`core/recovery.go`,
  `api/recovery.go`) It does not and cannot recover a lost private key — it
  migrates balances to a *new* wallet, gated only by a low-entropy "wallet
  creation year" security question plus a verified email address. The code's
  own doc comment states this security model is "deliberately weaker than a
  signature" and must be treated as a testnet convenience only.
- **Real SMTP is now configured and live on node1 (as of 2026-07-20).**
  `api/email.go` implements a real SMTP sender (STARTTLS + AUTH PLAIN,
  stdlib only), and both `api/recovery.go` (recovery-code emails) and
  `api/hub_rate.go` (hub-attention notifications) call it when
  `smtpConfigured()` is true. Node1 has `SMTP_HOST`/`SMTP_USERNAME`/
  `SMTP_PASSWORD`/`SMTP_FROM` set via a systemd drop-in
  (`/etc/systemd/system/creditchain.service.d/smtp.conf`, `mail.privateemail.com`) —
  confirmed live: recovery-code requests now genuinely email the code and the
  API response omits it entirely (`HandleRequestEmailCode`/
  `HandleRequestRecoveryCode` in `api/recovery.go` only return the code in the
  disclosed testnet-fallback branch, never once SMTP is configured). Node2 has
  no drop-in and is currently out of service/excluded from the network — this
  remains a node2-only gap, not a code gap, once/if node2 is restored. See
  `ops/EMAIL_SETUP.md` for the setup steps.
- **The recovery verification code itself is not part of the consensus/
  security boundary** — it's described in code as playing the same role as
  a CAPTCHA (bot-deterrence), not as cryptographic authentication.

## Moderation / Trust Model

- **Moderator attestation is human trust, not cryptographic verification.**
  (`core/moderator.go`, `core/p2p_offer.go`, `core/negotiation.go`,
  `api/p2p_external.go`, `api/moderators.go`) — consistently and repeatedly
  disclosed in code: a moderator confirming a trade or resolving a dispute
  is a documented judgment call about whether an off-app (real-world)
  payment happened, never a cryptographic guarantee. Moderator oversight
  never gates or reverses the underlying on-chain fund movement — it's an
  attestation layer sitting alongside the economic settlement, not an
  escrow-release mechanism in the traditional sense.
- **A trade can go permanently unassigned if zero moderators are
  registered.** `core.ModeratorStore.AssignNext` returns `""` in that case —
  disclosed as a real gap on a brand-new/small network, not an edge case
  that's been engineered around.

## Networking / P2P Permissioning

- **Permissioned network, no Sybil resistance, no self-serve join.** Already
  covered above under Consensus (including the 2026-08-10 fix that closed
  the identity-spoofing-into-block-forgery escalation, and what's still
  disclosed as open) — listed again here because it's as much a
  networking-model decision as a consensus one. See `ops/NODE_OPERATOR.md`
  for the current manual-handout join process.
- **Platform fee mechanism fails open (no fee), not closed (error), if
  misconfigured.** If `PLATFORM_FEE_ADDRESS` is unset, `applyPlatformFee`/
  `skimPlatformFee` silently skip taking a fee rather than erroring — a
  deliberate choice to never block a legitimate trade over an ops
  misconfiguration, but worth an auditor confirming that's the intended
  failure mode for a revenue-bearing mechanism.
- **Admin endpoints are fully disabled by default, not gated-but-open.** If
  `ADMIN_TOKEN` is unset, admin routes fail closed entirely — meaning
  there's no admin auth configured unless an operator explicitly opts in by
  setting the token. Confirm this is set (and rotated) appropriately in the
  real production environment.

## Other

- **Hub storefronts render genuinely untrusted third-party HTML/CSS/JS.**
  (`core/hub_storefront.go`) The entire safety of this feature rests on the
  sandboxed-iframe isolation (`sandbox="allow-scripts"` only, deliberately
  *without* `allow-same-origin`) implemented in
  `website/js/p2p.js`'s `renderStorefrontFrame` — this must never be
  rendered any other way. Worth an auditor's explicit sign-off given it's
  the one place in the app that intentionally executes arbitrary
  user-supplied code.
- **QR code encoder had 3 real placement bugs, now fixed, plus a new
  hand-rolled decoder for the in-app scanner.** The above entry
  originally flagged that the encoder was checked only structurally,
  never against a real scan - that risk materialized: the QR scanner
  ("does not yet work") turned out to have two independent problems.
  (1) `website/js/send.js`/`send-eth.js` gated all scanning on the
  browser-native `BarcodeDetector` API, which only ships on Android
  Chrome/ChromeOS - absent on desktop Chrome/Edge/Firefox/Safari, so
  the scanner silently failed for nearly everyone regardless of the
  encoder. (2) `website/js/qrcode.js` itself had 3 genuine bugs in its
  zigzag data-placement logic, meaning the QR codes it rendered were
  actually corrupted and would fail on *any* correct scanner, not just
  this app's own: (a) format-info bits were written into the matrix
  *after* the data-placement loop instead of before, so those cells
  looked "available" during placement and got clobbered; (b) it drew
  alignment patterns at all 4 combinations of the version-5 coordinate
  list `[6, 30]` instead of the single spec-correct position `(30,
  30)`, so 2 of the 3 "alignment patterns" silently overwrote parts of
  the top-right/bottom-left finder patterns; (c) the column-pair walk
  substituted column 5 for the reserved timing column (6) on only the
  one iteration where it hit exactly 6, without shifting subsequent
  iterations, causing column 4 to be visited (and overwritten) twice
  while column 0 was never visited at all. All 3 are fixed in
  `qrcode.js` directly (see its inline comments for the mechanism).
  Fix for (1): a new hand-rolled `website/js/qr-decode.js` reads QR
  codes without needing `BarcodeDetector` - deliberately scoped to
  exactly what `qrcode.js` produces (fixed Version 5 / EC level L /
  mask 0 / byte mode), not a general-purpose QR decoder. It has no
  Reed-Solomon error correction; instead a scanned code's data is
  verified by locally recomputing its expected EC codewords (via the
  same `QR.computeECCodewords` the encoder itself uses) and requiring
  an exact match - a clean capture decodes, a bad/blurry one is
  rejected with "try again" rather than silently returning corrupted
  data. Verified via round-trip tests (encode then decode, byte-for-
  byte match) across multiple addresses plus synthetic rotation/blur/
  downscale variants, and via a real end-to-end browser test driving
  the actual file-upload scan path. Known residual limitation, worth
  flagging to an auditor: no live-camera-in-the-loop test was possible
  in this environment (no physical camera) - only the file-upload
  fallback path was exercised against a real `<input type="file">`
  element; the live-video path shares the same `decodeImageSource`
  function so this is low risk, but hasn't been confirmed against an
  actual phone camera.

## Infrastructure / Operational (from this session's work, not yet in code
comments)

- **RESOLVED (2026-08-16): the backup script's "unexpected identity" is
  AWS's own standard Lightsail instance role, confirmed via node1's
  instance metadata service — not an unauthorized second identity.**
  Originally flagged during Phase 0 backup verification: debug output
  showed the `aws` CLI authenticating with a temporary `ASIA...`-prefixed
  session credential rather than the long-lived `AKIA...` backup-only IAM
  user in `/etc/creditchain-backup.env`, with the real credential boundary
  unconfirmed (hypothesized as "most likely an IAM role attached directly
  to the Lightsail instance"). Confirmed via `curl` against
  `http://169.254.169.254/latest/meta-data/iam/security-credentials/`
  (IMDSv2) directly on node1 - no AWS console access needed, despite that
  being the account currently MFA-locked: the attached role is named
  `AmazonLightsailInstanceRole`, `sts get-caller-identity` returns
  `arn:aws:sts::923988302379:assumed-role/AmazonLightsailInstanceRole/i-0890f78f43dcc5b4c`,
  and the role is confirmed minimally-scoped - it can't even call
  `iam:GetRole`/`iam:ListAttachedRolePolicies` on itself (`AccessDenied`
  on both). This is AWS's own well-documented default: every Lightsail
  instance gets this role auto-attached, unrelated to and separate from
  the backup script's own `AKIA...` user, which is (and always was) the
  actual credential authorizing the S3 uploads. No action needed.
- **Single-JSON-file persistence, no real database — Postgres migration
  still not wired up, but the capacity blocker that ruled it out is now
  resolved.** `state.json` / `blockchain.json` are still rewritten
  wholesale on every save and remain the sole *authoritative* source a
  node reads from at startup — no transactional guarantees, unbounded
  growth, single-write-can-corrupt-everything risk on that primary copy is
  still real. **Dual-write code exists and is ready** (`api/db.go`,
  `ops/POSTGRES_SETUP.md`) — every save additionally writes the same
  snapshot into Postgres if `DATABASE_URL` is configured — this was
  deliberately not wired up when node1 was on the smallest Lightsail RAM
  tier (see the resolved memory-pressure entry below), but node1 has since
  been resized to 4GB RAM with real headroom, so that specific blocker no
  longer applies. Wiring up an actual RDS instance (or confirming
  self-hosting Postgres on the now-larger box is safe) remains a real
  to-do, just no longer blocked on capacity. Decomposing individual stores
  into a real relational schema (the deeper part of this gap) remains
  unstarted regardless.
- **RESOLVED this session: node1's memory pressure.** Node1 was found
  running on the smallest Lightsail tier (911MB total, 47Mi free, 489Mi in
  swap) — the same condition that caused node2's earlier OOM/fork cycle.
  Fixed via a proper resize: snapshot → new 4GB/2vCPU/80GB instance →
  careful stop-old/resync-final-state/start-new sequence (mirroring the
  same non-destructive procedure used for node2's fork recovery) → static
  IP moved to the new instance, confirmed zero data loss (matching block
  height/hash throughout) and zero DNS change needed. Verified post-move:
  `/proc/meminfo` shows 3.9GB total, ~2.3GB available. One real hazard hit
  and worth flagging for any future repeat of this procedure: the
  snapshot's Plesk-blueprint origin means each new instance created from
  it ships with a host-level iptables firewall (`plesk-firewall.service`,
  default-DROP policy) that blocks the app's port 8080 even after
  Lightsail's own cloud firewall allows it — this needs a manual
  `iptables -I INPUT 1 -p tcp --dport 8080 -j ACCEPT` (and the matching
  Lightsail cloud-firewall rule) on every new instance created this way,
  not just the first time. The old smaller instance (still Plesk-blueprint
  origin, previously the live one) was left stopped/unattached rather than
  deleted, at the user's choice, as a spare.
- **Monitoring/alerting: health-check deployed and running on both
  production nodes** (`ops/healthcheck.sh` + systemd timer, every 2
  minutes) — checks process-alive, disk usage, block-height stall, and
  cross-node hash divergence (the exact failure mode behind both real
  production forks — see `HARDENING_HISTORY.md`). Verified working both
  locally (simulated stall, matching-peer no-false-positive, unreachable-
  peer cases) and on both live production nodes (first run on each reported
  clean, with real block height/hash) — confirmed still active on node1
  this session (`creditchain-healthcheck.timer`/`creditchain-backup.timer`
  both `active`). **External heartbeat monitor (healthchecks.io /
  UptimeRobot) and CDN/WAF (Cloudflare) were explicitly decided against
  for this round**, not merely deferred by oversight — both require
  creating a third-party account, which is outside what this assistant
  will do on a user's behalf regardless of how low-friction the signup is;
  the user was given exact setup steps and declined to act on them this
  session. Until either is set up, a failure is only visible via
  `journalctl` — nothing pages a human automatically, and there's no
  CDN/WAF layer in front of the public domain.
- **Node2 has a real history of instability and is
  currently unreachable/out of scope again as of this session.** Prior
  incidents on this same box, in order: (1) a total blackout (SSH/HTTPS/
  ping all timing out) traced to the instance itself hanging, resolved by
  a console restart; (2) after that restart, a genuine chain fork at block
  index 46856 (same "reorg-with-rollback not supported" failure class as
  two earlier production forks); (3) the root economic cause of the
  hang/fork cycle: both production nodes run on the smallest Lightsail RAM
  tier (911MB total), and node2's in-app P2P sync path was observed being
  repeatedly OOM-killed mid-sync while trying to catch up to node1's
  full chain over HTTP — `syncChainFromPeer`'s `SaveState()` only fires
  once, after its entire per-block apply loop completes, so every OOM
  crash mid-sync wasted the whole attempt and restarted from zero,
  meaning the built-in sync could plausibly never complete on its own at
  this chain size on this RAM tier. That specific incident was resolved by
  directly staging node1's `state.json`/`blockchain.json` onto node2
  (non-destructively, old files renamed not deleted) rather than relying
  on the OOM-prone sync path, and node2 caught up and ran normally
  afterward. **Node2 has since gone unreachable again** and, per the
  user's explicit instruction, is being left alone rather than
  investigated/recovered as part of ordinary session work — meaning the
  order-book deploy in this update went to node1 only, and node2's chain
  state is now stale relative to node1's by however long it's been down.
  Before any future two-node work, this needs (a) confirming node2's
  actual state when it's manually brought back, since a large amount of
  new consensus-relevant code (the order book, CCT fees) shipped to node1
  alone in the meantime, and (b) revisiting the underlying RAM-tier
  capacity issue rather than treating each recurrence as a one-off, since
  the same OOM/fork failure mode has now recurred multiple times on the
  same undersized instance.

## Price tracking / token standards / YEN-e claim / device lock (2026-07-25)

- **Multi-currency display price relies on a free, keyless, unauthenticated
  external API (CoinGecko) with no fallback provider.** (`api/price_oracle.go`)
  If CoinGecko is unreachable or rate-limits this node, the cache simply
  stops updating — callers always get the last-known-good rates (with a
  stale `updatedAt`) rather than an error, so a transient outage never
  breaks the UI, but a prolonged one means users see increasingly stale
  fiat conversions with no visible warning beyond the timestamp itself.
  Never used for anything that moves funds — purely a display convenience,
  same framing as the existing ETH/BTC/USDC oracle work it extends.
- **CCT's ETH-equivalent price (`api/cct_price.go`'s `cctPriceInETH`) falls
  back to a hardcoded illustrative constant (`DefaultCCTPerETH = 0.000975`)
  until at least one real CCT/ETHTEST trade has been recorded on the order
  book.** This is a judgment call carried over from earlier hub-rate
  documentation, not a live market rate — every fiat price shown for CCT
  on a fresh/thin-liquidity network is this illustrative number, not
  something derived from real trading activity, until real trades exist.
- **Token standards (`core/token_standard.go`) are backend-defined, not
  live-governance-created.** Only three standards exist (`C101` native/
  reserved, `C117S` stable, `C1` independent) and adding a new one means a
  code change and redeploy, not an on-chain action — matching the user's
  own "later on" framing for future growth, not yet built as a live
  mechanism. `C117S`'s `PegReference` is purely descriptive metadata,
  never mechanically enforced (no oracle or mint-controller exists to
  actually back a peg for a user-created token).
- **YEN-e time-cooldown claim (`api/yene_claim.go`) is an explicit testnet
  stand-in for a real Ethereum-side YEN-e bridge that does not exist yet.**
  The claimed amount (1-100 YEN-e, deterministically derived from the
  transaction's own ID so every node computes the identical value) and the
  5-day cooldown are both the user's own explicit numbers, not derived
  from any real bridge economics — there is nothing on the Ethereum side
  to actually redeem this against.
- **USDC/ETH address unification (`api/subaddress.go`) only takes effect
  once a wallet already has a KMS/dev-signer-held custodial Sepolia
  account.** A wallet that has never touched the ETH custodial corner
  still gets USDCTEST's old CreditChain-only derived sub-address as a
  fallback — same "can't happen in practice once ETHB exists, kept as a
  defensive default" reasoning as the original ETHB address-unification
  work this mirrors.
- **The device app-lock password (`website/js/device-lock.js`) is a local
  UI gate, not encryption, and this is by explicit design, not an
  oversight.** The wallet's private key already sits unencrypted in this
  browser's `localStorage` (this project's existing, disclosed custody
  model) — the device password never touches it, is never used as a key
  or passphrase for anything, and is stored (as a salted SHA-256 hash,
  client-side only, never sent to any server) purely to gate the UI. It
  stops someone who picks up an already-unlocked device from casually
  opening the app; it does nothing against anyone who already has the raw
  `localStorage` contents or the wallet's recovery phrase. The
  "forgot password" recovery path is a user-chosen security-question
  hint (also salted-hashed client-side) with no cryptographic tie to
  wallet ownership — correctly answering the hint resets the device lock
  but was never intended as, and must never be treated as, proof of
  wallet ownership. Unlock state lives in `sessionStorage` (cleared when
  the tab/browser closes, per the user's explicit "close and reopen
  should re-prompt" requirement) — a page reload within the same session
  does not re-prompt.

## Staking reward removal (2026-08-01)

- **Staking CCT/ETHB no longer mints any reward or oETH receipt** —
  `core.RewardForStake`'s call site and the ETHB-specific
  `Wallet.OETHBalance` mint, both formerly in `api/apply.go`'s
  `applyStake`, were removed. Staking is now a pure locking mechanism:
  the flat 10% CCT reward and the 1:1 oETH collateral receipt that used
  to mint on every stake action are both gone. This is a deliberate
  economic change, not a bug — mining/block production (PoSR) is the
  intended way to earn CCT going forward, not staking.
- **This narrows new oETH supply for the collateral-backed borrow
  feature.** oETH is used as loan collateral (`api/lock_collateral.go`,
  `api/borrow.go`) — with ETHB staking no longer minting it, the only
  ways to get new oETH going forward are: legacy ETHTEST-stake
  redemption still in flight (`api/legacy_stake_migration.go`) and
  collateral being credited back on loan resolution
  (`creditCollateralTo`). **Existing oETH balances and any already-open
  oETH-collateralized loan are completely unaffected** — nothing was
  touched or reduced, only future minting from new stake actions
  stopped. If oETH supply becomes a practical constraint on new
  borrowing, a deliberate new oETH-issuance mechanism (unrelated to
  staking) would need to be designed — not attempted here.
- **`api/stake.go`'s `HandleStake` no longer returns an
  `estimatedReward` field**, and `api/staking.go`'s `HandleStaking`
  always reports `Rewards: 0` for current (non-legacy) positions —
  both previously promised a payout that no longer happens.
  `stakingStore.Reward()`/`core.RewardForStake()` themselves are left
  in place (not deleted) since `legacyPositions` and other call sites
  may still reference them; only the two call sites that fed a false
  promise to a user were changed.

## STETH_CCC1 as loan collateral (2026-08-11)

- **First collateral type backed by a real balance, not a pure
  in-memory counter.** (`api/apply.go`'s `applyLockCollateral`,
  `creditCollateralTo`, `collateralEscrowAddress`) Unlike CCT (staked
  position) and OETH (a receipt with nothing external behind it),
  locking STETH_CCC1 as collateral actually moves real vault-locked
  stETH out of the depositor's balance into a synthetic escrow address
  (`"COLLATERAL:STETH_CCC1"`) and back on release/liquidation — the
  same "real balance, really moved" pattern `applyStake` already
  established for ETH_CCC1 staking.
- **Seizure-to-lender can land at an unreachable address if the lender
  has never registered a self-custodial ETH address.**
  (`api/apply.go`'s `creditCollateralTo`) Releasing STETH_CCC1
  collateral back to the *borrower* is always safe — holding STETH_CCC1
  to lock in the first place requires having already registered one.
  But `liquidateLoan`'s seize-to-*lender* path calls
  `resolveBalanceAddress(recipient, "", "STETH_CCC1")` with an empty
  public key (the function has no pubkey parameter to pass one), which
  falls back to deriving a sub-address from an empty key if the lender
  has no `ethAddressStore`/`ethCustodialStore` entry. The result is
  deterministic (every node computes the same address, so consensus
  isn't at risk) but not something the lender's real wallet can easily
  discover or claim today. Not fixed here — would need either a
  required-registration gate before a STETH_CCC1-collateralized loan
  offer/negotiation can fund, or a claim-sweep mechanism triggered on
  registration (mirroring `redeemFreeLegacyStakeFor`'s "lazily
  re-check on a related event" pattern). Low practical likelihood (any
  lender who has ever touched the ETH/USDC/stETH vault already has one
  registered) but not structurally prevented.
- **STETH_CCC1 valuation uses a fixed 1 YEN-e unit == 1 USD cent
  assumption, not the live CCT↔YENE pool rate.** (`api/apply.go`'s
  `collateralValueInYen`) Real USD value comes from the admin-governed
  `EthUsdCentsPerEth` rate (stETH assumed ~1:1 with ETH, no separate
  stETH/ETH rate tracked); that USD-cents figure is then treated
  directly as YEN-e units. This was an explicit, considered design
  decision (not an oversight) — routing through the CCT↔YENE AMM pool
  instead would tie a real-asset collateral type's liquidation safety to
  a pool whose price a large trade can move, which is a worse property
  for this purpose, not a better one.
- **Proactive price-triggered liquidation (`processCollateralLiquidations`)
  now also sweeps STETH_CCC1 loans**, unlike the OETH-only original
  design — deliberately, since STETH_CCC1 tracks a real asset's real
  price where early liquidation protection matters more than it ever
  did for OETH's synthetic AMM-pool price. CCT stays excluded (no live
  price, same as before).
- **The self-funded/negotiation collateral-reservation path
  (`hasEnoughCollateral`, `reserveCollateralForLoan`,
  `peekCollateralSymbolForLoan`) prices STETH_CCC1 via the inverse of
  `collateralValueInYen`** (`stethCcc1WeiNeededForYenValue`), not a raw
  wei-vs-whole-unit magnitude comparison the way CCT/OETH's checks work
  — a raw comparison would be off by up to 18 orders of magnitude
  (nearly any nonzero wei lock would "cover" nearly any loan). The
  explicit loan-request flow (`api/loan_requests.go`,
  `meetsMinimumCollateralRatio`) has the analogous fix for its own
  minimum-ratio gate.
- **uint64 whole-amount ceiling.** Collateral amounts stay `uint64`
  throughout (never `DecimalTokenStore`/big.Int), the same convention
  `ethCcc1TransferAdapter` already uses for ETH_CCC1 staking — a single
  STETH_CCC1 lock/reservation is capped at roughly 18.4 stETH
  (`math.MaxUint64` wei). Not new to this feature (the identical ceiling
  has silently existed for ETH_CCC1 staking since that feature shipped),
  just newly relevant here too.

## External security audit remediation, phase 1 (2026-08-15)

A full external source-level security review (`creditchain-audit-initial-findings.md`)
surfaced 5 Critical, 3 High (2 already fixed the same day, see the H-01/
H-02 sections above), and 2 Medium findings. This section covers the
first remediation pass. All 5 Critical, 3 High, and 2 Medium findings
are now fixed (C-05 and H-03 as disclosed partial/safe mitigations
rather than the audit's fuller structural-redesign recommendation —
see their own sections below for exactly what's disclosed).
`DepositsHeldForSecurityRemediation` (below) has been lifted
accordingly as of 2026-08-16.

- **Deposits held network-wide pending remediation.** (`api/security_hold.go`)
  `DepositsHeldForSecurityRemediation` (a package-level `var`, default
  `true`) makes `applyBridgeDeposit`, `applyBridgeBtcDeposit`, and
  `applyVaultLockAttest` no-op immediately, before doing anything else -
  blocks every new ETHB/BTCB mirror-model credit and every new
  ETH_CCC1/USDC_CCC1/STETH_CCC1/BTC_CCC1 vault mint, forged or genuine,
  put in place specifically because C-01 and C-05 (below) made deposit
  accounting unsafe. Existing balances/withdrawals/transfers/loans/
  staking/collateral/governance are completely unaffected. Deliberately
  a compiled-in default, not a runtime/governance toggle - lifting it
  requires a deliberate source edit and redeploy. **Lifted 2026-08-16**,
  now that every Critical/High/Medium finding below has a fix in place
  (C-02 through M-02, deployed and tested) - `DepositsHeldForSecurityRemediation`
  is now `false`. Re-enable it the same way (flip the var, redeploy) if
  a new deposit-accounting-relevant gap is found before a genuinely
  independent re-review has happened; this codebase's own disclosed
  gaps (C-05's residual front-running-recovery gap, H-03's residual
  authenticated-but-semantically-invalid gap - see their sections
  below) were judged acceptable to leave the hold lifted over, not
  eliminated outright.
- **C-04 fixed: wallet recovery disabled on mainnet.** (`api/apply.go`'s
  `applyRecoverWallet`, `api/recovery.go`'s `HandleRecoverWallet`) The
  consensus-layer apply function never actually checked the emailed
  confirmation code - only the HTTP handler did, at submission time,
  which a transaction forged directly against `/tx/submit` skips
  entirely. The two remaining checks (linked email, account creation
  year) are both derivable from chain-visible/public data, not a real
  second factor, so anyone who knew a target's email and account year
  could migrate their wallet and permanently freeze the real owner out.
  Recovery is now blocked outright when `MAINNET_MODE=true` (both at
  the apply layer, the real fix, and the HTTP layer, for a clear error
  instead of a silently-dropped transaction) - untouched on testnet,
  where there's no real value to steal. Matches this mechanism's own
  pre-existing doc comment (`core/recovery.go`), which already disclosed
  it as "a testnet-only convenience... not a substitute for real
  account-recovery infrastructure... before any of this holds real
  value." Stays disabled on mainnet until a real redesign (precommitted
  recovery authority, time delay, dispute window) replaces it.
- **C-01 fixed: node-authored reports now require an authorized node
  identity.** (`api/apply.go`'s `isAuthorizedNodeIdentity`,
  `authorizedNodeReporterPublicKeys`) `/tx/submit` accepts any validly-
  signed transaction, and every "node-authored" report type
  (`TxBridgeDeposit`, `TxBridgeBtcDeposit`, `TxVaultReleaseComplete`,
  `TxSlashReport`, `TxBridgeTxHashRecord`) previously only checked that
  `Data == AuthPayload` - proof SOME key signed it, never that the
  signer was an authorized node. Anyone could forge a bridge-deposit
  report to mint unbacked ETHB/BTCB, forge a vault-release-completion
  report to make a real pending withdrawal permanently vanish from the
  relay's retry queue without the funds ever moving, or Sybil-flood
  slash reports to get honest validators banned. Fixed by requiring the
  signer to be either a real registered PoSR validator's delegated node
  key, or explicitly listed in a new `AUTHORIZED_NODE_REPORTER_PUBLIC_KEYS`
  env var (comma-separated hex Ed25519 public keys - same allowlist
  pattern `BRIDGE_VALIDATOR_ADDRESSES`/`ADMIN_MULTISIG_ADDRESSES`
  already use). Both mainnet and node1 had their own node identity's
  **public** key (never the private key - read directly from
  `data/identity.json`'s public field) allowlisted so legitimate self-
  reporting keeps working without needing the full multi-day,
  wallet-signed PoSR validator-onboarding pipeline
  (`api/validator_application.go`) to bootstrap first on a network that
  currently has zero registered validators. This is disclosed as a
  bootstrap accommodation, not the end state - a genuinely decentralized
  deployment should rely on real staked/community-approved validators
  once that pipeline has real participants.
- **C-05 fixed, fully, as of 2026-08-16 (superseding the original
  partial mitigation below): conflicting vault lock attestations no
  longer just get rejected - an honest claim and a fraudulent one now
  naturally diverge into separate records, so the honest one can still
  reach threshold and mint normally.** (`api/vault_lock.go`'s
  `vaultLockAttestationID`, `legacyVaultLockAttestationID`)
  The first pass (2026-08-15) fixed the immediate danger (a malicious
  validator's front-run claim silently absorbing honest votes) by
  rejecting any attestation whose claimed recipient/amount didn't match
  what was already on record for that event's ID - safe, but it
  explicitly disclosed a residual gap: because the ID still only
  covered chain + external tx hash + external index, a genuinely
  front-run real deposit would get permanently stuck (no funds at risk,
  no wrong mint, but no automatic recovery either), since every honest
  validator's correct attestation kept colliding with, and being
  rejected against, the attacker's already-stored record.
  The full fix implements the audit's own recommendation - folding the
  claimed recipient/amount into the attestation ID itself, so an honest
  claim and a fraudulent one for the same external event compute
  DIFFERENT ids and can never collide at all. The previously-disclosed
  "gets stuck" gap is closed: the fraudulent record still gets created,
  but it can never block or absorb votes intended for the real one -
  it just sits stuck at whatever vote count the colluding minority
  reaches, forever short of threshold, while the honest claim
  progresses normally on its own, separate, correctly-converging ID.
  **What made this safe to actually do** (the exact concern the
  original disclosure raised - `VaultLockAttestationStore` is a
  permanent, never-pruned audit trail of everything ever minted, and a
  naive ID-format change risks orphaning already-in-flight or already-
  minted historical records): `vaultLockAttestationID` checks whether a
  record already exists under the ORIGINAL (`legacyVaultLockAttestationID`)
  scheme first, and if so, continues using that exact same legacy ID -
  every node deterministically agrees on this by checking the same
  replicated store, so it can never cause disagreement about which ID
  "the" record for an event lives at. Only a genuinely NEW event (no
  legacy record yet) uses the new content-derived scheme, from its
  very first vote onward, so every honest validator attesting to a new
  event's real facts converges on the identical new ID from the start.
  This is a smaller, safer change than a hard cutover would have been -
  no historical or in-flight record's ID ever changes underneath it,
  only brand-new events get the improved scheme.
  **One caller needed updating**: `HandleVaultLockStatus` (the public
  polling endpoint a depositor's own frontend uses while waiting for
  their deposit to mint) previously could reconstruct the ID from just
  chain/tx-hash/index; a genuinely new event's ID now also needs the
  claimed recipient/amount, which only the depositor's own frontend
  reliably knows (it's their own deposit). Extended with optional
  `depositorCcAddress`/`depositorPublicKey`/`amountSmallestUnit` query
  params - `website/js/vault.js`'s `fetchLockStatus` and its three
  callers (`vault-deposit-eth.js`, `vault-deposit-btc.js`,
  `lido-stake.js`) now pass them. Omitting them still resolves any
  event still on the legacy scheme; only a brand-new event's status
  would be unavailable without them (a caller that hasn't been updated
  to send its own known deposit facts). New tests:
  `api/c05_full_fix_test.go` - differing claims for a brand-new event
  produce different IDs; two honest validators' identical claims
  converge on the same ID; an event already in flight under the legacy
  scheme continues on its original legacy ID rather than splitting.
  `api/c05_vault_attestation_conflict_test.go`'s existing regression
  test was strengthened to assert the actual improvement: the real
  depositor's claim now mints successfully despite a front-run attempt,
  which the partial fix alone could not guarantee.
- **C-03 fixed: stored XSS from unescaped user-submitted text rendered
  via `innerHTML`.** (`website/js/api.js`'s new `escapeHtml`/
  `escapeHtmlMultiline` helpers, loaded on every page before any
  page-specific script) A number of frontend pages interpolated
  server-returned free-text fields directly into `innerHTML` template
  strings with no escaping, letting attacker-controlled `<script>`/
  `onerror=`/etc. content stored in ordinary user-submitted fields
  execute in any other visitor's browser session (session-token/wallet-
  signing-flow theft, not just defacement). Confirmed-exploitable
  vectors fixed this pass, roughly in order of reach:
  - **Transaction summaries** (`tx.summary`, computed server-side in
    `api/explorer_summary.go` but embedding raw user text for several
    transaction types — governance proposal titles, Trading Hub
    application names, RC-infusion trust reasons) rendered unescaped in
    the wallet activity feed (`outernet.js`), the asset history list
    (`asset.js`), and the standalone transaction detail page (`tx.js`).
    Any wallet that ever transacts with (or merely appears alongside) a
    poisoned transaction would render it.
  - **Governance ballot titles/descriptions** (`b.title`/`b.description`
    on the live ballots list, `v.ProposalTitle` on a wallet's public vote
    history, `vote.title` on the block explorer's per-address vote
    history) — a proposal is attacker-authored by definition, and these
    three render locations put it in front of every voter, every visitor
    to that address's explorer page, and the proposer's own history.
  - **Block explorer search** (`explorer.js`'s `runSearch`) — the
    "No block or transaction matches" message interpolated the raw
    search query, and the search box is pre-filled from the page's own
    `?q=` URL parameter, making this a genuine reflected-XSS delivery
    vector via a crafted link (not just self-XSS from the box a user
    typed into themselves).
  - **Trading Hub / worker / token-registry / notification / multisig-
    proposal free text** — verified-hub directory names (`companies.js`,
    `validator.js`, `p2p.js`'s hub directory card), hub payment
    instructions and trade-terms text, chat messages, delegated-worker
    names, in-app notification text, user-registered custom-token
    name/symbol/peg-reference (`tokens.js` — note `symbol` is also
    server-validated to `[A-Z]{2,10}` in `core/token_registry.go`, so it
    was never actually exploitable, only `name`/`pegReference` were),
    and pending admin multisig-proposal action type/payload
    (`admin.js` — admin-only surface, fixed for defense-in-depth against
    one malicious signer targeting another signer's session).
  - A user log's status-history entries (`governance.js`) were also
    fixed, incidentally repairing what was already a broken (undefined-
    function, silently-failing) escape call before `api.js` gained the
    shared helpers.
  Left deliberately unescaped: `/token-standards` and `/reputation/math`
  render fields sourced from fixed, dev-curated backend constants (no
  user input reaches them, confirmed by reading the handlers), and
  several fields already flow through `.innerText` rather than
  `.innerHTML` (inherently safe regardless of content). Deployed to both
  node1 (testnet) and the public site (`creditchain.site`, which is the
  only origin that actually serves the frontend — see below).
  **Incidental infrastructure finding, not a vulnerability:** verifying
  this deployment surfaced that the mainnet Lightsail instance
  (3.39.124.210) runs only the `creditchaind-mainnet` backend on
  :9090 — no nginx, no web server of any kind is installed there. The
  `/opt/creditchain-mainnet/website/` copies made during this and
  earlier remediation passes are inert; `website/js/network-config.js`
  confirms the real architecture is a single static site (served from
  node1) that switches its API host/port at runtime between testnet
  (same-host :8080) and mainnet (`service.creditchain.site:9090`), not
  two independently-served sites. Frontend changes only ever need
  deploying to node1 going forward.
- **C-02 fixed: PoSR leader election is now deterministic AND enforced
  at block adoption.** (`core/validator_store.go`'s `List`, `api/peer.go`'s
  new `verifyLeaderOrFallback`) Two separate bugs, both closed:
  - **Nondeterminism.** `ValidatorStore.List()` ranged directly over
    the `Validators` map, whose iteration order Go deliberately
    randomizes. `core/consensus.go`'s `selectFrom` walks its candidate
    pool in order to do the weighted draw, so two honest nodes holding
    byte-identical validator state could still build differently-
    ordered slices and elect different leaders from the same previous-
    block hash - a silent, ongoing risk of nodes diverging on who's
    allowed to produce, not just a one-off bug. Fixed by sorting `List()`
    ascending by address - an arbitrary but canonical key every node
    independently reproduces from the same replicated state.
  - **Unenforced selection.** Block adoption (`adoptBlock` and the
    catch-up `syncChainFromPeer` loop) only ever verified that a
    block's producer was a real, authorized validator node
    (`verifyBlockProducer`) - never that it was actually the validator
    PoSR selection elected for that round. Any registered validator
    could therefore produce arbitrary consecutive blocks immediately,
    collecting every reward and pre-empting the intended leader. Fixed
    by `verifyLeaderOrFallback`, called right after the existing size/
    timestamp checks: the normal case requires `block.Validator` to
    exactly match `currentLeader()` (now deterministic per the fix
    above); an out-of-turn block is only accepted as the existing
    missed-block liveness fallback (`consensus_loop.go`'s
    `localAuthorizedValidator` path) if THIS node's own wall clock
    independently confirms `missedRoundsBeforeFallback *
    consensusTickInterval` (48s) has genuinely elapsed since the parent
    block - deliberately never trusting the block's own `Timestamp`
    field for that determination, since a producer fully controls it
    before signing (it's part of `core.ComputeBlockHash`'s preimage) and
    could otherwise just assert the window elapsed and produce
    out-of-turn immediately, defeating the whole check.
  New tests: `core/validator_store_test.go` (permutes insertion order,
  confirms `List()` always returns the same sorted result) and
  `api/c02_leader_election_test.go` (accepts the true elected leader
  immediately; rejects an out-of-turn validator's block that arrives
  with zero elapsed time; accepts one once the fallback window has
  genuinely elapsed; rejects a forged early `Timestamp` claiming the
  window elapsed when this node's own clock disagrees).
  **The audit's documented, not-attempted-here alternative - PoSR
  selection weighting using `float64`/`math.Pow` rather than integer
  fixed-point arithmetic - is now also fixed, as of 2026-08-17.**
  (`core/consensus.go`'s `validatorWeight`, `selectFrom`) The genuinely
  non-portable operation was narrowly `math.Pow(slashDecayFactor,
  float64(v.SlashCount))`: IEEE-754 guarantees the four basic operators
  (+, -, *, /) are bit-identical across every conforming platform, but
  gives no such guarantee for transcendental functions like `Pow` -
  different libm implementations can legitimately disagree in the last
  bit, which on genuinely heterogeneous validator hardware could mean
  two honest nodes computing different weights, and therefore different
  elected leaders, from identical replicated state. Rather than the
  narrower fix of only replacing `Pow`, the full pipeline now runs in
  integer fixed-point (scale `posrPrecision = 1_000_000`): stake and
  reputation are pure-integer ratios of already-`uint64` fields; the
  slash decay applies its 9/10-per-strike factor via bounded repeated
  integer multiplication (early-exits once the decay truncates to 0,
  so an inflated `SlashCount` can't turn this into an unbounded loop -
  in practice `SlashCount` never reaches this code above
  `PermanentBanSlashCountThreshold` anyway, since permanently-banned
  validators are filtered out upstream); and `selectFrom`'s cumulative-
  weight draw is likewise pure integer. `v.Uptime` itself stays a
  float64 *source* value (populated elsewhere via
  `ValidatorStatus.Uptime()`'s own basic, IEEE-754-safe division) and is
  converted to fixed-point at the single point of use with one
  multiply+truncate - also a basic, deterministic operation - since
  `Validator` doesn't carry the raw `BlocksProduced`/`MissedBlocks` pair
  needed to reconstruct an exact integer ratio at this call site.
  **Incidental fix alongside this rewrite**: `selectFrom` had a latent
  indexing bug - it built a separate `weights` slice that *skipped*
  zero-weight validators, then indexed the *original* pool with the
  shortened slice's index, silently misaligning `weights[i]` with
  `pool[i]` (and so potentially selecting the wrong validator) whenever
  a zero-weight validator sat anywhere but the very end of the pool.
  Fixed by pairing each weight with its original pool index instead of
  relying on positional alignment. New tests:
  `core/consensus_posr_fixedpoint_test.go` - max-input weight equals
  exactly `posrPrecision`; zero-input weight is exactly 0; stake beyond
  `StakeCap` isn't over-weighted; one-strike decay matches
  `posrPrecision*9/10` exactly, two-strike decay stays compounding (not
  linear); an extreme `SlashCount` (1 billion) still terminates promptly
  and decays to 0; 1,000 repeated calls with identical input return
  identical output; and a regression test placing a zero-weight
  validator in the middle of a 3-validator pool confirms both genuinely-
  eligible validators remain reachable across many seeds and the zero-
  weight one is never selected.
- **H-03 fixed, fully, as of 2026-08-16 (superseding the original
  partial mitigation below): a transaction can no longer permanently
  consume another transaction's ID at all.** (`api/apply.go`'s
  `applyTxOnce`, `replayKeyForTx`, `H03FixCutoverUnixSeconds`) The
  first pass (2026-08-15) closed the "free, no signature needed"
  version of the attack by moving the replay-set seen-marking to after
  `verifyTxAuth` passes, but explicitly disclosed a residual gap: an
  attacker who genuinely controlled SOME real private key could still
  sign a well-formed, authentically-signed transaction that copies a
  victim's `tx.ID` and passes `verifyTxAuth`, even though it's doomed
  to fail a *semantic* check deeper inside the type-specific apply
  function (e.g. insufficient balance) - `verifyTxAuth` only validates
  authenticity and static structure, not runtime state, so it couldn't
  catch that class.
  The full fix closes this completely: for any transaction timestamped
  at or after `H03FixCutoverUnixSeconds`, `replayKeyForTx` stops using
  the attacker-choosable `tx.ID` field as the consensus replay key at
  all and uses a hash of `tx.AuthSignature` instead - a value
  cryptographically bound to the transaction's own real signing event.
  An attacker cannot choose what replay key their transaction (forged
  or genuinely signed) consumes without already possessing the exact
  signature bytes of the transaction they're trying to poison, which by
  definition means it isn't a distinct transaction at all. This is
  smaller and safer than the audit's own suggested alternative
  (replacing `Transaction.ID` as the primary identifier everywhere and
  extending `core.ComputeBlockHash` to cover the full canonical
  transaction encoding, touching every historical block's hash-
  compatibility and every frontend/API reference to a transaction ID):
  `tx.ID` is never itself part of what a signature covers (see
  `core.SignedEnvelope`'s fields), so the server was always free to
  stop trusting it for this one internal purpose without touching
  anything externally visible - block hashes, historical IDs, API
  responses, and frontend tx-ID displays are all completely unaffected.
  Gated by `AuthTimestamp` (immutable, signed - same reasoning as
  `H01FixCutoverUnixSeconds`) so a node resyncing testnet's real history
  keeps deduplicating pre-fix transactions by `tx.ID` exactly as they
  were originally decided live. New tests: `api/h03_full_fix_test.go` -
  a genuinely-signed-but-unfunded forgery that copies a victim's
  `tx.ID` no longer prevents the victim's real transaction from
  applying (the exact residual gap this closes); two transactions
  sharing an `AuthSignature` but different `tx.ID`s produce the same
  replay key (proving the key is signature-derived); a genuine retry
  (identical transaction resubmitted) still correctly dedupes to
  exactly one application. `api/h03_tx_id_poisoning_test.go` (the
  original partial-fix tests) still pass unchanged, now exercising the
  pre-cutover/legacy-`tx.ID` code path.
- **M-01 fixed: peer-relayed and directly-submitted transactions are
  now validated before mempool admission, not just at block-apply
  time.** (`api/peer.go`'s `HandlePeerTx`, `api/submit_tx.go`'s
  `HandleSubmitTx`, `core/mempool.go`'s new `ValidateMempoolTimestamp`)
  Both endpoints used to insert whatever they deserialized straight
  into the FIFO mempool - `HandlePeerTx` behind the shared
  `NETWORK_SECRET`, `HandleSubmitTx` with no gate at all, since it's
  meant to be publicly reachable. Neither checked a signature, a
  timestamp bound, or even that the transaction was well-formed for its
  claimed type. An attacker (anyone, for `/tx/submit`; a `NETWORK_SECRET`
  holder or a peer relaying something it never validated itself, for
  `/peer/tx`) could continually fill the 5,000-entry mempool with
  invalid/future-dated garbage - FIFO eviction would then discard
  legitimate pending transactions to make room, and future-dated
  entries never aged out via `core.Mempool.Prune` at all, since Prune
  only drops a transaction once `now - tx.Timestamp` exceeds
  `MempoolTxExpiry` (15 minutes) - a large-enough future timestamp
  defers that forever. Fixed by running the exact same `verifyTxAuth`
  a transaction will independently be re-checked against at apply time
  (`api/apply.go`), plus a new `core.ValidateMempoolTimestamp` future-
  drift bound (120s, matching `MaxFutureBlockDrift`'s existing
  precedent for blocks), on BOTH endpoints before `mempool.Add` ever
  runs - a forged or implausibly-future-dated transaction now never
  occupies mempool space at all, rather than only failing once a
  producer eventually spends block capacity including it. `/tx/submit`
  also gained the `mempool.Has` dedup check `/peer/tx` already had
  (a pure hygiene improvement, not itself a security fix - prevents
  duplicate FIFO entries of an already-pending ID). Explicitly not
  attempted: the audit's "prefer an authenticated peer identity with
  revocation over one shared network secret" recommendation - that's a
  larger P2P-authentication architecture change than this pass's scope,
  and the shared-secret model is this deployment's existing, accepted
  design elsewhere too (see `NETWORK_SECRET`'s own doc comments); per-
  peer/IP admission quotas beyond the generic per-IP rate limiter
  already applied to every route (`api/hardening.go`'s `allowRequest`,
  20 req/s sustained / 40 burst) and the generic 64KB request-body cap
  (already covering the audit's "size" recommendation) were judged
  sufficient given that limiter's existing reach. New tests:
  `api/m01_mempool_dos_test.go` - both endpoints reject an
  unauthenticated transaction and an implausibly-future-timestamped one
  before either ever reaches the mempool; both still accept a
  genuinely valid, freshly-timestamped one.
- **M-02 fixed: the node identity file is now written and tightened to
  owner-only permissions.** (`api/node_identity.go`'s `loadNodeIdentity`)
  `data/identity.json` embeds the node's raw Ed25519 **private** key
  (used to sign every block this node produces), and was written with
  mode `0644` - world/group-readable on Unix, meaning any other local
  account on the host, an unencrypted backup, or a support bundle could
  read the full signing identity, not just its public half. Fixed by
  writing a freshly-generated identity at `0600` (owner read/write
  only), AND `os.Chmod`-ing the file to `0600` on every successful load
  of an existing one - a node upgraded from before this fix already has
  a `0644` file sitting on disk from its very first run, and this
  brings it into compliance automatically on the next restart, with no
  manual `chmod` step required. **Node identity keys already deployed
  under the old `0644` mode should still be treated as a disclosed
  historical exposure** per the audit's own recommendation ("rotate any
  key that may have been exposed") - this fix prevents the exposure
  going forward, it does not retroactively un-expose a key that already
  sat world-readable on a shared host; rotating node1/mainnet's
  identity keys (which would also mean re-registering each as its
  validators' `AuthorizedNodePublicKey` - see `core/validator_store.go`)
  is tracked as separate, disclosed follow-up work, not done as part of
  this pass. New tests: `api/m02_node_identity_permissions_test.go`
  (Unix-only, skipped on Windows) - a freshly-created identity file is
  `0600`; an existing `0644` file is tightened to `0600` on the next
  load.

## Operational bug fixes found re-seeding node1's validator (2026-08-18)

Node1's testnet validator set had gone empty at some point (separate,
undiagnosed incident - `state.json`'s `validators` map holds no replay/
reconciliation against `blockchain.json`'s history, so this is disclosed
as its own gap, not fixed here), leaving the chain producing blocks only
sporadically via the bootstrap fallback. Re-registering a validator to
restore normal production surfaced two real, unrelated bugs:

- **Fixed: `/wallet`'s `cctBalance` field always read 0, regardless of a
  wallet's real CCT balance.** (`api/wallets.go`'s `HandleWallet`,
  `api/ai.go`'s `get_my_wallet` AI tool) Both read `wallet.CCTBalance` -
  dead state only ever written by `core.RewardWallet`, which nothing in
  the live codebase calls anymore (CCT rewards moved to the real
  `cctStore` ledger well before this was noticed). Every wallet's
  `cctBalance` therefore always read 0 through these two call sites,
  regardless of a real, nonzero `cctStore` balance backing every actual
  transfer/stake/fee - invisible in normal use since the site's own
  dashboard (`outernet.js`/`wallet-page.js`) reads `/token/balance`
  (backed by `cctStore` correctly) rather than this field, but a real
  wrong answer for anything else reading `/wallet` directly, including
  the AI assistant's own "what's my balance" tool. Fixed by reading
  `core.CctSmallestToWholeFloor(cctStore.BalanceOf(address))` at both
  sites instead - the same whole-CCT-rounding helper already used
  elsewhere for this exact purpose (`core/staking_sync.go`). The dead
  `Wallet.CCTBalance` field and `core.RewardWallet` themselves are left
  in place (out of scope - a broader dead-code pass, not this fix). New
  test: `api/wallets_test.go`.
- **Fixed: minting or admin-recovering CCT to an address typed in its
  normal "cc"-prefixed display form silently created a second,
  unreachable balance under that literal 42-character string, instead
  of the real 40-character raw address every wallet actually signs
  with.** (`api/apply.go`'s `applyTokenMint`, `api/admin_transfer.go`'s
  `applyAdminTransfer`) `api/admin.go` already had exactly this
  normalization (`normalizeAdminAddress`, added for task #267's cosmetic
  address prefix) but only ever applied it to the configured admin/
  operator identity - never to an arbitrary caller-supplied destination
  address. `applyAdminTransfer` already normalized ETH-style checksummed
  addresses (`normalizeEthAddress`, closing an earlier, identically-
  shaped ETH_CCC1 incident - see that function's own doc comment) but
  had no equivalent for CreditChain's own address family, so the exact
  same failure mode was still fully open for CCT/REPT/etc. Both fixed by
  extracting the existing strip-leading-"cc" logic into a neutrally-
  named `normalizeCreditChainAddress` and applying it (chained with
  `normalizeEthAddress`, since the two only ever recognize non-
  overlapping prefixes) to `applyTokenMint`'s destination and
  `applyAdminTransfer`'s From/To, matching the precedent
  `normalizeEthAddress` already set for its own address family. New
  tests: `api/token_mint_test.go`, and a new case added to
  `api/admin_transfer_test.go` alongside its existing mixed-case ETH
  regression test.

## Moderator stake floor now survives delegation (2026-08-18)

- **Fixed: an active moderator could delegate away their own staked
  CCT and drop below the stake requirement they registered under, with
  nothing stopping it.** (`api/moderators.go`'s new `availableStakedCct`/
  `activeModeratorStakeShortfall`, `api/apply.go`'s `applyModeratorRegister`/
  `applyValidatorDelegate`, `api/validators.go`'s `HandleDelegateToValidator`)
  Staking and delegating-to-a-validator are two separate pools, both
  funded from the same liquid CCT balance but tracked independently
  (`core.StakingStore` vs `core.DelegationStore`) - `moderatorStakeRequirementCCT`
  (default 200 CCT) was only ever checked once, at registration time,
  against raw `stakingStore.StakeOf`, which delegation never touches.
  Nothing re-checked it afterward, so a moderator could register, then
  delegate their entire self-stake to a validator, and keep moderating
  with effectively 0 CCT actually still committed. Fixed by introducing
  "available staked CCT" (staked minus delegated-out, via the new
  `core.DelegationStore.TotalDelegatedBy`) as what both the registration
  check and a new delegation-time gate use: an active, non-deactivated
  moderator can no longer submit a delegation that would drop their
  available stake below their own requirement, at both the HTTP fast-
  fail and the authoritative apply-side check. Deliberately scoped to
  delegation only, not unstaking too (a real, adjacent gap - the
  `StakePurposeStore` "MODERATION" lock recorded at registration is pure
  bookkeeping today, never actually checked by `applyUnstake` - left
  as disclosed, not fixed here, since it's a separate mechanism/decision
  from what this pass addressed). New tests:
  `core/validator_delegation_test.go`, `api/moderators_test.go`.
- **Added: delegated-out CCT and available-staked-CCT now surface on
  `/staking`'s response** (`StakingResponse.DelegatedOutCCT`/
  `AvailableStakedCCT`) and on `staking.html`'s own position display -
  previously a wallet had no way to see how much of its staked CCT was
  actually still free vs. committed to backing a validator. New test:
  `api/staking_test.go`.

## Mutual post-trade review on the P2P Swipe page (2026-08-18)

- **Added: both sides of a successfully-settled P2P trade can now
  review each other, feeding a bounded reputation bonus.**
  (`core/trade_review.go`'s new `TradeReview`/`TradeReviewStore`,
  `api/trade_review.go`, `api/apply.go`'s `applyTradeReview`,
  `website/js/p2p.js`'s `renderTradeReviewPrompt`) No per-trade/per-
  counterparty review system existed before this - the only prior
  "Rating" (`core/rating.go`) is a one-per-wallet, one-time site
  rating, unrelated to any specific trade. Shown on the Swipe page's
  chat modal once a trade reaches a genuinely successful settlement
  (`CONFIRMED`, `AUTO_RELEASED`, `RESOLVED_RELEASE` - deliberately
  excludes `RESOLVED_REFUND`/`EXPIRED`, which are terminal but mean the
  trade fell through, not that it succeeded) and the current wallet
  hasn't reviewed the other side yet. 1-5 stars, optional comment;
  4-5 stars grant the reviewee `+1` reputation (matching
  `ModeratorConfirmReputationBonus`'s existing conservative precedent),
  1-3 is recorded but pays nothing.
  **Deliberate anti-farming design, discussed and confirmed with the
  user before building**: reputation is 55% of PoSR validator-selection
  weight and feeds governance voting power, so an uncapped review-to-
  reputation path would let two colluding wallets create trivial repeat
  trades, mark off-app payment as sent (never cryptographically
  verified), and mutually 5-star each other for unlimited reputation.
  Closed by capping the reputation payout at once per DIRECTED
  reviewer->reviewee pair, ever (`TradeReviewStore.RewardedPairs`) -
  the review itself is still always recorded (visible track record),
  only the reputation bonus stops after the first time a given reviewer
  has rewarded a given reviewee. Scoped to EXTERNAL-mode P2P offers
  only (where the Swipe page's chat modal actually lives) - ONCHAIN-
  mode offers settle atomically on accept and never reach this UI at
  all, so they're out of scope, not a gap. New tests:
  `core/trade_review_test.go`, `api/trade_review_test.go`.

## "Trust Score" rename + profile.html Node ID display bug (2026-08-18)

- **Renamed the reputation number's display label to "Trust Score"**
  (`reputation.html`'s nav tile and score-card heading, `profile.html`'s
  label) - the site had already organically converged on "Trust Score"
  everywhere else (`developers.html`, `governance.html`,
  `reputation.html`'s own explainer prose, `js/reputation.js`); only
  these two headings still said "Your Score"/"Reputation." Kept
  `reputation.html`'s own `<h1>Reputation</h1>` page/section title as-
  is - it's broader than just the score (covers History, Voting Power,
  REPT, Mining Power too). `dashboard.html`'s old "Reputation Score"
  heading was deliberately left untouched - confirmed orphaned (not
  linked from anywhere in the live site), not worth touching.
- **Fixed: `profile.html`'s "Node ID" field always showed "Not
  Connected," even for an active validator.** (`website/js/profile.js`)
  Same dead-field bug class as this session's earlier `wallet.CCTBalance`
  fix: it read `wallet.NodeID` (via `/wallet`'s response), a field
  nothing in the live codebase ever writes. A validator's real node
  delegation lives on `ValidatorStatus.AuthorizedNodeID` instead,
  exposed as `authorizedNodeId` on `/validators`' response - a fetch
  `profile.js` already made for the neighboring "Validator: Active/
  Inactive" line, just never read for this field. Fixed by sourcing
  Node ID from that same `/validators` lookup instead of the dead
  `/wallet` field. Verified directly against node1's live API/data
  before and after the fix; "Credit Score" and "Account Level" (also
  flagged as looking stale) turned out to be correctly wired already -
  they only looked broken when tested against a backend-less static
  server with no live API to answer their fetches.

## Operator token-mint endpoint now refuses on mainnet (2026-08-18)

- **Fixed: `/token/mint` (the admin-only CCT/token faucet) had no
  mainnet gate at all**, unlike the public `/faucet`, which already
  refused correctly (`errFaucetDisabledOnMainnet`). `HandleTokenMint`'s
  own doc comment already describes it as "an operator-only faucet for
  testnet seeding," but nothing enforced that - an admin credential
  used on mainnet (e.g. out of habit, exactly how this session used it
  to fund a testnet validator's stake) could mint tokens with no real
  backing into the real network's supply. Fixed at both layers, same
  "never trust submission-time state alone" split as every other gate
  in this file: `HandleTokenMint` now refuses with 503 before even
  evaluating admin auth (mirroring `HandleFaucet`'s exact ordering),
  and `applyTokenMint` - the authoritative, consensus-critical check -
  no-ops under `MAINNET_MODE=true` regardless of how a `TxTokenMint`
  transaction reached it. Confirmed mainnet's real systemd unit already
  has `MAINNET_MODE=true` set, so this fix is actually live there, not
  just correct in theory. New tests:
  `TestMainnetGate_TokenMintRefusedUnderMainnetMode`,
  `TestMainnetGate_ApplyTokenMintNoOpsUnderMainnetMode`,
  `TestMainnetGate_ApplyTokenMintStillWorksWithoutMainnetMode` (all in
  `api/mainnet_gate_test.go`, alongside the equivalent faucet tests).

## Marketplace platform fee redesigned: 5-way split, moderators funded (2026-08-18, part 1)

- **Redesigned `splitPlatformFee`** (`api/apply.go`) from the old
  50% Foundation / 30% burn / 20% validator-pool split to: **50%
  burned / 20% validator pool / 10% Foundation Treasury (protocol
  admin) / 10% Outernet operator / 10% moderator side** - a deliberate
  product decision, not a bug fix, worked through explicitly with the
  user including the anti-farming/economic-viability tradeoffs of
  each piece. `outernetOperatorAddress` is a new, genuinely separate
  destination from `treasuryStore.Treasury.FoundationAddress` - the
  user's own framing: Outernet is the operator running this specific
  interface, not the protocol itself, the same distinction a rented
  storefront has from the marketplace platform it rents space on.
  Deliberately a plain Go constant (not admin-settable like
  `FoundationAddress`) - changing it is a code change and redeploy.
- **Moderator dispute-resolution now gets paid directly, not pooled.**
  A trade that settles normally (no dispute) still routes its 10%
  moderator share into a new `platformFeeModeratorPoolAddress` pool
  (swept biweekly by reputation x uptime - not yet built, tracked as
  part 2 below). But a trade that reaches a moderator because of a
  genuine dispute, and gets resolved as a release (a real completed
  trade, so a real fee exists), now pays that 10% share directly to
  `tx.From` - the moderator who actually did the work - via a new
  `resolvingModerator` parameter threaded through
  `applyPlatformFee`/`releaseEscrowToBuyer`/`splitPlatformFee`.
- **Added moderator uptime tracking** (`core.ModeratorStatus.DisputesAssigned`/
  `DisputesResolved`/`Uptime()`) - deliberately scoped to disputes only,
  not every trade a moderator was ever assigned to: most trades never
  need a moderator at all, so counting every assignment would make
  every moderator's uptime look artificially low regardless of real
  responsiveness. `DisputesAssigned` increments the moment a P2P
  offer's SettlementStatus actually becomes `DISPUTED` (all three
  paths: seller-initiated, buyer-initiated, and the buyer-disagrees
  branch of `ConfirmBuyer`); `DisputesResolved` increments in
  `applyP2PModeratorResolve`, for either outcome (release or refund) -
  resolving a dispute either way is still doing the work. New/updated
  tests: `core/moderator_test.go` (Uptime, RecordDisputeAssigned/Resolved),
  `api/platform_fee_test.go` (5-way split, direct-vs-pooled moderator
  payment), `api/moderator_fee_test.go` (full dispute-open-then-resolve
  flow), `api/ccc1_transfer_fee_test.go` (updated for the new percentages).

## Marketplace platform fee redesign, part 2: refund fault-choice + biweekly moderator sweep (2026-08-18)

- **Implemented, superseding part 1's disclosed gap above**: refund-
  resolved disputes now still get the at-fault party a real
  accountability path, and the pooled moderator share now actually
  gets distributed.
- **`core.DisputeFaultChoice`** (`core/dispute_fault_choice.go`) - the
  moment `applyP2PModeratorResolve` resolves a dispute as a REFUND (a
  settlement that, per the pre-existing "never from a refund" rule,
  collects zero trade fee), it now creates a `DisputeFaultChoice`
  instead of immediately penalizing reputation. The at-fault party (the
  side the moderator found to be lying) gets `DisputeFaultChoiceWindowSeconds`
  (72 hours) to either pay the moderator's would-be 10% fee directly via
  `POST /dispute-fault-choice/pay` (`applyDisputeFaultPay`, no reputation
  penalty) or explicitly decline via `POST /dispute-fault-choice/decline`
  (`applyDisputeFaultDecline`, standard `ReputationDeltaP2PFraud` penalty,
  no fee taken). Scoped to "any at-fault party," not just hubs, per the
  user's explicit correction during design. If neither happens in time,
  `resolveExpiredDisputeFaultChoices` (called unconditionally from
  `ApplyBlock`, block-timestamp-driven for cross-node determinism) marks
  it DEFAULTED and applies BOTH the reputation penalty and a forced fee
  transfer - the user's own "if he does not choose he loses both" rule.
  The forced transfer is best-effort: `Transfer` silently no-ops on
  insufficient balance, same as every other forced-transfer path in this
  codebase - nothing here can force money that genuinely isn't there, so
  a truly empty at-fault wallet still gets the reputation penalty but may
  not actually pay.
- **`sweepModeratorFeePoolIfDue`** (`api/apply.go`) - every
  `moderatorFeePoolSweepIntervalSeconds` (14 days, block-timestamp-driven,
  gated on `treasuryStore.Treasury.LastModeratorPoolSweepAt`), distributes
  each native symbol's `platformFeeModeratorPoolAddress` balance across
  every currently-active (non-deactivated) moderator, weighted by
  reputation × uptime - computed in pure integer fixed-point
  (`moderatorWeightPrecision`, mirroring `core/consensus.go`'s
  `posrPrecision` pattern) rather than float64, and iterated in an
  explicit address-sorted order for the same "every node visits state in
  the same order" determinism this file's other sweeps already follow. A
  pool with nobody eligible to receive it (no active moderators, or every
  active moderator currently has 0 reputation or 0 uptime) is simply left
  alone until the next sweep - never burned, never stuck, just deferred.
- New tests: `api/dispute_fault_choice_test.go` (pay success, pay with
  insufficient balance leaving the choice pending, decline, the 72-hour
  timeout applying both penalties, an unexpired choice being left alone,
  the weighted multi-moderator sweep distribution including a deactivated
  moderator receiving nothing, and the sweep no-op before the interval
  elapses); `api/apply_test.go`'s
  `TestApplyP2PModeratorResolve_RefundOpensFaultChoiceInsteadOfImmediatePenalty`
  (replacing the old immediate-penalty-on-refund test, since that
  behavior no longer exists).
- **Still not implemented (disclosed, tracked as follow-up, not part of
  this pass)**: no frontend UI yet for the pay/decline choice - only the
  backend HTTP handlers (`HandleDisputeFaultChoicePay`,
  `HandleDisputeFaultChoiceDecline`, `HandleGetDisputeFaultChoice`) exist
  today, so an at-fault party currently has no way to act on a pending
  choice except a direct API call.

## Trading Hub shareable link: wallet creation, vaulting, and marketplace recommendation (2026-08-18)

- **Added a Trading Hub's own shareable invite link**
  (`website/hub-link.html?hub=<hub address>`) - a real-world-shareable
  URL a verified hub can hand out (flyer, social post, in person) that
  lands a new visitor on a choice: create a wallet to buy crypto,
  create a wallet and go straight to vaulting crypto to sell, or
  connect an already-existing wallet. Validates the hub is currently
  `companyStore.IsVerified` before showing any choice - an invalid or
  no-longer-verified hub shows a clear "can't be used right now"
  notice instead, never a broken/misleading flow. Reuses the site's
  EXISTING non-custodial wallet-creation flow (`register.html`, client-
  side keypair generation, no server-side custody) rather than
  inventing a new one - the only additions were passing a `hub` param
  through and, when selling, redirecting to `assets.html` (which
  already links to the ETH/USDC/BTC vault-deposit pages) instead of the
  general dashboard after account creation.
- **`core.Wallet.ConnectedHub`** (`core/wallet.go`) - deliberately
  separate from the pre-existing `ReferredBy` (a one-time referral-
  bonus attribution fixed at registration): `ConnectedHub` can be set
  (and changed, if the wallet later follows a different hub's link) at
  any point in a wallet's life, including long after it already
  existed. A brand-new wallet created through a hub link sets this
  directly at creation (`api/wallet_create.go`'s new
  `connectedHub` field on `createWalletRequest`, validated against
  `companyStore.IsVerified` - an unverified/nonexistent hub is silently
  ignored, same "harmless either way" precedent as `ReferredBy`). An
  ALREADY-EXISTING wallet connecting to a hub it discovered later
  (`hub-link.html` → "I already have a wallet" → `login.html?hub=...`)
  goes through the new signed `core.TxWalletConnectHub` transaction
  instead (`HandleConnectHub`/`applyWalletConnectHub`, api/apply.go) -
  signed so nobody else can silently attach a stranger's wallet to a
  hub they never chose; a failed/racy connect attempt (e.g. the hub got
  deactivated between sharing and clicking the link) never blocks the
  actual login, it just skips the attribution.
- **Marketplace recommendation**: the P2P Marketplace page
  (`website/js/p2p.js`) now fetches the current wallet's connected hub
  (`GET /wallet/connected-hub`) and, if set, shows a "Continue trading
  with [Hub]" banner plus sorts that hub's card to the front of the
  Trading Hub directory - the actual "recommend the hub that connected
  them" behavior, confirmed with the user as banner-plus-sort rather
  than a hard filter.
- **Fixed a real pre-existing gap found while designing this**: hub
  verification certificates (`applyCompanyUploadCertificate`,
  api/apply.go) used to be written to disk completely unencrypted.
  Now AES-256-GCM-encrypted at rest before writing, reusing the exact
  same key/helpers `api/hub_kyc.go` already established for hub-
  submitted trader PII (same trust model: defends against the file
  leaking if the underlying disk/backup is exposed, not end-to-end
  encryption the node operator is locked out of - see that file's own
  doc comment). Fails closed at both the HTTP fast-fail
  (`HandleUploadCertificate`) and the authoritative apply-time check if
  `HUB_KYC_ENCRYPTION_KEY_HEX` isn't configured, rather than ever
  silently falling back to plaintext.
- **Deliberately NOT built (confirmed with the user before starting)**:
  genuine on-chain encrypted storage with per-role (moderator/
  governance) decryption. The public verification facts a hub's
  credential is meant to prove - which moderator approved it, what
  date, what fee was paid - are already permanent on-chain plaintext
  today (`TxCompanyReview`/`TxCompanyApply` transactions, plus the
  executive-only `core.ModeratorVerificationEntry` audit log) and
  needed no new work. Building real on-chain ciphertext with multi-
  recipient role-based decryption would have been the first feature of
  either kind in this codebase - permanent chain bloat from storing
  ciphertext forever, plus real unsolved key-management questions
  (moderator deactivation, key rotation) - so the off-chain,
  already-established encryption pattern above was used instead.
- **Operational gap, not yet closed**: `HUB_KYC_ENCRYPTION_KEY_HEX` is
  NOT currently set on either node1 or mainnet's systemd unit - meaning
  hub certificate uploads (and the pre-existing hub-KYC submission
  feature it shares a key with) will fail closed with a clear error
  until the user provisions this key on both nodes.
- New tests: `api/wallet_connect_hub_test.go` (wallet-creation
  attribution for verified/unverified hubs, the signed connect-hub
  transaction's verified/self-connect/unverified-hub rejection paths,
  the public connected-hub lookup endpoint),
  `api/company_certificate_encryption_test.go` (encrypted-not-plaintext
  round trip, fail-closed behavior with no key configured at both the
  HTTP and apply layers).
- Inherits the existing, already-disclosed limits of the underlying
  "Verified Trading Company" system this whole feature sits on top of
  (see `core/company.go`'s doc comment, referenced earlier in this
  file under Moderator attestation) - it's a testnet-only placeholder
  badge, not real KYC/AML/licensing verification, and this feature
  doesn't change that.

## Site-wide clean URLs, no .html extension (2026-08-18)

- **Every internal link across all 51 `website/*.html` pages and
  `website/js/*.js` files now points at the extension-less form**
  (e.g. `wallet` instead of `wallet.html`) - 248 quoted navigation
  targets rewritten (href attributes, `location.href` assignments,
  template-literal hrefs/redirects), leaving bare comment mentions of
  filenames untouched. `index.html` collapses to the site root (`/`)
  rather than `/index`, matching how the homepage was already linked
  everywhere.
- **`cmd/staticserve/main.go` rewritten** to serve these clean URLs: an
  extension-less request that matches a real page (e.g. `/wallet`)
  transparently serves that page's `.html` file without a visible
  redirect; a direct hit on the old `*.html` URL 301-redirects to the
  clean form (query strings preserved) so existing bookmarks/search-
  engine links still resolve instead of 404ing outright. New tests:
  `cmd/staticserve/main_test.go`.
- **Fixed a real, unrelated pre-existing gap discovered while doing
  this**: the `cmd/staticserve/main.go` source actually committed to
  this repo had NO TLS support at all (plain `http.ListenAndServe`),
  while node1's real production `creditchain-web.service` unit has
  always passed it `TLS_CERT_FILE`/`TLS_KEY_FILE` and genuinely serves
  HTTPS on port 443 - meaning the repo's own source could never have
  produced the binary actually running in production. (This traces
  back to an earlier session's doc comment noting the production
  binary had "no matching source anywhere in this repo... This
  restores that" - the restoration was incomplete.) Now reads those
  same env vars and calls `http.ListenAndServeTLS` when both are set,
  falling back to plain HTTP with a loud warning for local dev -
  matching real production behavior rather than diverging from it.
- Deploy note: both changes ship together in the same
  `creditchain-web.service` binary/restart on node1 - the clean-URL
  logic and the TLS fix are not separable in this codebase's current
  build.

## Stuck vault release investigated: stale-signature bug fixed, flat-fee-eats-small-withdrawals gap disclosed (2026-08-18)

Investigated mainnet's `creditchain-mainnet.service` logging "vault
release relay: release() call failed... insufficient distinct valid
signatures" on a repeating 30s loop. Root-caused via direct on-chain
inspection (public RPC `eth_call` against the deployed
`CreditChainVault` contract's `threshold()`/`validators(i)` getters,
independently reconstructing the release digest and running
`crypto.SigToPub` against the stuck approval's stored signature) rather
than guessing from logs alone - found two separate, real issues:

- **Fixed: a signature made by a since-rotated `VAULT_RELEASE_SIGNING_KEY_HEX`
  got stuck forever.** Both `scanVaultReleaseApprovalsForAutoSign`
  (`api/vault_release_approve_watch.go`) and `applyVaultReleaseApprove`
  (`api/vault_lock.go`) only ever checked "has this validator's wallet
  address signed this approval AT ALL" before permanently skipping it -
  never "does what's stored still match this validator's CURRENT
  signing key." Once `VAULT_RELEASE_SIGNING_KEY_HEX` rotates (for
  whatever reason - a deliberate key rotation, a config fix), any
  approval that validator had already signed under the OLD key stays
  wedged under its wallet-address entry forever: the relay keeps
  resubmitting the same stale, now-permanently-invalid signature bundle
  every 30s, and the vault contract's own on-chain ecrecover correctly
  keeps rejecting it (`distinctValid` never reaches `threshold`) since
  the recovered address matches neither on-chain validator. Confirmed
  this was the exact mechanism for a real stuck mainnet approval
  (`7a48cf30fb65701c253147cf2361bad3`) - the stored signature
  independently verified to recover to a third address matching
  neither on-chain validator NOR that same wallet's own
  currently-registered `EthSigningAddress`.

  Fixed by adding `signatureMatchesAddress` (verifies a stored
  signature still recovers to the CURRENT signing address for the
  approval's own digest) - the auto-sign loop now re-signs and
  resubmits whenever the stored entry is missing OR stale, and
  `applyVaultReleaseApprove` now allows a validator's resubmission to
  overwrite their own prior entry rather than refusing it outright.
  Safe to allow: `tx.AuthAddress` is already independently verified as
  genuinely that registered validator by the transaction's own
  signed-envelope pipeline before this function runs, and any
  resubmitted signature still has to independently pass the vault
  contract's own on-chain ecrecover check before real funds move - this
  map is coordination bookkeeping, not itself an authorization
  boundary. New tests: `api/vault_release_stale_signature_test.go`
  (`signatureMatchesAddress`'s own match/mismatch/malformed cases, and
  `applyVaultReleaseApprove`'s overwrite behavior).

- **Disclosed, NOT fixed - needs a product decision**: `splitVaultReleaseFee`
  (`api/vault_fee.go`) charges a FLAT gas-estimate fee for ETH/USDC
  releases regardless of withdrawal size. If the fee exceeds the
  requested amount, `release.Sign() < 0` clamps to `release = 0, fee =
  amount` - the user's ETH_CCC1/USDC_CCC1 was already burned
  (irreversibly, in `applyVaultBurn`, before this split ever runs), and
  they receive a technically-valid but literally-zero-value release.
  This is exactly what happened to the stuck approval above
  (`AmountSmallestUnit: "0"` against a real, nonzero
  `PendingFeeAmountSmallestUnit`) - whoever burned that ETH_CCC1 lost it
  entirely to the fee, and no code fix can recover it after the fact
  (the burn already happened). Open questions for the user, not
  something to unilaterally decide: should a withdrawal request that
  would net to zero be REJECTED upfront at `applyVaultBurn` time (never
  burns in the first place) rather than silently succeeding into a
  worthless release, and/or should the fee be capped as a percentage of
  the withdrawal instead of always flat. Whether to make the specific
  affected wallet (`49b4e343344e1aa9d49be55a23ef0024e03310fc` on
  mainnet) whole is a business decision, separate from any code change.

- **Follow-up, same day: built the remediation the user chose.** User
  confirmed the affected wallet should be made whole, and asked "how,
  where did the value go, who got the fee" first - traced precisely
  through the code (not from memory) to confirm nobody was ever
  credited (fee-crediting only ever happens inside
  `applyVaultReleaseComplete`, which only runs after a REAL successful
  on-chain `release()` - this one has never once succeeded) and the
  real ETH never left the deployed vault contract (a reverted Ethereum
  transaction rolls back completely - nothing on-chain changed). Built
  `core.TxAdminRefundStuckVaultBurn` (`api/admin_refund_stuck_vault_burn.go`,
  new `core.VaultReleaseRefunded` terminal status) - a narrow,
  incident-specific admin action mirroring the existing
  `TxAdminNormalizeCcc1Balance` precedent exactly: admin-gated (same
  `requireAdminAuthed`/multisig-aware trust model as every other admin
  action), only ever reachable for an approval that is STILL PENDING
  (refuses outright once RELEASED, since the real value genuinely left
  the vault in that case and refunding too would double-spend), refunds
  the FULL original burned amount (release + the fee that was never
  credited to anyone) back to the burner's own real, already-registered
  self-custodial address (resolved via `ethAddressStore` directly -
  refuses rather than guess a fallback address if no real registration
  exists), and marks the approval REFUNDED so it can never be
  signed/relayed/refunded a second time. New admin.html/admin.js card
  ("Refund a Stuck Vault Release Burn") wired to the same
  `apiPostSigned` + multisig-proposal pattern every other admin action
  on that page already uses - the user's own admin multisig signers
  execute it, Claude never holds or enters the admin key itself. New
  tests: `api/admin_refund_stuck_vault_burn_test.go` (full refund,
  refuses an already-RELEASED approval, not replayable, refuses without
  a resolvable destination address).
- Deliberately does NOT fix the underlying flat-fee-eats-small-withdrawals
  design gap above - this only remediates the one already-affected
  wallet/approval. The design decision (reject upfront vs. cap the fee
  as a percentage) is still open.

- **Follow-up, same day: closed the design gap itself.** User asked to
  fix the flat fee so small withdrawals stop getting eaten - chose
  "reject upfront" (never burn a withdrawal that can't survive its own
  fee) over "cap the fee as a percentage," since it directly eliminates
  the exact failure mode without changing the fee's real-gas-cost basis.
  `applyVaultBurn` (`api/vault_lock.go`) now computes the ETH/USDC
  release/fee split BEFORE burning anything (previously this was only
  ever computed after) - if the split would leave a release amount of
  `"0"` (fee ≥ requested amount), the burn never happens at all and no
  release-approval record is created, rather than silently burning the
  user's balance into a worthless $0 release. `HandleVaultBurn` gained
  a matching fast-fail with a clear, actionable error message
  (surfaced verbatim in `vault-withdraw.js`'s existing "Withdrawal
  failed: " + e.message handling - no frontend change needed). Scoped
  to ETH/USDC only - BTC's fee is a percentage of the amount (bounded
  ≤100% by construction), so it never had this failure mode. New tests:
  `api/vault_burn_fee_floor_test.go` (refuses a too-small withdrawal
  with the balance completely untouched, allows a comfortably-larger
  one through unchanged, confirms BTC is unaffected by the new check,
  confirms the HTTP-layer fast-fail returns a clear 400).

## KMS-backed BTC vault signer built, closing the "LOCAL DEV FALLBACK" gap (2026-08-19)

- **Fixed**: `InitBtcVaultSigner` (`api/vault_btc_signer.go`) previously
  always fell back to `localDevBtcVaultSigner` (a real secp256k1
  keypair held only in process memory, lost on restart) regardless of
  `BTC_VAULT_KMS_ENABLED` - a disclosed, honest "not yet implemented"
  gap, not a silent one, but a real production-custody hole
  nonetheless (unlike the ETH custodial signer, which has used real AWS
  KMS since an earlier session). Now mirrors `InitEthSigner`
  (`api/eth_signer.go`) exactly: a new `kmsBtcVaultSigner` provisions a
  real AWS KMS `ECC_SECG_P256K1` key (the identical curve Ethereum
  uses, so this deliberately reuses `eth_signer.go`'s
  `parseKMSPublicKey`/`padTo32`/`ecdsaDERSignature` helpers rather than
  duplicating secp256k1/KMS plumbing that was never actually
  Ethereum-specific), returns the compressed public key
  `core.BridgeValidator.BtcPublicKeyHex` expects, and signs by asking
  KMS for a raw ECDSA digest signature, then re-encoding through this
  codebase's own `btcec/ecdsa` `Signature` type after a low-S
  correction (Bitcoin's own BIP62/BIP146 standardness requirement,
  the same malleability-prevention rule Ethereum's EIP-2 enforces) -
  genuinely simpler than the ETH signer, since BTC's witness
  verification needs no recovery-id brute-force at all. Falls back to
  the local dev signer with the same loud warning if
  `BTC_VAULT_KMS_ENABLED=true` but AWS config can't load, same
  degrade-gracefully posture as every other signer in this codebase.
- New tests: `api/vault_btc_signer_test.go` -
  `TestKMSBtcSignatureReconstruction` (deliberately forces a real
  signature into HIGH-S form to prove the correction branch actually
  runs, not just the pass-through case, then verifies the reconstructed
  signature against the real public key), `TestKMSBtcVaultSigner_NewKeyProducesCompressedPublicKey`
  (confirms the compressed-pubkey derivation matches a direct
  compression of the same keypair). Both exercise the real
  parsing/correction/serialization logic without needing a live AWS
  connection, mirroring `eth_signer_test.go`'s established technique for
  the ETH signer.
- **Not yet done**: actually provisioning a real KMS key and setting
  `BTC_VAULT_KMS_ENABLED=true` on mainnet - that's an operator action
  (real AWS credentials/billing), not a code gap. Until then, mainnet's
  BTC vault custody remains on the local dev fallback, same as before
  this fix - this closes the "the code doesn't exist" gap, not the
  "it's configured in production" gap.

## Dispute fault-choice frontend UI built (2026-08-19)

- **Fixed**: the pay-or-decline mechanic for a refund-resolved dispute
  (`core.DisputeFaultChoice`, `api/dispute_fault_choice.go`) had
  working backend handlers (`HandleDisputeFaultChoicePay`/`Decline`/`Get`)
  since the marketplace-fee-redesign work earlier this session, but no
  frontend - the at-fault party had no way to act on a pending choice
  except a direct API call. New `renderDisputeFaultChoicePrompt`
  (`website/js/p2p.js`), wired into the existing P2P chat modal
  alongside `renderTradeReviewPrompt` (same render-on-modal-open
  pattern): shown ONLY to the wallet that's actually
  `choice.atFaultAddress`, only while `SettlementStatus ==
  "RESOLVED_REFUND"` and the choice is still `PENDING` and before its
  deadline - a public read (`GET /dispute-fault-choice/status`), same
  "everyone in the dispute already knows the outcome" reasoning as the
  rest of this dispute machinery, but the UI itself only renders for
  the one wallet it actually applies to. Shows the fee amount, a
  render-time "time remaining" readout, and Pay/Decline buttons
  (`apiPostSigned` to the existing endpoints) - a client-side deadline
  check hides the prompt once passed rather than offering a choice
  that's about to be rejected as expired (the real, authoritative
  expiry is still `resolveExpiredDisputeFaultChoices`, block-timestamp-
  driven, unchanged). Verified live in-browser: renders correctly
  inside the chat modal with the fee amount, countdown, and both
  buttons all present and correctly wired, no console errors.
- Pure frontend change - no Go code touched, no new tests needed beyond
  the existing backend test coverage this UI now exposes.

## Postgres dual-write is now live on mainnet (2026-08-19)

- **Provisioned by the user**: a real AWS RDS PostgreSQL instance,
  `DATABASE_URL` set on mainnet's `creditchain-mainnet.service`.
  `api/db.go`'s `InitDB()` (already fully wired in code since an
  earlier session - see this file's "Fixed the validator-delegation-
  hijack gap" era entry - this was a provisioning gap, never a code
  gap) now genuinely connects, confirmed via the log line
  `"Postgres dual-write enabled for node: mainnet"` (which only fires
  after a real successful connect + ping + `CREATE TABLE IF NOT EXISTS
  state_snapshots`, not merely "the env var is present"), zero
  `"Postgres snapshot write failed"` lines since the restart, and
  continued normal block production throughout.
- **Real incident during setup, resolved**: a pre-existing, empty
  `DATABASE_URL` placeholder inside a `postgres.conf` systemd drop-in
  (dated 2026-08-16, three days before this setup - evidently staged
  in advance for exactly this moment by an earlier session) was
  silently overriding the real value once added to the main unit file,
  since systemd drop-ins load after and override the base unit for a
  conflicting key. Symptom: the live process's environment appeared to
  have `DATABASE_URL` set (a naive `grep` on the raw NUL-delimited
  `/proc/<pid>/environ` even falsely appeared to confirm this), yet the
  startup log still read `"DATABASE_URL not set"` - the log line (the
  application's own `os.Getenv` check) was the authoritative signal,
  not the raw environ grep. Fixed by removing the redundant line added
  to the main unit file and instead replacing the pre-existing empty
  value inside `postgres.conf` directly (matching this session's
  established "match by key name, replace value" secret-rotation
  discipline). **Lesson for next time**: always parse
  `/proc/<pid>/environ` through `tr '\0' '\n'` before grepping it - a
  raw grep against a NUL-delimited file can produce a misleading match
  count - and treat the application's own startup log as more
  authoritative than a raw environment dump when the two disagree.
- Node1 (testnet) was NOT given a DATABASE_URL in this pass - mainnet
  only, per the immediate need. Same setup would apply if desired for
  node1 later.

## CRITICAL, STILL OPEN: mainnet vault contract's on-chain threshold is 1 - either single bridge validator can unilaterally drain the vault (2026-08-19/20)

**Not yet fixed - flagging prominently for the next audit pass.** Discovered
while investigating a user-reported "deposits not working" ticket (which
turned out to be a false alarm - the deposit had simply not yet been made;
the investigation surfaced this instead). Verified directly against the
live deployed contract via three independent public RPC providers
(1rpc.io, blastapi.io), not just CreditChain's own backend config:

- **On-chain `threshold()` on `CreditChainVault.sol` at
  `0xE3C27466943903B390d06Cddb265263091b2d752` (Ethereum mainnet) = 1.**
  Matches the backend's own `BRIDGE_VALIDATOR_THRESHOLD=1`
  (`bridge-validator.conf`) - both layers agree, this isn't a
  frontend/backend mismatch, it's a deliberate-or-forgotten deployment
  configuration.
- **Two validators are registered on-chain**:
  `0x82c4e688d52f4a6a1a2fede737d29cb5ff25495d` and
  `0x7864a654505f2e2d7e5c2fe33014df2c25c2fee4`.
- **Concrete consequence**: `release()`'s own guard
  (`require(distinctValid >= threshold, ...)`, `contracts/CreditChainVault.sol`)
  is satisfied by ONE valid signature from EITHER of those two addresses
  alone. Either validator, acting alone - compromised, dishonest, or
  physically coerced - can today sign a `release()` call moving the
  vault's entire real ETH/USDC balance to any address of their choosing,
  with zero cooperation required from the other validator. The same
  single-validator-sufficient threshold also applies to
  `TxVaultLockAttest` on the mint side (`BRIDGE_VALIDATOR_THRESHOLD=1`),
  meaning a single validator can also mint unbacked ETH_CCC1/USDC_CCC1/
  BTC_CCC1 by attesting to a deposit that never happened.
- **Current exposure is small but real**: vault balance at time of
  discovery was ~0.004 ETH (~$8) - low stakes today, but this is a
  structural configuration gap that scales directly with whatever value
  accumulates in the vault, not something that becomes safer with more
  usage.
- **Why raising `threshold` alone isn't sufficient**: it only adds real
  security if the additional required signers are genuinely independent,
  separately-held keys. If the same operator (or anyone who could access
  both) currently controls both registered validator keys, a higher
  threshold requiring signatures from the same underlying party changes
  nothing - still one real point of failure, just needing multiple
  signatures FROM it.
- **User's explicit concern, verbatim**: coercion/physical-security risk
  at scale ("if theres a vault with 1 million$ and even 7 bridge
  validators people can kidnap all 7 validators to withdraw that vault") -
  correctly identified that raw M-of-N multisig alone doesn't solve
  coercion if all N are simultaneously reachable, and that a naive
  higher threshold doesn't help if it's not paired with genuine signer
  independence.
- **Direction chosen, not yet built** (see the same-day design
  conversation, not yet a written proposal file): move toward a
  trust-minimized, proof-based bridge instead of (or as a defense-in-depth
  layer alongside) federated validator attestation - a genuine Ethereum
  light client (evaluated: a16z's Helios, MIT-licensed, confirmed working
  in a live manual test against mainnet's real Infura endpoint on
  2026-08-20 - produces genuinely consensus-verified block headers with
  correct `receiptsRoot`/`stateRoot`, and serves working `eth_getLogs`
  queries; `eth_getBalance`/`eth_getProof` currently return "distance to
  target block exceeds maximum proof window" and need further
  investigation, though this doesn't block the receipt/log-proof path
  the deposit-verification use case actually needs) combined with
  Merkle-Patricia proof verification via `go-ethereum`'s own
  `trie.VerifyProof` (already a vendored dependency, zero new crypto
  code needed for that specific primitive) to verify deposit events
  directly against real Ethereum consensus, removing the human
  validator (and therefore the coercion target) from the mint path
  entirely. Not yet wired into the actual codebase - Helios was only run
  as a manual, temporary foreground process on mainnet for this proof-
  of-concept, deliberately not persisted as a service yet.
- **Immediate, much simpler mitigations NOT yet applied, worth doing
  regardless of the larger light-client effort**: raising the on-chain
  `threshold` to require 2 genuinely-independent signers (once a second
  real, separately-controlled operator exists - see the "more
  validators" gap elsewhere in this file), a withdrawal delay/veto
  window, per-transaction/per-day velocity caps, and a duress-signal or
  anomaly-detection gate before `release()` executes - all discussed,
  none implemented yet.
- **This is exactly the kind of finding a fresh external audit should
  independently verify and weigh in on** - flagging it here specifically
  so it isn't lost/deprioritized before the next audit engagement.

## Bridge validator allowlist removal caused a real production outage, reverted same day (2026-08-20)

- **What happened**: the "allowlist removed entirely" change described below shipped to mainnet, and within the hour broke real production signing. Mainnet has exactly ONE real, currently-active PoSR validator. The already-deployed `CreditChainVault.sol` contract's own on-chain `validators[]` only recognizes TWO addresses (`0x82c4e688...`/`0x7864a654...`, from the original incident's threshold=1 finding above) - these belong to the two CreditChain wallets (`174db2f081f217e3077eb8a382d0e7f92feefd35`, `5457b6cea59504fa53f685bf25674896d0fe4d2c`) registered as bridge validators. Neither of those two wallets is CreditChain's one real PoSR validator. The moment PoSR-only eligibility went live, both of the only wallets capable of producing a contract-valid signature lost eligibility to even submit `TxVaultReleaseApprove` at all - a real pending withdrawal (`b6a9b3c3c9d023e7c5d23b3a1496cd05`) got stuck with zero eligible signers, surfaced by the user as "sign and approve... showing its broken button."
- **Why the "just become a real PoSR validator" fix isn't quick**: checked `HandleRegisterValidator` - becoming a real validator requires `validatorApplicationStore.IsApproved(address)` (a governance-support + emailed-confirmation application process), a registration fee, AND staking >= `core.MinValidatorStake` (1000 CCT) - neither bridge-validator wallet has that much CCT (196 and 0 respectively at time of discovery). This is a real multi-step process, not a single transaction - not something to block a live, already-pending withdrawal on.
- **Reverted same day**: `BRIDGE_VALIDATOR_ADDRESSES` restored as a valid eligibility path ALONGSIDE `isRealPoSRValidator` (the pre-2026-08-20 additive design - see `api/bridge_validator.go`'s current header comment). The 6-10 block release confirmation-depth requirement (below) was NOT touched by this revert - that part shipped fine and stays PoSR-eligibility-agnostic. Env var was still configured on mainnet's systemd unit the whole time (nothing lost), so restoring the code path alone was sufficient - no config change needed.
- **Real fix, still not done**: get `174db2f081f217e3077eb8a382d0e7f92feefd35` and/or `5457b6cea59504fa53f685bf25674896d0fe4d2c` (whichever the operator actually controls the key for) funded to >= 1000 CCT and through the real validator application + registration process. Once done, that wallet's existing bridge-validator registration (already on file, no need to redo) becomes independently PoSR-eligible and the allowlist entry for it stops being load-bearing. Only remove the allowlist again once true for every currently-necessary signer - verify via `/validators` and `/bridge-validator/list` on the target network first, don't assume from a prior session's state.
- **Process lesson**: before removing a fallback/legacy eligibility path on a LIVE system, check whether anything *currently in production* depends on it, not just whether the replacement mechanism is theoretically sound. The additive PoSR eligibility fix itself was correct and well-tested; the mistake was in the SECOND step (full allowlist removal) - deploying it without first checking `/validators` + `/bridge-validator/list` together against the specific target network's actual state.

## Bridge validator eligibility tied to real PoSR validators, allowlist removed, and a 6-10 block release confirmation depth added (2026-08-20) - PARTIALLY REVERTED, see entry above

- **Fixed (partial mitigation, not a full solution) for the threshold=1
  finding above**: `isBridgeValidatorAddress` (`api/bridge_validator.go`)
  previously had exactly one eligibility path - a small, manually-
  curated `BRIDGE_VALIDATOR_ADDRESSES` allowlist with zero connection to
  CreditChain's own real, economically-secured PoSR validator set. An
  intermediate design (same day, superseded before deploy) kept that
  allowlist AND added a second, additive path. Per explicit direction,
  the allowlist was instead **removed entirely**: `isRealPoSRValidator`
  is now the ONLY eligibility path, granting it to any address that is
  a genuine, currently-active CreditChain validator (`Active`, not
  `PermanentlyBanned`, staked >= `core.MinValidatorStake`, reputation >=
  `core.MinValidatorReputationGate` - the exact same bar
  `core.isEligible` uses for PoSR block-production eligibility, computed
  independently from the same stores since that function takes a
  different, consensus-hot-path-specific type this call site has no
  reason to reconstruct). `BRIDGE_VALIDATOR_ADDRESSES` no longer has any
  effect on eligibility.
- **New, same day: a 6-10 CreditChain-block confirmation-depth
  requirement on the release side** (`core.VaultReleaseConfirmationDepthBlocks`,
  set to 8 - the midpoint of the requested 6-10 range). A burn
  (`TxVaultBurn`) now stamps `core.VaultReleaseApproval.BurnBlockHeight`
  with the CreditChain block it was applied in
  (`applyVaultBurn`/`api/vault_lock.go`), and no bridge validator's
  signature is accepted - at the HTTP submission boundary
  (`HandleVaultReleaseApprove`), at consensus apply time
  (`applyVaultReleaseApprove`, the safety-critical, deterministic
  re-check every node performs identically), or by the auto-sign watch
  loop (`scanVaultReleaseApprovalsForAutoSign`,
  `api/vault_release_approve_watch.go`) - until
  `currentChainHeight() - BurnBlockHeight >= VaultReleaseConfirmationDepthBlocks`.
  This replaces the previous "act on it as soon as seen" behavior with a
  real finality buffer, guarding against a short-lived CreditChain-side
  fork/reorg unwinding a burn before an irreversible external release
  (real ETH/BTC/USDC leaving the vault) has happened. Mint-side
  (deposit/lock-attestation) confirmation depth is a separate, pre-
  existing concept (external-chain confirmations, enforced by the watch
  loop that submits `TxVaultLockAttest` in the first place) and was not
  changed.
- **What this does and doesn't fix**: bridge-validator trust now scales
  automatically with real network decentralization AND is no longer
  reachable through a fixed list at all - as more genuine, independent,
  staked validators join CreditChain's own consensus, they automatically
  become eligible to also co-sign vault attestations/releases, with zero
  further code or config changes needed, and a manually-curated key list
  can no longer bypass that. Releases also now require real CreditChain
  finality (6-10 blocks) before any signature is even collected. Neither
  change raises the on-chain `threshold()` (still 1, still the live
  critical gap documented above), and neither eliminates human signers
  the way a full proof-based release verification would (see below) -
  together they narrow WHO can sign (tied to real, growing validator
  status instead of a fixed list) and WHEN they may sign (only after
  real finality), not the fundamental trust model. Given mainnet
  currently has only 1 real active validator, the practical security
  uplift from the eligibility change alone is small today - it's
  infrastructure that pays off as validator count grows.
- **Explicitly NOT built, scoped and discussed but out of reach for now**:
  a genuinely signer-less release path, where Ethereum's
  `CreditChainVault.sol` independently verifies a CreditChain consensus
  proof (mirroring the Helios/Merkle-proof deposit-verification
  direction, but in the opposite direction) instead of trusting any
  validator's signature at all. Real obstacle found: CreditChain signs
  its own blocks with Ed25519, which Ethereum has no cheap native way to
  verify (EIP-665, proposing exactly this precompile, has been open
  since 2018 and still isn't live on mainnet). The proven real-world
  answer to this exact problem is a zk-SNARK wrapping the Ed25519
  verification into something cheap to verify on-chain - this is
  literally how NEAR's Rainbow Bridge solves the identical problem
  (NEAR also signs with Ed25519). That's a genuine, substantial zk-
  circuit engineering project - comparable in scope to what a dedicated
  bridge team spends months on - not something to build blind at the
  tail of a long session. Documented here as the real long-term
  direction, deliberately not started.
- New/updated tests: `api/bridge_validator_posr_eligibility_test.go`
  (a real PoSR validator is eligible with no allowlist entry at all,
  `BRIDGE_VALIDATOR_ADDRESSES` alone no longer grants eligibility, and
  each of the four eligibility criteria - active, not banned, sufficient
  stake, sufficient reputation - is independently enforced, not just
  checked in combination) and `api/vault_bridge_test.go`
  (`TestApplyVaultReleaseApprove_RejectsSignatureBeforeConfirmationDepth`,
  `TestVaultReleaseConfirmationsRemaining_TracksChainHeight` - the
  confirmation-depth gate rejects a signature at 0 blocks and at one
  block short of the requirement, then accepts it once deep enough).

## Retiring wallet recovery silently broke 3 unrelated notification features - fixed with a new, narrowly-scoped contact-email flow (2026-08-21)

**Found via a real user report**: a validator applicant never received the
emailed confirmation link, and there was no way to request it again or
change the email on file. Root cause: when wallet recovery was
discontinued (`api/recovery.go`'s `walletRecoveryDiscontinuedMessage`,
2026-08-16), `HandleRequestEmailCode`/`HandleConfirmEmail` - the ONLY
path that ever set `core.WalletRecovery.EmailVerified=true` - were both
hard-disabled (`410 Gone`) along with the rest of the recovery flow.
Three separate, unrelated features already depended on that exact flag
and silently stopped working for any wallet created after the cutover:
`api/validator_application.go`'s confirm-link email,
`api/p2p_trade_start_notify.go`, `api/p2p_dispute_notify.go` - all three
just found `EmailVerified` false and skipped sending, with no error
surfaced anywhere. Retiring the weak "email + claimed year" RECOVERY
mechanism was the right call and stays retired; the mistake was that the
underlying "verify an email as a notification address" capability was
never something recovery should have owned exclusively in the first
place.

**Fix**: a new, deliberately separate transaction pair
(`core.TxRequestContactEmailCode`/`TxConfirmContactEmail`,
`api/contact_email.go`) rather than reviving `TxSetRecoveryEmail` -
that type's `applySetRecoveryEmail` keeps its hard cutover check, wallet
recovery itself stays correctly dead. Writes into the same
`core.WalletRecovery` record purely as a verified notification address
- explicitly not a recovery credential, and confirmed nothing here
reactivates `HandleRecoverWallet` or any other recovery capability.
`HandleRequestContactEmailCode` doubles as both "resend" and "change the
email" (re-requesting overwrites any still-pending code), closing the
exact gap the user reported. New `website/settings.html` "Contact
Email" card + `settings.js` wiring - previously no frontend existed for
this at all.

13 new tests (`api/contact_email_test.go`): signature-binding for both
new transaction types (payload tampering rejected), state correctness
independent of whether SMTP is actually configured, wrong-code
rejection, and an explicit end-to-end regression test
(`TestContactEmailFlow_EndToEnd_ClosesTheRealReportedGap`) proving the
new flow reaches the exact `EmailVerified=true` state the three starved
consumers already correctly check for. Full `go build`/`go vet`/`go test
./...` + linux/amd64 cross-compile clean. Verified live in-browser with
a real Ed25519-signed request round-tripping to a real local API
instance (correctly rejected with the honest "email is not configured"
error, since local dev has no SMTP set - same as every other
email-dependent feature in this codebase). **Not yet deployed** to node1
or mainnet.

## Marketplace: no USD offers, no US-jurisdiction Trading Hubs, currency dropdown (2026-08-31)

This platform holds no US money-transmitter/MSB license anywhere.
Facilitating USD-denominated peer-to-peer settlement (a verified Trading
Hub's `HubRate.FiatSymbol` in OFFAPP mode - see `core/hub_rate.go`) is
money transmission under US federal and most state law regardless of
whether this codebase ever touches the actual dollars itself, since
OFFAPP settlement always happens off-app between the two traders. A
US-based Trading Hub carries the same exposure on the operator side -
real fines, and possible personal legal consequences for whichever
moderator approved it.

**Fixed - two hard, server-side allowlists, not free text and not a
moderator judgment call:**

- **`core.AllowedOffAppCurrencies`** (`core/fiat_currency.go`) - the
  fixed list of real-world settlement currencies a Trading Hub may
  quote an OFFAPP rate in. USD is not on it, in any form -
  `core.IsUSDCurrency` also catches common aliases ("US$", "$",
  "Dollar", "United States Dollar", ...) so a direct API caller
  bypassing the frontend gets an error naming the actual regulatory
  reason, not a generic "unsupported currency" message. Enforced at
  both submission (`HandleSetHubRate`, `api/hub_rate.go`) and block
  confirmation (`applyHubRateSet`, `api/apply.go`) - never trusted from
  submission time alone, this codebase's usual discipline. `GET
  /hub-rates/currencies` serves the list so the frontend dropdown
  (`website/p2p.html`'s `hubRateFiatSymbol`, now a `<select>`, was a
  free-text `<input>`) can never drift from what will actually be
  accepted. ONCHAIN-mode rates (FiatSymbol is a real resolvable token,
  e.g. quoting CCT against ETH_CCC1) are untouched - this allowlist only
  gates OFFAPP's real-world-currency field.

- **`core.AllowedHubCountries`** (`core/company_country.go`) - the fixed
  list of jurisdictions a Trading Hub applicant may declare
  (`CompanyApplication.Country`, new field). It has no "United States"
  entry at all, by construction - not a blocklist trying to catch every
  way "US"/"USA"/a state name could be typed into free text.
  `core.CompanyStore.Review` (`core/company.go`) refuses to approve ANY
  application whose Country isn't on this list - which covers both an
  unset Country and a pre-this-feature application with none recorded -
  so approving a US-based hub is not possible at all, regardless of
  what a moderator clicks. Checked again, redundantly, at submission
  (`HandleApplyCompany`) and at the moderator's review HTTP call
  (`HandleReviewCompany`) so both the applicant and the moderator get an
  immediate, specific reason instead of a silently-no-op'd transaction.
  `GET /companies/countries` serves the list for the application form's
  new Country dropdown (`website/companies.html`); `moderation.js`'s
  assigned-applications view flags an ineligible jurisdiction and
  disables its own Approve button client-side too, with the fines/legal-
  consequences framing spelled out, on top of the real server-side
  block.

**Explicitly NOT solved by this pass, disclosed rather than silently
assumed:** there is still no real jurisdiction/identity data for an
ordinary trader (as opposed to a Trading Hub applicant) anywhere in this
codebase - `core/company.go`'s own doc comment already flags the whole
`CompanyApplication` flow as a testnet-only placeholder with no real
KYC/AML vendor integration. Blocking "US persons" at the level of
individual anonymous P2P traders would need real identity/geo
infrastructure (IP geolocation is trivially spoofed and would be
security theater; a self-attestation checkbox is the standard mechanism
real OTC/P2P platforms use absent real KYC, but none exists yet for a
plain trader account here) - this pass only closes the two levers that
actually exist and are enforceable today: what currency a Hub may quote,
and what jurisdiction a Hub may declare. `website/p2p-terms.html` and
`companies.html` both disclose the USD/US restriction to users up front
rather than only surfacing it as a rejected-request error.

19 new tests across `core/fiat_currency_test.go`,
`core/company_country_test.go`, `api/hub_rate_currency_test.go`,
`api/company_country_test.go` - allowlist/alias matching, the HTTP
fast-fail and block-confirmation rejections for both USD specifically
and an arbitrary unsupported currency/country, and that approval
genuinely succeeds for an eligible currency/jurisdiction (so this is a
real allowlist, not an accidental block-everything). Existing tests that
happened to use `FiatSymbol: "USD"` as an arbitrary placeholder
(`hub_rate_scale_test.go`, `hub_operator_test.go`,
`hub_directory_filters_test.go`) were repointed to a real allowed
currency (NGN/GBP) - none of them were testing USD-specific behavior.
Full `go build`/`go vet`/`go test ./...` clean, including a
`-shuffle=on` pass; that pass also surfaced (and this entry's commit
fixed) a pre-existing gap where `api/apply_test.go`'s shared
`resetApplyTestState()` never cleared the package-level `mempool` var,
letting one HTTP-handler test's pending transaction leak into an
unrelated later test's assertions under certain orderings - unrelated
to this feature but a genuine test-isolation bug this session's own new
tests happened to expose.
