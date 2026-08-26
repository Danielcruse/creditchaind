package api

import "testing"

// makeRealPoSRValidator sets up address as a genuine, currently-active
// PoSR validator - Active, staked >= core.MinValidatorStake, reputation
// >= core.MinValidatorReputationGate - the exact bar isRealPoSRValidator
// checks.
func makeRealPoSRValidator(address string) {
	validatorStore.Register(address, "node-"+address, "pubkey-"+address)
	stakingStore.Stakes[address] = map[string]uint64{"CCT": 1000}
	walletStore.Get(address).Reputation = 50
}

// TestIsBridgeValidatorAddress_RealPoSRValidatorIsEligible is the core
// regression test for the fix: a genuine, active, sufficiently-staked
// CreditChain validator should be treated as bridge-validator-eligible
// even without ever being listed in BRIDGE_VALIDATOR_ADDRESSES.
func TestIsBridgeValidatorAddress_RealPoSRValidatorIsEligible(t *testing.T) {
	resetApplyTestState()
	t.Setenv("BRIDGE_VALIDATOR_ADDRESSES", "")
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "")

	const addr = "real-posr-validator"
	makeRealPoSRValidator(addr)

	if !isBridgeValidatorAddress(addr) {
		t.Fatal("expected a genuine, active, sufficiently-staked PoSR validator to be bridge-validator-eligible")
	}
}

// TestIsBridgeValidatorAddress_AllowlistAndPoSRAreBothIndependentPaths
// confirms both eligibility paths work independently - see
// bridge_validator.go's header doc comment for why the allowlist was
// restored on 2026-08-20 after briefly being removed entirely: removing
// it broke real production signing because neither of mainnet's two
// actual bridge-validator wallets (the only two the deployed vault
// contract itself recognizes) was yet a real PoSR validator.
func TestIsBridgeValidatorAddress_AllowlistAndPoSRAreBothIndependentPaths(t *testing.T) {
	resetApplyTestState()
	t.Setenv("BRIDGE_VALIDATOR_ADDRESSES", "val-a,val-b,val-c")
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "2")

	if !isBridgeValidatorAddress("val-a") {
		t.Fatal("expected an allowlisted address to be eligible even without real PoSR validator status")
	}
	if isBridgeValidatorAddress("not-in-the-list") {
		t.Fatal("expected an address in neither the allowlist nor a real validator to be ineligible")
	}

	makeRealPoSRValidator("posr-only-validator")
	if !isBridgeValidatorAddress("posr-only-validator") {
		t.Fatal("expected a genuine PoSR validator to be eligible even without being in BRIDGE_VALIDATOR_ADDRESSES")
	}
}

// TestIsBridgeValidatorAddress_RejectsEachFailingCriterion confirms
// isRealPoSRValidator actually enforces every one of its stated bars,
// not just some of them.
func TestIsBridgeValidatorAddress_RejectsEachFailingCriterion(t *testing.T) {
	resetApplyTestState()
	t.Setenv("BRIDGE_VALIDATOR_ADDRESSES", "")
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "")

	t.Run("not registered at all", func(t *testing.T) {
		resetApplyTestState()
		if isBridgeValidatorAddress("never-registered") {
			t.Fatal("expected an unregistered address to be ineligible")
		}
	})

	t.Run("registered but inactive", func(t *testing.T) {
		resetApplyTestState()
		const addr = "inactive-validator"
		makeRealPoSRValidator(addr)
		status, _ := validatorStore.Get(addr)
		status.Active = false
		if isBridgeValidatorAddress(addr) {
			t.Fatal("expected an inactive validator to be ineligible")
		}
	})

	t.Run("permanently banned", func(t *testing.T) {
		resetApplyTestState()
		const addr = "banned-validator"
		makeRealPoSRValidator(addr)
		status, _ := validatorStore.Get(addr)
		status.PermanentlyBanned = true
		if isBridgeValidatorAddress(addr) {
			t.Fatal("expected a permanently-banned validator to be ineligible")
		}
	})

	t.Run("stake below MinValidatorStake", func(t *testing.T) {
		resetApplyTestState()
		const addr = "understaked-validator"
		makeRealPoSRValidator(addr)
		stakingStore.Stakes[addr]["CCT"] = 999 // core.MinValidatorStake is 1000
		if isBridgeValidatorAddress(addr) {
			t.Fatal("expected an under-staked validator to be ineligible")
		}
	})

	t.Run("reputation below MinValidatorReputationGate", func(t *testing.T) {
		resetApplyTestState()
		const addr = "low-reputation-validator"
		makeRealPoSRValidator(addr)
		walletStore.Get(addr).Reputation = 1 // core.MinValidatorReputationGate is 20
		if isBridgeValidatorAddress(addr) {
			t.Fatal("expected a low-reputation validator to be ineligible")
		}
	})
}
