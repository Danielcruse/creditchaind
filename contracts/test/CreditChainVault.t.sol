// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

// Run and passing: `forge test` (Foundry 1.7.1, solc 0.8.35) - 12/12
// tests passed, 2026-08-02. See contracts/README.md and
// contracts/AUDIT_PACKAGE.md. This still does not substitute for a
// professional third-party audit before real value.

import "forge-std/Test.sol";
import "../CreditChainVault.sol";
import "./MockERC20.sol";

contract CreditChainVaultTest is Test {
    CreditChainVault vault;
    MockERC20 token;

    uint256 constant VAL_A_KEY = 0xA11CE;
    uint256 constant VAL_B_KEY = 0xB0B;
    uint256 constant VAL_C_KEY = 0xCAFE;
    address valA;
    address valB;
    address valC;

    address depositor = address(0xD0D0);
    address recipient = address(0xBEEF);

    function setUp() public {
        valA = vm.addr(VAL_A_KEY);
        valB = vm.addr(VAL_B_KEY);
        valC = vm.addr(VAL_C_KEY);

        address[] memory validators = new address[](3);
        validators[0] = valA;
        validators[1] = valB;
        validators[2] = valC;

        vault = new CreditChainVault(validators, 2, 0, 0); // 2-of-3, caps disabled by default (tests that need a cap set it explicitly)
        token = new MockERC20();

        vm.deal(depositor, 10 ether);
        token.mint(depositor, 1_000_000e6);
    }

    // --- Deposit happy paths ---

    function test_Deposit_LocksEthAndEmitsLocked() public {
        vm.prank(depositor);
        vm.expectEmit(true, true, false, true);
        emit CreditChainVault.Locked(depositor, address(0), 1 ether, bytes20(uint160(1)), bytes32(uint256(2)));
        vault.deposit{value: 1 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        assertEq(address(vault).balance, 1 ether, "vault should hold the locked ETH");
    }

    function test_Deposit_RevertsOnZeroValue() public {
        vm.prank(depositor);
        vm.expectRevert(bytes("CreditChainVault: zero deposit"));
        vault.deposit{value: 0}(bytes20(uint160(1)), bytes32(uint256(2)));
    }

    function test_DepositERC20_RequiresAllowlistedToken() public {
        vm.startPrank(depositor);
        token.approve(address(vault), 100e6);
        vm.expectRevert(bytes("CreditChainVault: token not allowed"));
        vault.depositERC20(address(token), 100e6, bytes20(uint160(1)), bytes32(uint256(2)));
        vm.stopPrank();
    }

    function test_DepositERC20_HappyPathOnceAllowlisted() public {
        _allowToken(address(token));

        vm.startPrank(depositor);
        token.approve(address(vault), 100e6);
        vault.depositERC20(address(token), 100e6, bytes20(uint160(1)), bytes32(uint256(2)));
        vm.stopPrank();

        assertEq(token.balanceOf(address(vault)), 100e6, "vault should hold the locked USDC");
        assertEq(token.balanceOf(depositor), 1_000_000e6 - 100e6);
    }

    // --- Per-depositor native-unit caps ---

    function test_Deposit_AllowsExactlyAtCap() public {
        _setCaps(1 ether, 0);

        vm.prank(depositor);
        vault.deposit{value: 1 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        assertEq(vault.depositedWeiByAccount(bytes20(uint160(1))), 1 ether);
    }

    function test_Deposit_RevertsWhenExceedingPerDepositorCap() public {
        _setCaps(1 ether, 0);

        vm.prank(depositor);
        vm.expectRevert(bytes("CreditChainVault: exceeds per-depositor cap"));
        vault.deposit{value: 1 ether + 1}(bytes20(uint160(1)), bytes32(uint256(2)));
    }

    function test_Deposit_CapIsCumulativeAcrossMultipleDeposits() public {
        _setCaps(1 ether, 0);

        vm.startPrank(depositor);
        vault.deposit{value: 0.6 ether}(bytes20(uint160(1)), bytes32(uint256(2)));
        // Second deposit alone is under the cap, but combined with the
        // first exceeds it - must still revert (cap tracks cumulative
        // lifetime deposits, not just the single largest transaction).
        vm.expectRevert(bytes("CreditChainVault: exceeds per-depositor cap"));
        vault.deposit{value: 0.5 ether}(bytes20(uint160(1)), bytes32(uint256(2)));
        vm.stopPrank();
    }

    function test_Deposit_DifferentDepositorsHaveIndependentCapHeadroom() public {
        _setCaps(1 ether, 0);

        vm.prank(depositor);
        vault.deposit{value: 1 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        // A different creditChainAddress (bytes20(2), not bytes20(1)) has
        // its own, untouched headroom even though the same EOA sends it.
        vm.prank(depositor);
        vault.deposit{value: 1 ether}(bytes20(uint160(2)), bytes32(uint256(2)));

        assertEq(vault.depositedWeiByAccount(bytes20(uint160(1))), 1 ether);
        assertEq(vault.depositedWeiByAccount(bytes20(uint160(2))), 1 ether);
    }

    function test_DepositERC20_RevertsWhenExceedingPerDepositorCap() public {
        _allowToken(address(token));
        _setCaps(0, 1000e6);

        vm.startPrank(depositor);
        token.approve(address(vault), 1001e6);
        vm.expectRevert(bytes("CreditChainVault: exceeds per-depositor cap"));
        vault.depositERC20(address(token), 1001e6, bytes20(uint160(1)), bytes32(uint256(2)));
        vm.stopPrank();
    }

    function test_Deposit_CapDisabledWhenZero() public {
        // Default (from setUp) is 0/0 - explicitly confirms "0 = uncapped"
        // still allows a large deposit through, matching this codebase's
        // existing convention elsewhere (e.g. account_holdings_cap.go).
        vm.prank(depositor);
        vault.deposit{value: 5 ether}(bytes20(uint160(1)), bytes32(uint256(2)));
        assertEq(address(vault).balance, 5 ether);
    }

    function test_SetPerDepositorCaps_RequiresThresholdSignatures() public {
        uint256 nonce = 400;
        bytes32 digest = keccak256(abi.encode("SET_PER_DEPOSITOR_CAPS", uint256(2 ether), uint256(2000e6), nonce, address(vault), block.chainid));
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);

        vault.setPerDepositorCaps(2 ether, 2000e6, nonce, sigs);

        assertEq(vault.perDepositorCapWei(), 2 ether);
        assertEq(vault.perDepositorCapUsdc(), 2000e6);
    }

    function test_SetPerDepositorCaps_RevertsWithInsufficientSignatures() public {
        uint256 nonce = 401;
        bytes32 digest = keccak256(abi.encode("SET_PER_DEPOSITOR_CAPS", uint256(2 ether), uint256(2000e6), nonce, address(vault), block.chainid));
        bytes[] memory sigs = new bytes[](1);
        sigs[0] = _sign(VAL_A_KEY, digest);

        vm.expectRevert(bytes("CreditChainVault: insufficient signatures"));
        vault.setPerDepositorCaps(2 ether, 2000e6, nonce, sigs);
    }

    function _setCaps(uint256 capWei, uint256 capUsdc) internal {
        uint256 nonce = uint256(keccak256(abi.encode("cap-nonce", capWei, capUsdc, block.timestamp, gasleft())));
        bytes32 digest = keccak256(abi.encode("SET_PER_DEPOSITOR_CAPS", capWei, capUsdc, nonce, address(vault), block.chainid));
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);
        vault.setPerDepositorCaps(capWei, capUsdc, nonce, sigs);
    }

    // --- Release: signature verification ---

    function test_Release_HappyPathWithTwoOfThreeSignatures() public {
        vm.prank(depositor);
        vault.deposit{value: 5 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        uint256 releaseId = 42;
        bytes32 digest = keccak256(abi.encode(recipient, address(0), 2 ether, releaseId, address(vault), block.chainid));

        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);

        uint256 balBefore = recipient.balance;
        vault.release(payable(recipient), address(0), 2 ether, releaseId, sigs);

        assertEq(recipient.balance, balBefore + 2 ether, "recipient should receive the released ETH");
        assertTrue(vault.usedReleaseIds(releaseId));
    }

    function test_Release_RevertsWithInsufficientSignatureCount() public {
        vm.prank(depositor);
        vault.deposit{value: 5 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        uint256 releaseId = 43;
        bytes32 digest = keccak256(abi.encode(recipient, address(0), 1 ether, releaseId, address(vault), block.chainid));

        bytes[] memory sigs = new bytes[](1);
        sigs[0] = _sign(VAL_A_KEY, digest);

        vm.expectRevert(bytes("CreditChainVault: insufficient signatures"));
        vault.release(payable(recipient), address(0), 1 ether, releaseId, sigs);
    }

    function test_Release_RevertsOnDuplicateSignerNotCountedTwice() public {
        vm.prank(depositor);
        vault.deposit{value: 5 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        uint256 releaseId = 44;
        bytes32 digest = keccak256(abi.encode(recipient, address(0), 1 ether, releaseId, address(vault), block.chainid));

        // Same validator's signature submitted twice must not satisfy a
        // 2-of-3 threshold on its own.
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_A_KEY, digest);

        vm.expectRevert(bytes("CreditChainVault: insufficient distinct valid signatures"));
        vault.release(payable(recipient), address(0), 1 ether, releaseId, sigs);
    }

    function test_Release_RevertsOnWrongSigner() public {
        vm.prank(depositor);
        vault.deposit{value: 5 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        uint256 releaseId = 45;
        bytes32 digest = keccak256(abi.encode(recipient, address(0), 1 ether, releaseId, address(vault), block.chainid));

        uint256 notAValidatorKey = 0xDEAD;
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(notAValidatorKey, digest);

        vm.expectRevert(bytes("CreditChainVault: insufficient distinct valid signatures"));
        vault.release(payable(recipient), address(0), 1 ether, releaseId, sigs);
    }

    function test_Release_RevertsOnReplayedReleaseId() public {
        vm.prank(depositor);
        vault.deposit{value: 5 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        uint256 releaseId = 46;
        bytes32 digest = keccak256(abi.encode(recipient, address(0), 1 ether, releaseId, address(vault), block.chainid));

        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);

        vault.release(payable(recipient), address(0), 1 ether, releaseId, sigs);

        vm.expectRevert(bytes("CreditChainVault: releaseId already used"));
        vault.release(payable(recipient), address(0), 1 ether, releaseId, sigs);
    }

    // --- Pause gating ---

    function test_Pause_BlocksNewDepositsNotRelease() public {
        vm.prank(depositor);
        vault.deposit{value: 5 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        uint256 pauseNonce = 100;
        bytes32 pauseDigest = keccak256(abi.encode("SET_PAUSED", true, pauseNonce, address(vault), block.chainid));
        bytes[] memory pauseSigs = new bytes[](2);
        pauseSigs[0] = _sign(VAL_A_KEY, pauseDigest);
        pauseSigs[1] = _sign(VAL_B_KEY, pauseDigest);
        vault.setPaused(true, pauseNonce, pauseSigs);
        assertTrue(vault.paused());

        vm.prank(depositor);
        vm.expectRevert(bytes("CreditChainVault: paused"));
        vault.deposit{value: 1 ether}(bytes20(uint160(1)), bytes32(uint256(2)));

        // release() is deliberately NOT gated by whenNotPaused - a
        // paused vault must still let already-locked funds be withdrawn
        // via a genuine threshold-signed release, only NEW deposits stop.
        uint256 releaseId = 101;
        bytes32 releaseDigest = keccak256(abi.encode(recipient, address(0), 1 ether, releaseId, address(vault), block.chainid));
        bytes[] memory releaseSigs = new bytes[](2);
        releaseSigs[0] = _sign(VAL_A_KEY, releaseDigest);
        releaseSigs[1] = _sign(VAL_B_KEY, releaseDigest);
        vault.release(payable(recipient), address(0), 1 ether, releaseId, releaseSigs);
    }

    // --- setValidators self-governance ---

    function test_SetValidators_RequiresThresholdSignatures() public {
        address[] memory newValidators = new address[](2);
        newValidators[0] = valA;
        newValidators[1] = valC;

        uint256 nonce = 200;
        bytes32 digest = keccak256(abi.encode("SET_VALIDATORS", newValidators, uint256(2), nonce, address(vault), block.chainid));
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);

        vault.setValidators(newValidators, 2, nonce, sigs);

        address[] memory got = vault.getValidators();
        assertEq(got.length, 2);
        assertEq(got[0], valA);
        assertEq(got[1], valC);
    }

    // --- Duplicate/zero-address validators (regression for the audit
    // finding that release()'s and _requireThresholdSignatures's
    // dedup logic tracks SEEN ARRAY INDICES, not seen signer addresses -
    // a validator set containing the same address twice would let that
    // one signer alone reach threshold by submitting their own
    // signature twice, no cooperation from any other validator needed.
    // The fix rejects the malformed set at the only two places
    // `validators` is ever assigned, so it can never exist on-chain in
    // the first place. ---

    function test_Constructor_RevertsOnDuplicateValidator() public {
        address[] memory dup = new address[](3);
        dup[0] = valA;
        dup[1] = valA;
        dup[2] = valB;

        vm.expectRevert(bytes("CreditChainVault: duplicate validator"));
        new CreditChainVault(dup, 2, 0, 0);
    }

    function test_Constructor_RevertsOnZeroAddressValidator() public {
        address[] memory withZero = new address[](2);
        withZero[0] = valA;
        withZero[1] = address(0);

        vm.expectRevert(bytes("CreditChainVault: zero address validator"));
        new CreditChainVault(withZero, 1, 0, 0);
    }

    function test_SetValidators_RevertsOnDuplicateValidator() public {
        address[] memory dup = new address[](3);
        dup[0] = valA;
        dup[1] = valB;
        dup[2] = valA;

        uint256 nonce = 201;
        bytes32 digest = keccak256(abi.encode("SET_VALIDATORS", dup, uint256(2), nonce, address(vault), block.chainid));
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);

        vm.expectRevert(bytes("CreditChainVault: duplicate validator"));
        vault.setValidators(dup, 2, nonce, sigs);
    }

    function test_SetValidators_RevertsOnZeroAddressValidator() public {
        address[] memory withZero = new address[](2);
        withZero[0] = valA;
        withZero[1] = address(0);

        uint256 nonce = 202;
        bytes32 digest = keccak256(abi.encode("SET_VALIDATORS", withZero, uint256(2), nonce, address(vault), block.chainid));
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);

        vm.expectRevert(bytes("CreditChainVault: zero address validator"));
        vault.setValidators(withZero, 2, nonce, sigs);
    }

    // --- Reentrancy: a malicious ERC20 that reenters release() during
    // its transfer() call must be blocked by nonReentrant. ---

    function test_Release_BlocksReentrancy() public {
        ReentrantERC20 evilToken = new ReentrantERC20(vault);
        vm.prank(address(this));
        // Allowlist the evil token via a genuine threshold signature so
        // this test exercises the real path, not a shortcut.
        uint256 nonce = 300;
        bytes32 digest = keccak256(abi.encode("SET_TOKEN_ALLOWED", address(evilToken), true, nonce, address(vault), block.chainid));
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);
        vault.setTokenAllowed(address(evilToken), true, nonce, sigs);

        evilToken.mint(depositor, 1000);
        vm.startPrank(depositor);
        evilToken.approve(address(vault), 1000);
        vault.depositERC20(address(evilToken), 1000, bytes20(uint160(1)), bytes32(uint256(2)));
        vm.stopPrank();

        uint256 releaseId = 301;
        bytes32 releaseDigest = keccak256(abi.encode(recipient, address(evilToken), uint256(500), releaseId, address(vault), block.chainid));
        bytes[] memory releaseSigs = new bytes[](2);
        releaseSigs[0] = _sign(VAL_A_KEY, releaseDigest);
        releaseSigs[1] = _sign(VAL_B_KEY, releaseDigest);

        evilToken.armReentry(recipient, releaseId);
        vm.expectRevert(bytes("CreditChainVault: token transfer failed"));
        vault.release(payable(recipient), address(evilToken), 500, releaseId, releaseSigs);
    }

    // --- Helpers ---

    function _sign(uint256 privateKey, bytes32 digest) internal pure returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);
        return abi.encodePacked(r, s, v);
    }

    function _allowToken(address tokenAddr) internal {
        uint256 nonce = 999;
        bytes32 digest = keccak256(abi.encode("SET_TOKEN_ALLOWED", tokenAddr, true, nonce, address(vault), block.chainid));
        bytes[] memory sigs = new bytes[](2);
        sigs[0] = _sign(VAL_A_KEY, digest);
        sigs[1] = _sign(VAL_B_KEY, digest);
        vault.setTokenAllowed(tokenAddr, true, nonce, sigs);
    }
}

// ReentrantERC20 - a malicious token whose transfer() tries to
// re-enter CreditChainVault.release() mid-call, to prove nonReentrant
// actually blocks it (test_Release_BlocksReentrancy above).
contract ReentrantERC20 {
    CreditChainVault immutable vaultRef;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;
    bool armed;
    address reentryTo;
    uint256 reentryReleaseId;

    constructor(CreditChainVault _vault) {
        vaultRef = _vault;
    }

    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        return true;
    }

    function armReentry(address to, uint256 releaseId) external {
        armed = true;
        reentryTo = to;
        reentryReleaseId = releaseId;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        balanceOf[from] -= amount;
        balanceOf[to] += amount;
        return true;
    }

    function transfer(address, uint256) external returns (bool) {
        if (armed) {
            armed = false;
            bytes[] memory empty = new bytes[](0);
            // This reentrant call must revert due to nonReentrant -
            // swallow the revert here so the OUTER call's own revert
            // reason ("token transfer failed", since this transfer()
            // itself then returns false) is what the test asserts on.
            try vaultRef.release(payable(reentryTo), address(0), 1, reentryReleaseId, empty) {
                return true;
            } catch {
                return false;
            }
        }
        return false;
    }
}
