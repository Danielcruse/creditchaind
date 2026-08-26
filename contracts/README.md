# CreditChainVault - lock-and-mint bridge contract

**Status: compiled, full test suite run and passing (2026-08-02), deployed to Sepolia (older version) and mainnet (current version, with per-depositor caps).**
`forge test` (Foundry 1.7.1, solc 0.8.35): 20/20 tests passed, 0 failed.
Sepolia deployment at `0x467477F749f265b306045bB1e338a843A86a1745` (chain ID
11155111, constructor args `validators=[0xf4e0649e14c3cfd201f9e13d093f7fa27238152d]`,
`threshold=1`) predates the per-depositor cap fields and was deliberately
left as-is rather than redeployed. Live end-to-end on testnet: bridge
validator registered, `/vault/info` and `/vault/release-queue` confirmed
responding correctly against the deployed contract.

**This is still NOT a substitute for a professional third-party audit.**
Passing tests written by the same process that wrote the contract prove
internal consistency with its own stated intent, not the absence of
exploitable bugs an independent reviewer would catch. See
`AUDIT_PACKAGE.md` in this directory for the package prepared for a
real auditor. **Mainnet deployment proceeded without an audit, per
explicit operator instruction, using a per-depositor native-unit deposit
cap as the risk-limiting mechanism in its place** - see
`AUDIT_PACKAGE.md`'s cap section for exactly what that does and does not
protect against.

## Setup

```bash
curl -L https://foundry.paradigm.xyz | bash
foundryup

cd contracts
# foundry.toml already present in this directory (src=".", test="test", libs=["lib"])
git clone --depth 1 https://github.com/foundry-rs/forge-std lib/forge-std   # if lib/forge-std isn't already present
```

## Compile

```bash
forge build
```

## Test

```bash
forge test -vvv
```

Every test in `CreditChainVault.t.sol` must pass before this contract
is trusted with any real value:
- Deposit happy paths (ETH + allowlisted ERC20)
- Per-depositor native-unit cap: enforced, cumulative across multiple
  deposits, independent per `creditChainAddress`, disabled when zero,
  adjustable only via threshold signatures
- `release()` correctly requires threshold DISTINCT valid signatures
  (rejects too few, a duplicate signer's signature counted twice, a
  non-validator signer)
- `usedReleaseIds` replay guard
- Pause blocks new deposits but not already-authorized releases
- `setValidators`/`setTokenAllowed`/`setPerDepositorCaps` self-governance
  (same on-chain signature check as `release()`, no separate admin key)
- Reentrancy is blocked (a malicious ERC20 attempting to re-enter
  `release()` from within its own `transfer()` call)

## Deploy (Sepolia first, always - for any FUTURE contract change; the
## current Sepolia deployment already exists and is deliberately not
## being redeployed for the cap-mechanism change, see status above)

```bash
forge create contracts/CreditChainVault.sol:CreditChainVault \
  --rpc-url $SEPOLIA_RPC_URL \
  --private-key $DEPLOYER_PRIVATE_KEY \
  --constructor-args-path deploy-args.json
```

where `deploy-args.json` is a flat JSON array of strings:
`["[0xValidator1,0xValidator2,0xValidator3]", "2", "500000000000000000", "1000000000"]`
(validators, threshold, per-depositor ETH cap in wei, per-depositor USDC
cap in USDC's own 6-decimal units - `0` disables either cap).

Set the resulting deployed address as `VAULT_CONTRACT_ADDRESS` in the
`creditchaind` environment (see `api/vault_eth_watch.go`) - the
existing `InitEthRPC` chain-ID safety gate (`api/eth_rpc.go`) refuses
to enable anything real-value-related against an unexpected chain, the
same protection this contract inherits by construction.

**Mainnet activation without a professional audit is a deliberate,
explicit operator decision, not this project's default recommendation.**
The plan this contract was originally built under named a professional
audit as a hard precondition before real value - that precondition was
explicitly overridden in favor of the per-depositor cap mechanism above.
Anyone reusing this contract elsewhere should not treat that override as
a general endorsement of skipping an audit.
