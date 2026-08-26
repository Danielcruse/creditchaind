// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

// CreditChainVault - the real, on-chain lock-and-mint side of the
// bridge redesign (see core/vault_lock_attestation.go's Go-side doc
// comment for the full architecture). Real ETH/USDC locked here is
// what backs ETH_CCC1/USDC_CCC1 1:1 - CreditChain's Go backend never
// holds a key that can move funds out of this contract; only a real
// M-of-N threshold of independently-verified bridge-validator
// signatures, checked ON-CHAIN via ecrecover, can ever trigger
// release(). Anyone may relay a valid signature bundle (permissionless
// finalization) - CreditChain's own servers carry zero additional
// trust beyond what this contract itself checks.
//
// Compiled and tested (forge test: 12/12 passing, see
// contracts/README.md) and deployed to Sepolia at
// 0x467477F749f265b306045bB1e338a843A86a1745 - 2026-08-02. Passing
// tests are NOT a substitute for a professional third-party audit;
// see contracts/AUDIT_PACKAGE.md for the hard preconditions before any
// mainnet deployment with real value.
//
// No external imports (OpenZeppelin etc.) - the small, well-established
// ReentrancyGuard/Pausable/ECDSA-recovery patterns below are
// reimplemented inline rather than fetched, for the same reason this
// project's JS crypto (BIP143, RFC6979) is hand-implemented rather than
// vendored: no package-fetching capability in the authoring
// environment. Keeping this file dependency-free also means it compiles
// with nothing beyond a bare solc/forge install.

/// @notice Minimal ERC20 interface - only the calls this vault needs.
interface IERC20 {
    function transferFrom(address from, address to, uint256 amount) external returns (bool);
    function transfer(address to, uint256 amount) external returns (bool);
}

contract CreditChainVault {
    // --- Reentrancy guard (inline, mirrors OpenZeppelin's well-known
    // pattern: a single non-zero/non-zero status flag, cheaper than a
    // bool because SSTORE from nonzero-to-nonzero is cheaper than
    // zero-to-nonzero). ---
    uint256 private constant NOT_ENTERED = 1;
    uint256 private constant ENTERED = 2;
    uint256 private reentrancyStatus = NOT_ENTERED;

    modifier nonReentrant() {
        require(reentrancyStatus != ENTERED, "CreditChainVault: reentrant call");
        reentrancyStatus = ENTERED;
        _;
        reentrancyStatus = NOT_ENTERED;
    }

    // --- Pausable (inline) ---
    bool public paused;

    modifier whenNotPaused() {
        require(!paused, "CreditChainVault: paused");
        _;
    }

    // --- Validator set: the on-chain source of truth for which
    // signatures release() will accept. Mirrors core.BridgeValidator's
    // EthSigningAddress field - this is NOT the same set as
    // CreditChain's internal ADMIN_MULTISIG_ADDRESSES (see
    // core/bridge_validator.go's doc comment for why they're kept
    // separate). Deliberately self-governing: setValidators can only
    // ever be called via the same M-of-N signature check release()
    // itself uses, so no separate upgradeable-admin key exists anywhere
    // in this contract. ---
    address[] public validators;
    uint256 public threshold;

    // usedReleaseIds is the on-chain replay guard - once a given
    // releaseId has been consumed by a successful release(), the exact
    // same signature bundle can never be replayed. releaseId is
    // computed off-chain deterministically from the corresponding
    // TxVaultBurn's own transaction ID (see api/vault_lock.go's
    // vaultReleaseIDFor) - every node/validator independently derives
    // the identical value, so no on-chain counter/nonce state is needed
    // here beyond this used-set.
    mapping(uint256 => bool) public usedReleaseIds;

    // allowedTokens is the on-chain ERC20 allowlist depositERC20() will
    // accept - USDC only, initially. Deliberately narrow (not "any
    // ERC20") so this contract can never be tricked into custody of an
    // arbitrary/malicious token contract.
    mapping(address => bool) public allowedTokens;

    // --- Per-depositor native-unit deposit caps: a mainnet safety
    // mechanism used IN PLACE OF a professional audit, per explicit
    // operator instruction (see contracts/AUDIT_PACKAGE.md) - not a
    // permanent design feature. Deliberately in NATIVE units (wei /
    // USDC's own 6-decimal units), never USD: converting to USD would
    // require a price oracle, and adding one to an already-unaudited
    // contract trades one risk (no cap) for a worse one (oracle
    // manipulation). Tracked as CUMULATIVE LIFETIME deposits per
    // depositor (keyed by creditChainAddress, the same bytes20 already
    // passed into deposit()/depositERC20()) - deliberately NOT decremented
    // on release(), since release() has no way to attribute a withdrawal
    // back to a specific depositor's creditChainAddress (it operates on
    // an arbitrary `to` address, validator-authorized, not depositor-
    // specific). This makes the cap strictly conservative (a depositor
    // who withdraws does not regain headroom) rather than precisely
    // "current balance at risk" - simpler and safer than trying to track
    // a live balance incorrectly. Admin-adjustable via the same M-of-N
    // threshold mechanism as setValidators/setTokenAllowed/setPaused,
    // since a fixed native-unit cap's USD value drifts with market price.
    mapping(bytes20 => uint256) public depositedWeiByAccount;
    mapping(bytes20 => uint256) public depositedUsdcByAccount;
    uint256 public perDepositorCapWei;
    uint256 public perDepositorCapUsdc;

    event Locked(
        address indexed depositor,
        address indexed token, // address(0) for ETH
        uint256 amount,
        bytes20 creditChainAddress,
        bytes32 creditChainPublicKey
    );

    event Released(address indexed to, address indexed token, uint256 amount, uint256 indexed releaseId);
    event ValidatorsUpdated(address[] newValidators, uint256 newThreshold);
    event Paused();
    event Unpaused();
    event TokenAllowlistUpdated(address indexed token, bool allowed);
    event PerDepositorCapsUpdated(uint256 newCapWei, uint256 newCapUsdc);

    constructor(
        address[] memory initialValidators,
        uint256 initialThreshold,
        uint256 initialPerDepositorCapWei,
        uint256 initialPerDepositorCapUsdc
    ) {
        require(initialValidators.length > 0, "CreditChainVault: no validators");
        require(initialThreshold > 0 && initialThreshold <= initialValidators.length, "CreditChainVault: bad threshold");
        _requireNoZeroOrDuplicateAddresses(initialValidators);
        validators = initialValidators;
        threshold = initialThreshold;
        perDepositorCapWei = initialPerDepositorCapWei;
        perDepositorCapUsdc = initialPerDepositorCapUsdc;
    }

    // --- Deposit (lock) side ---

    /// @notice Locks real ETH into the vault. The depositor must supply
    /// their own CreditChain address/public key so the off-chain
    /// bridge-validator watchers (api/vault_eth_watch.go) know which
    /// account to credit - this contract has no concept of CreditChain
    /// accounts itself, it only records the fact and amount of the lock.
    function deposit(bytes20 creditChainAddress, bytes32 creditChainPublicKey) external payable whenNotPaused {
        require(msg.value > 0, "CreditChainVault: zero deposit");
        if (perDepositorCapWei > 0) {
            uint256 newTotal = depositedWeiByAccount[creditChainAddress] + msg.value;
            require(newTotal <= perDepositorCapWei, "CreditChainVault: exceeds per-depositor cap");
            depositedWeiByAccount[creditChainAddress] = newTotal;
        }
        emit Locked(msg.sender, address(0), msg.value, creditChainAddress, creditChainPublicKey);
    }

    /// @notice Locks an allowlisted ERC20 (USDC) into the vault via a
    /// standard approve+transferFrom - deliberately not EIP-2612 permit,
    /// since real deployed USDC doesn't uniformly support permit across
    /// chains/versions (see plan's Phase B design note).
    function depositERC20(address token, uint256 amount, bytes20 creditChainAddress, bytes32 creditChainPublicKey)
        external
        whenNotPaused
        nonReentrant
    {
        require(allowedTokens[token], "CreditChainVault: token not allowed");
        require(amount > 0, "CreditChainVault: zero deposit");
        if (perDepositorCapUsdc > 0) {
            uint256 newTotal = depositedUsdcByAccount[creditChainAddress] + amount;
            require(newTotal <= perDepositorCapUsdc, "CreditChainVault: exceeds per-depositor cap");
            depositedUsdcByAccount[creditChainAddress] = newTotal;
        }
        require(IERC20(token).transferFrom(msg.sender, address(this), amount), "CreditChainVault: transferFrom failed");
        emit Locked(msg.sender, token, amount, creditChainAddress, creditChainPublicKey);
    }

    // --- Release (withdraw) side ---

    /// @notice Releases real ETH or an allowlisted ERC20 back to `to`,
    /// once `signatures` contains at least `threshold` valid, DISTINCT
    /// bridge-validator signatures over the canonical digest below.
    /// Callable by anyone (permissionless finalization) - the caller
    /// need not be `to` or a validator themselves, they're just relaying
    /// an already-authorized action; this contract's own on-chain check
    /// is the entire authorization boundary.
    function release(
        address payable to,
        address token, // address(0) for ETH
        uint256 amount,
        uint256 releaseId,
        bytes[] calldata signatures
    ) external nonReentrant {
        require(!usedReleaseIds[releaseId], "CreditChainVault: releaseId already used");
        require(signatures.length >= threshold, "CreditChainVault: insufficient signatures");

        bytes32 digest = keccak256(abi.encode(to, token, amount, releaseId, address(this), block.chainid));

        // Track which validator indices have already been counted, so
        // the SAME validator submitting two signatures (or two
        // signatures that happen to recover to the same address) can
        // never be double-counted toward threshold.
        bool[] memory seen = new bool[](validators.length);
        uint256 distinctValid = 0;

        for (uint256 i = 0; i < signatures.length; i++) {
            address signer = _recoverSigner(digest, signatures[i]);
            for (uint256 v = 0; v < validators.length; v++) {
                if (!seen[v] && validators[v] == signer) {
                    seen[v] = true;
                    distinctValid++;
                    break;
                }
            }
        }

        require(distinctValid >= threshold, "CreditChainVault: insufficient distinct valid signatures");

        usedReleaseIds[releaseId] = true;

        if (token == address(0)) {
            (bool ok, ) = to.call{value: amount}("");
            require(ok, "CreditChainVault: ETH transfer failed");
        } else {
            require(IERC20(token).transfer(to, amount), "CreditChainVault: token transfer failed");
        }

        emit Released(to, token, amount, releaseId);
    }

    // --- Self-governance: every state-changing admin action below
    // requires the SAME on-chain M-of-N signature check as release(),
    // over a digest scoped to that specific action (so a signature
    // authorizing one action can never be replayed as authorization for
    // a different one). No separate admin/owner key exists anywhere in
    // this contract. ---

    function setValidators(address[] calldata newValidators, uint256 newThreshold, uint256 actionNonce, bytes[] calldata signatures)
        external
    {
        require(newValidators.length > 0, "CreditChainVault: no validators");
        require(newThreshold > 0 && newThreshold <= newValidators.length, "CreditChainVault: bad threshold");
        require(!usedReleaseIds[actionNonce], "CreditChainVault: nonce already used");
        _requireNoZeroOrDuplicateAddresses(newValidators);

        bytes32 digest = keccak256(abi.encode("SET_VALIDATORS", newValidators, newThreshold, actionNonce, address(this), block.chainid));
        _requireThresholdSignatures(digest, signatures);

        usedReleaseIds[actionNonce] = true;
        validators = newValidators;
        threshold = newThreshold;
        emit ValidatorsUpdated(newValidators, newThreshold);
    }

    function setTokenAllowed(address token, bool allowed, uint256 actionNonce, bytes[] calldata signatures) external {
        require(!usedReleaseIds[actionNonce], "CreditChainVault: nonce already used");

        bytes32 digest = keccak256(abi.encode("SET_TOKEN_ALLOWED", token, allowed, actionNonce, address(this), block.chainid));
        _requireThresholdSignatures(digest, signatures);

        usedReleaseIds[actionNonce] = true;
        allowedTokens[token] = allowed;
        emit TokenAllowlistUpdated(token, allowed);
    }

    function setPaused(bool newPaused, uint256 actionNonce, bytes[] calldata signatures) external {
        require(!usedReleaseIds[actionNonce], "CreditChainVault: nonce already used");

        bytes32 digest = keccak256(abi.encode("SET_PAUSED", newPaused, actionNonce, address(this), block.chainid));
        _requireThresholdSignatures(digest, signatures);

        usedReleaseIds[actionNonce] = true;
        paused = newPaused;
        if (newPaused) emit Paused();
        else emit Unpaused();
    }

    /// @notice Adjusts the per-depositor native-unit caps (see the
    /// mapping declarations above) - the USD value of a fixed native-unit
    /// figure drifts with market price, so this needs to be adjustable
    /// without a redeploy, same threshold-signed pattern as every other
    /// self-governance action here.
    function setPerDepositorCaps(uint256 newCapWei, uint256 newCapUsdc, uint256 actionNonce, bytes[] calldata signatures)
        external
    {
        require(!usedReleaseIds[actionNonce], "CreditChainVault: nonce already used");

        bytes32 digest = keccak256(abi.encode("SET_PER_DEPOSITOR_CAPS", newCapWei, newCapUsdc, actionNonce, address(this), block.chainid));
        _requireThresholdSignatures(digest, signatures);

        usedReleaseIds[actionNonce] = true;
        perDepositorCapWei = newCapWei;
        perDepositorCapUsdc = newCapUsdc;
        emit PerDepositorCapsUpdated(newCapWei, newCapUsdc);
    }

    // --- Internal helpers ---

    /// @dev Rejects a validator set containing the zero address or any
    /// duplicate entry - called from the ONLY two places `validators` is
    /// ever assigned (the constructor and setValidators), so `validators`
    /// can never hold a duplicate from this point forward. This is what
    /// actually closes the distinct-signer gap: release()'s and
    /// _requireThresholdSignatures's dedup logic below both track SEEN
    /// VALIDATOR ARRAY INDICES, not seen signer addresses - if the same
    /// address occupied two indices, one real signer submitting their own
    /// signature twice would count as two distinct approvals toward
    /// threshold (e.g. validators=[A,A,B], threshold=2 - A alone reaches
    /// distinctValid=2 by submitting their own signature bytes twice, no
    /// cooperation from B needed). An O(n^2) nested-loop check is
    /// intentionally simple and gas-cheap enough for this contract's
    /// deliberately small validator set (see the constructor's own doc
    /// comment on why a small, explicit set is the design, not a
    /// limitation to work around here).
    function _requireNoZeroOrDuplicateAddresses(address[] memory addrs) internal pure {
        for (uint256 i = 0; i < addrs.length; i++) {
            require(addrs[i] != address(0), "CreditChainVault: zero address validator");
            for (uint256 j = i + 1; j < addrs.length; j++) {
                require(addrs[i] != addrs[j], "CreditChainVault: duplicate validator");
            }
        }
    }

    /// @dev Shared threshold-check used by every self-governance action
    /// above - same distinct-signer-counting logic release() uses,
    /// factored out since only the digest differs per action.
    function _requireThresholdSignatures(bytes32 digest, bytes[] calldata signatures) internal view {
        require(signatures.length >= threshold, "CreditChainVault: insufficient signatures");

        bool[] memory seen = new bool[](validators.length);
        uint256 distinctValid = 0;

        for (uint256 i = 0; i < signatures.length; i++) {
            address signer = _recoverSigner(digest, signatures[i]);
            for (uint256 v = 0; v < validators.length; v++) {
                if (!seen[v] && validators[v] == signer) {
                    seen[v] = true;
                    distinctValid++;
                    break;
                }
            }
        }

        require(distinctValid >= threshold, "CreditChainVault: insufficient distinct valid signatures");
    }

    /// @dev secp256k1 order, used for the low-S malleability check below
    /// (EIP-2 - only the lower half of possible s values is considered
    /// canonical, same rule this project's own client-side ETH signing
    /// already enforces, website/js/vendor/eth-tx.js).
    uint256 private constant SECP256K1_N = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141;
    uint256 private constant SECP256K1_N_HALF = SECP256K1_N / 2;

    /// @dev Recovers the signer of `digest` from a standard 65-byte
    /// [R || S || V] signature (V is 27 or 28, matching this project's
    /// own client-side ETH signing convention - EthTx.signLegacyTx). No
    /// EIP-191 "\x19Ethereum Signed Message" prefix is applied - the
    /// digest passed in is already this contract's own canonical,
    /// unambiguous release/action digest, not a general-purpose message.
    function _recoverSigner(bytes32 digest, bytes calldata signature) internal pure returns (address) {
        require(signature.length == 65, "CreditChainVault: bad signature length");

        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := calldataload(signature.offset)
            s := calldataload(add(signature.offset, 32))
            v := byte(0, calldataload(add(signature.offset, 64)))
        }
        if (v < 27) v += 27;

        require(uint256(s) <= SECP256K1_N_HALF, "CreditChainVault: signature s value invalid (malleable)");
        require(v == 27 || v == 28, "CreditChainVault: bad signature v value");

        address signer = ecrecover(digest, v, r, s);
        require(signer != address(0), "CreditChainVault: invalid signature");
        return signer;
    }

    /// @notice Lets anyone view the current validator set for off-chain
    /// tooling (the P2WSH redeem-script builder / TxVaultLockAttest
    /// submitters don't need this - they read core.BridgeValidatorStore
    /// - but external integrators calling this contract directly do).
    function getValidators() external view returns (address[] memory) {
        return validators;
    }

    // Accepting plain ETH transfers outside deposit() would credit no
    // CreditChain account and strand the funds - deliberately rejected.
    receive() external payable {
        revert("CreditChainVault: use deposit()");
    }
}
