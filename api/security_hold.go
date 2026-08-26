package api

import "log"

// DepositsHeldForSecurityRemediation is a temporary, explicit, code-level
// hold on every deposit-minting path in this codebase - legacy bridge
// mirror-model deposits (ETHB/BTCB) and real vault lock-and-mint deposits
// (ETH_CCC1/USDC_CCC1/BTC_CCC1) alike - put in place 2026-08-15 at the
// user's direction after a full external security audit surfaced several
// Critical-severity findings that are actively exploitable against
// deposit accounting on live mainnet:
//
//   - C-01: /tx/submit accepts an arbitrary signed transaction, and
//     applyBridgeDeposit/applyBridgeBtcDeposit's "node-authored" auth
//     check only proves SOME key signed the report, never that it came
//     from an authorized node - anyone can forge a deposit report and
//     mint themselves ETHB/BTCB with nothing real behind it.
//   - C-05: applyVaultLockAttest's attestation ID doesn't cover the
//     claimed amount/recipient/public key, so a malicious bridge
//     validator can front-run a real deposit with a fraudulent claim and
//     have honest validators' later votes on the genuine event counted
//     toward the attacker's already-recorded fraudulent one.
//
// This is a deliberately blunt stopgap - it blocks ALL new deposits,
// forged or genuine, rather than trying to distinguish them (which is
// exactly what C-01/C-05 make impossible to do safely right now).
// Existing balances, withdrawals, transfers, loans, and every other
// feature are completely unaffected - only NEW deposit credit is held.
//
// This was originally gated on C-01 and C-05 alone; when C-02/C-03/
// H-03/M-01/M-02 also turned out to bear on deposit-accounting safety
// (see audit/KNOWN_GAPS.md's "External security audit remediation"
// section), the user explicitly extended the hold to cover the full
// finding set rather than lift it early. All ten findings (5 Critical,
// 3 High, 2 Medium) now have a fix in place, tested and deployed -
// **lifted 2026-08-16.** This is intentionally a compiled-in default,
// not a governance/runtime-configurable toggle - no HTTP handler or
// transaction type anywhere in this codebase ever writes to this
// variable, so in production it only ever changes via a deliberate
// source edit and redeploy, the same weight as putting it in place.
// It's a var rather than a const purely so tests can temporarily
// override it (see security_hold_test.go's allowDepositsForTest) to
// keep exercising the underlying crediting logic - and, as of this
// change, so it can be flipped back to true the same way if a new
// deposit-accounting-relevant gap is ever found.
var DepositsHeldForSecurityRemediation = false

// depositHeld logs once per rejected attempt (so a real, otherwise-valid
// deposit report doesn't just silently vanish from the operator's view)
// and reports whether the caller should return immediately without
// crediting anything. kind/txID are purely for the log line.
func depositHeld(kind, txID string) bool {
	if !DepositsHeldForSecurityRemediation {
		return false
	}
	log.Println("deposit held (security remediation in progress):", kind, "tx", txID)
	return true
}
