# Vault gas price oracle: AWS KMS setup (optional)

This is the operator-facing setup guide for hardening the vault gas price
oracle's signing key (`api/vault_gas_price_oracle_signer.go`) with AWS KMS
instead of the original `VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX` raw-hex
env var. Read `audit/KNOWN_GAPS.md`'s key-custody entries first - this
document is setup steps, not the security rationale.

**Entirely optional.** The gas price oracle loop already has a bounded
blast radius by design even with the key held as a plain hex env var - a
leaked key can only move `VaultReleaseGasPriceWei` by
`VaultGasPriceOracleMaxStepBasisPoints` (50%) at most once every
`VaultGasPriceOracleMinIntervalSeconds` (15 minutes), and only within
`[VaultGasPriceOracleMinWei, VaultGasPriceOracleMaxWei]` - see
`api/vault_gas_price_oracle.go`. KMS is real hardening, not a fix for an
otherwise-unbounded risk.

**Unlike the ETH custodial signer** (`ops/ETH_CUSTODIAL_SETUP.md`), which
provisions a brand-new KMS key per custodial account on demand, the gas
price oracle has exactly ONE fixed key - you create it once, up front,
yourself.

## 1. Create the KMS key

```bash
aws kms create-key \
  --key-spec ECC_NIST_EDWARDS25519 \
  --key-usage SIGN_VERIFY \
  --description "CreditChain vault gas price oracle signing key"
```

Note the returned `KeyId` (or create an alias with `aws kms create-alias`
for a friendlier reference - either works as `VAULT_GAS_PRICE_ORACLE_KMS_KEY_ID`
below).

## 2. Derive and register the CreditChain address this key signs as

The oracle mechanism only accepts updates from ONE specific CreditChain
address (`VAULT_GAS_PRICE_ORACLE_ADDRESS`) - same design as the relayer's
single fixed key. Fetch the public key and derive the address:

```bash
aws kms get-public-key --key-id <your-key-id> --query PublicKey --output text | base64 -d > pubkey.der
```

Feed `pubkey.der` through `core.GenerateAddress` (SHA-256 of the raw
32-byte Ed25519 public key, first 20 bytes, hex-encoded) - the small
`cmd/vaultgasoraclekeygen` helper in this repo does this for a raw hex key
today; a KMS-aware variant (parse the DER, then the same hash) is a small
addition if you don't want to do this by hand in a Go/Python one-off.

## 3. IAM policy

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["kms:GetPublicKey", "kms:Sign"],
      "Resource": "arn:aws:kms:REGION:ACCOUNT_ID:key/YOUR_KEY_ID"
    }
  ]
}
```

Scoped to exactly this one key ARN and exactly the two operations the
signer actually calls (`kmsEd25519OracleSigner.identity`/`.sign` in
`api/vault_gas_price_oracle_signer.go`) - no `CreateKey` needed here since
step 1 is a one-time manual setup action, not something the running node
ever does itself.

## 4. Configure the node

```
Environment=VAULT_GAS_PRICE_ORACLE_KMS_ENABLED=true
Environment=VAULT_GAS_PRICE_ORACLE_KMS_KEY_ID=<your-key-id-or-alias>
Environment=VAULT_GAS_PRICE_ORACLE_ADDRESS=cc<address derived in step 2>
Environment=AWS_ACCESS_KEY_ID=...
Environment=AWS_SECRET_ACCESS_KEY=...
Environment=AWS_DEFAULT_REGION=eu-west-1
```

Leave `VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX` unset (or remove it) - once
`VAULT_GAS_PRICE_ORACLE_KMS_ENABLED=true` and a valid key ID resolve, KMS
takes priority; the raw-hex path only serves as a fallback if KMS setup is
incomplete (see `resolveVaultGasPriceOracleSigner`'s doc comment).

Restart the service. You should see the usual `vault gas price oracle
loop started - signing as <address> - ...` log line - if resolution
silently fails (bad AWS config, wrong key ID, key isn't actually
`ECC_NIST_EDWARDS25519`), the loop simply never starts; check the log
line just above for why, same graceful-degrade posture every other
optional loop in this codebase has.

## 5. Bridge validator key and relayer key are NOT covered by this doc

`BRIDGE_VALIDATOR_PRIVATE_KEY_HEX` is deliberately each bridge validator's
own EXTERNAL signing infrastructure (Phase A design) - hardening this
server's own copy wouldn't be the right fix, since the whole point of the
M-of-N validator set is that no single operator's key-custody choice is
fatal to the system.

`RELAYER_PRIVATE_KEY_HEX` (vault_release_relay.go) is real, valuable
follow-up work using this exact same pattern (it's already secp256k1, so
it can reuse `api/eth_signer.go`'s existing `EthSigner`/KMS machinery
directly rather than needing a new Ed25519 signer) - not done in this
pass because it has 6 call sites across 4 files
(`vault_release_relay.go`, `vault_set_token_allowed_relay.go`,
`vault_set_validators_relay.go`, `vault_admin_sweep.go`), a larger and
riskier refactor of live fund-relay code than this document's single-key,
2-call-site change.
