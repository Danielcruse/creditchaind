# Vault release relayer: AWS KMS setup (optional)

This is the operator-facing setup guide for hardening the vault release
relayer's signing key (`api/vault_relayer_signer.go`) with AWS KMS instead
of the original `RELAYER_PRIVATE_KEY_HEX` raw-hex env var. Read
`audit/KNOWN_GAPS.md`'s key-custody entries first - this document is setup
steps, not the security rationale.

**Entirely optional, and lower-priority than it might sound.** The relayer
key's own doc comment (`api/vault_release_relay.go`) explains why: it only
ever pays gas for `release()` calls - the contract's own on-chain
`ecrecover` verification means the relayer's identity carries NO authority
over whether a release is valid. A leaked relayer key can at worst waste
its own gas or race another relayer to submit first (harmless - the
contract's `usedReleaseIds` makes a second submission a no-op revert). This
is why relaying is deliberately permissionless in the first place - any
account with ETH for gas can submit a valid signature bundle. KMS here is
real hardening (protects the relayer's own ETH balance from being drained
by a leaked key), not a fix for an otherwise-unbounded risk.

Same "one fixed key, not per-user provisioning" shape as
`ops/VAULT_GAS_PRICE_ORACLE_KMS_SETUP.md` - you create the KMS key once,
up front, yourself.

## 1. Create the KMS key

```bash
aws kms create-key \
  --key-spec ECC_SECG_P256K1 \
  --key-usage SIGN_VERIFY \
  --description "CreditChain vault release relayer signing key"
```

Note the returned `KeyId` (or create an alias with `aws kms create-alias`).

## 2. Fund the key's address with real ETH

Unlike the gas-price-oracle key (which never spends anything), the relayer
key's whole job is paying gas - it needs a real ETH balance.

```bash
aws kms get-public-key --key-id <your-key-id> --query PublicKey --output text | base64 -d > relayer-pubkey.der
```

Derive the Ethereum address from `relayer-pubkey.der` (SHA3-256/Keccak
of the uncompressed public key, last 20 bytes) - or simpler: start the
node with this KMS key configured (step 4 below) and read the address
straight from the "this node will pay gas from `<address>`" log line
`startVaultReleaseRelayLoop` already prints on startup, then send it ETH.

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

Scoped to exactly this one key ARN and exactly the two operations
`kmsRelayerSigner` actually calls (`api/vault_relayer_signer.go`) - no
`CreateKey` needed here since step 1 is a one-time manual action.

## 4. Configure the node

```
Environment=RELAYER_KMS_ENABLED=true
Environment=RELAYER_KMS_KEY_ID=<your-key-id-or-alias>
Environment=AWS_ACCESS_KEY_ID=...
Environment=AWS_SECRET_ACCESS_KEY=...
Environment=AWS_DEFAULT_REGION=eu-west-1
```

Leave `RELAYER_PRIVATE_KEY_HEX` unset (or remove it) once KMS is confirmed
working - it only serves as a fallback if KMS setup is incomplete (see
`resolveRelayerSigner`'s doc comment). `RELAYER_FEE_RECIPIENT_ETH_ADDRESS`
(a separate, unrelated env var - where the relayer's *earned fee* lands,
not where it pays gas from) is unaffected by this change either way.

Restart the service. You should see the usual `vault ETH/USDC release
relay: this node will pay gas from <address> - ...` log line - if
resolution silently fails (bad AWS config, wrong key ID, key isn't
actually `ECC_SECG_P256K1`), the relay loop simply never starts; check the
log line just above for why.

## 5. This same pattern now covers every call site

Unlike the gas-price-oracle key (2 call sites), the relayer key had 6
across 4 files (`vault_release_relay.go`, `vault_set_token_allowed_relay.go`
x2, `vault_set_validators_relay.go`, `vault_admin_sweep.go`) - all of them
now resolve through `resolveRelayerSigner()` uniformly, so enabling KMS
here covers the release relay loop AND the on-demand admin-triggered
relay endpoints (`setTokenAllowed`/`setPerDepositorCaps`/`setValidators`
relaying, and the release-signing-key ETH sweep tool) in one step - no
separate configuration needed per call site.
