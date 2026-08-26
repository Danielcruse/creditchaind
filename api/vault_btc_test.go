package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/creditchain/creditchaind/core"
)

// Phase C's blocking-gate note (see the bridge-vault plan's Verification
// section): a real bitcoind/testnet cross-check isn't possible in this
// environment, so these tests instead (a) hand-assemble the expected
// redeem-script bytes independently of txscript.MultiSigScript's own
// internals and compare, and (b) recompute the witness program via the
// standard library's crypto/sha256 directly, rather than only asserting
// against this package's own helper functions. This is a narrower gate
// than Phase B's Foundry suite - the plan documents this explicitly as a
// disclosed limitation, not an oversight.

func newTestBtcVaultSigner(t *testing.T) *localDevBtcVaultSigner {
	t.Helper()
	return newLocalDevBtcVaultSigner()
}

// manualMultisigRedeemScript builds a P2WSH multisig redeem script byte-
// for-byte by hand (OP_M, each <push-length><33-byte-pubkey>, OP_N,
// OP_CHECKMULTISIG), independent of txscript.MultiSigScript, so a bug in
// that call wouldn't also be silently present in the "expected" value.
func manualMultisigRedeemScript(t *testing.T, threshold int, pubKeys [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteByte(byte(txscript.OP_1 - 1 + threshold)) // OP_M
	for _, pk := range pubKeys {
		if len(pk) != 33 {
			t.Fatalf("manualMultisigRedeemScript: expected 33-byte compressed pubkey, got %d", len(pk))
		}
		buf.WriteByte(0x21) // push 33 bytes
		buf.Write(pk)
	}
	buf.WriteByte(byte(txscript.OP_1 - 1 + len(pubKeys))) // OP_N
	buf.WriteByte(txscript.OP_CHECKMULTISIG)
	return buf.Bytes()
}

// generateTestBtcPubKeys returns n real, valid compressed secp256k1
// public keys, hex-encoded - BuildBtcVaultRedeemScript requires actual
// points on the curve (btcutil.NewAddressPubKey validates this), so
// fixed made-up byte strings won't do.
func generateTestBtcPubKeys(t *testing.T, n int) []string {
	t.Helper()
	signer := newTestBtcVaultSigner(t)
	out := make([]string, n)
	for i := 0; i < n; i++ {
		_, pubHex, err := signer.NewKey(context.Background())
		if err != nil {
			t.Fatalf("generate test pubkey %d: %v", i, err)
		}
		out[i] = pubHex
	}
	return out
}

func testBridgeValidators(pubKeyHexes []string) map[string]*core.BridgeValidator {
	out := make(map[string]*core.BridgeValidator, len(pubKeyHexes))
	for i, pk := range pubKeyHexes {
		addr := hex.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 20))
		out[addr] = &core.BridgeValidator{
			CreditChainAddress: addr,
			BtcPublicKeyHex:    pk,
		}
	}
	return out
}

func TestBuildBtcVaultRedeemScript_MatchesManuallyAssembledScript(t *testing.T) {
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "2")
	pubKeyHexes := generateTestBtcPubKeys(t, 3)
	validators := testBridgeValidators(pubKeyHexes)

	got, err := BuildBtcVaultRedeemScript(validators)
	if err != nil {
		t.Fatalf("BuildBtcVaultRedeemScript failed: %v", err)
	}

	sortedRaw, err := sortedBtcVaultPublicKeys(validators)
	if err != nil {
		t.Fatalf("sortedBtcVaultPublicKeys failed: %v", err)
	}
	want := manualMultisigRedeemScript(t, 2, sortedRaw)

	if !bytes.Equal(got, want) {
		t.Fatalf("redeem script mismatch:\n got: %x\nwant: %x", got, want)
	}
}

func TestBuildBtcVaultRedeemScript_DeterministicRegardlessOfMapIteration(t *testing.T) {
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "2")
	pubKeyHexes := generateTestBtcPubKeys(t, 4)
	validators := testBridgeValidators(pubKeyHexes)

	var first []byte
	for i := 0; i < 10; i++ {
		script, err := BuildBtcVaultRedeemScript(validators)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if first == nil {
			first = script
			continue
		}
		if !bytes.Equal(first, script) {
			t.Fatalf("redeem script differed across repeated calls (map-iteration non-determinism) - every node MUST derive the identical script from the identical validator set")
		}
	}
}

func TestBuildBtcVaultRedeemScript_ErrorsWhenFewerKeysThanThreshold(t *testing.T) {
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "3")
	pubKeyHexes := generateTestBtcPubKeys(t, 2)
	validators := testBridgeValidators(pubKeyHexes)

	if _, err := BuildBtcVaultRedeemScript(validators); err == nil {
		t.Fatal("expected an error when fewer registered BTC keys than the threshold exist")
	}
}

func TestSortedBtcVaultPublicKeys_RejectsWrongLength(t *testing.T) {
	validators := map[string]*core.BridgeValidator{
		"addr1": {CreditChainAddress: "addr1", BtcPublicKeyHex: hex.EncodeToString(bytes.Repeat([]byte{0xAB}, 32))}, // 32, not 33
	}
	if _, err := sortedBtcVaultPublicKeys(validators); err == nil {
		t.Fatal("expected an error for a non-33-byte BTC public key")
	}
}

func TestBtcVaultWitnessProgram_MatchesStdlibSHA256(t *testing.T) {
	redeemScript := []byte{0x51, 0x21, 0x02, 0x03, 0x52, 0xae} // arbitrary bytes, content doesn't matter
	got := BtcVaultWitnessProgram(redeemScript)
	want := sha256.Sum256(redeemScript)
	if got != want {
		t.Fatalf("BtcVaultWitnessProgram = %x, want plain SHA256 = %x", got, want)
	}
	if len(got) != 32 {
		t.Fatalf("expected a 32-byte witness program, got %d", len(got))
	}
}

func TestBuildBtcVaultAddress_RoundTripsThroughDecode(t *testing.T) {
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "2")
	pubKeyHexes := generateTestBtcPubKeys(t, 3)
	validators := testBridgeValidators(pubKeyHexes)

	addr, redeemScript, err := BuildBtcVaultAddress(validators)
	if err != nil {
		t.Fatalf("BuildBtcVaultAddress failed: %v", err)
	}

	wantProgram := BtcVaultWitnessProgram(redeemScript)

	decoded, err := btcutil.DecodeAddress(addr.EncodeAddress(), &chaincfg.TestNet3Params)
	if err != nil {
		t.Fatalf("failed to decode the produced address string back: %v", err)
	}
	wsh, ok := decoded.(*btcutil.AddressWitnessScriptHash)
	if !ok {
		t.Fatalf("decoded address is not a P2WSH address: %T", decoded)
	}
	if !bytes.Equal(wsh.ScriptAddress(), wantProgram[:]) {
		t.Fatalf("decoded witness program %x does not match SHA256(redeemScript) %x", wsh.ScriptAddress(), wantProgram)
	}
	if addr.EncodeAddress()[:3] != "tb1" {
		t.Fatalf("expected a testnet bech32 (tb1...) address by default, got %s", addr.EncodeAddress())
	}
}

func TestLocalDevBtcVaultSigner_ProducesVerifiableSignature(t *testing.T) {
	signer := newTestBtcVaultSigner(t)
	keyRef, pubHex, err := signer.NewKey(context.Background())
	if err != nil {
		t.Fatalf("NewKey failed: %v", err)
	}

	var sighash [32]byte
	copy(sighash[:], bytes.Repeat([]byte{0x42}, 32))

	derSig, err := signer.Sign(context.Background(), keyRef, sighash)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	sig, err := ecdsa.ParseDERSignature(derSig)
	if err != nil {
		t.Fatalf("failed to parse the produced signature as DER: %v", err)
	}
	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil {
		t.Fatalf("failed to decode returned pubkey hex: %v", err)
	}
	pubKey, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		t.Fatalf("failed to parse returned pubkey: %v", err)
	}
	if !sig.Verify(sighash[:], pubKey) {
		t.Fatal("signature does not verify against the signer's own returned public key")
	}
}

func TestLocalDevBtcVaultSigner_RejectsUnknownKeyRef(t *testing.T) {
	signer := newTestBtcVaultSigner(t)
	var sighash [32]byte
	if _, err := signer.Sign(context.Background(), "not-a-real-key-ref", sighash); err == nil {
		t.Fatal("expected an error signing with an unknown key reference")
	}
}

func TestComputeBtcVaultWitnessSighash_DeterministicAndInputSensitive(t *testing.T) {
	redeemScript := []byte{txscript.OP_TRUE}
	var prevHash chainhash.Hash
	copy(prevHash[:], bytes.Repeat([]byte{0xAA}, 32))
	outpoint := wire.OutPoint{Hash: prevHash, Index: 0}

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))
	tx.AddTxOut(wire.NewTxOut(50000, []byte{0x51}))

	prevOuts := map[wire.OutPoint]*wire.TxOut{
		outpoint: {Value: 100000, PkScript: []byte{0x00, 0x20}},
	}

	d1, err := ComputeBtcVaultWitnessSighash(tx, 0, redeemScript, prevOuts, txscript.SigHashAll)
	if err != nil {
		t.Fatalf("ComputeBtcVaultWitnessSighash failed: %v", err)
	}
	d2, err := ComputeBtcVaultWitnessSighash(tx, 0, redeemScript, prevOuts, txscript.SigHashAll)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if d1 != d2 {
		t.Fatal("expected the sighash to be deterministic across repeated calls with identical inputs")
	}

	prevOutsChanged := map[wire.OutPoint]*wire.TxOut{
		outpoint: {Value: 999999, PkScript: []byte{0x00, 0x20}},
	}
	d3, err := ComputeBtcVaultWitnessSighash(tx, 0, redeemScript, prevOutsChanged, txscript.SigHashAll)
	if err != nil {
		t.Fatalf("third call failed: %v", err)
	}
	if d1 == d3 {
		t.Fatal("expected the sighash to change when the spent output's amount changes (BIP143 commits to the input amount)")
	}
}

func TestComputeBtcVaultWitnessSighash_RejectsOutOfRangeIndex(t *testing.T) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, nil, nil))
	if _, err := ComputeBtcVaultWitnessSighash(tx, 5, []byte{0x51}, map[wire.OutPoint]*wire.TxOut{}, txscript.SigHashAll); err == nil {
		t.Fatal("expected an error for an out-of-range input index")
	}
}

func TestAssembleBtcVaultWitness_OrdersSignaturesByScriptOrderNotSubmissionOrder(t *testing.T) {
	pubKeyHexesRaw := generateTestBtcPubKeys(t, 3)
	sortedPubKeys := make([][]byte, len(pubKeyHexesRaw))
	for i, h := range pubKeyHexesRaw {
		b, _ := hex.DecodeString(h)
		sortedPubKeys[i] = b
	}
	// Deliberately construct sortedPubKeys in a DIFFERENT order than the
	// generation order, simulating the real deterministic-sort step
	// (BuildBtcVaultRedeemScript would normally supply this already-
	// sorted) - what matters for this test is that AssembleBtcVaultWitness
	// follows sortedPubKeys' order, not the order signatures happen to be
	// registered in derSigsByPubKeyHex (a plain unordered map).
	redeemScript := manualMultisigRedeemScript(t, 2, sortedPubKeys)

	// Submit signatures "out of order" relative to sortedPubKeys - key
	// for index 2 submitted first, then index 0.
	sig2 := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	sig0 := []byte{0xCA, 0xFE, 0xBA, 0xBE}
	derSigs := map[string][]byte{
		hex.EncodeToString(sortedPubKeys[2]): sig2,
		hex.EncodeToString(sortedPubKeys[0]): sig0,
	}

	witness, err := AssembleBtcVaultWitness(redeemScript, sortedPubKeys, derSigs, 2, txscript.SigHashAll)
	if err != nil {
		t.Fatalf("AssembleBtcVaultWitness failed: %v", err)
	}

	// witness[0] = empty dummy, witness[1]/[2] = sigs in SCRIPT order
	// (index 0's sig, then index 2's sig - index 1 has no sig), witness
	// last = redeem script.
	if len(witness) != 4 {
		t.Fatalf("expected 4 witness items (dummy + 2 sigs + redeem script), got %d", len(witness))
	}
	if len(witness[0]) != 0 {
		t.Fatalf("expected witness[0] to be the empty OP_CHECKMULTISIG dummy, got %x", witness[0])
	}
	if !bytes.Equal(witness[1][:len(sig0)], sig0) {
		t.Fatalf("expected witness[1] to be pubkey-index-0's signature (script order), got %x", witness[1])
	}
	if !bytes.Equal(witness[2][:len(sig2)], sig2) {
		t.Fatalf("expected witness[2] to be pubkey-index-2's signature (script order), got %x", witness[2])
	}
	if !bytes.Equal(witness[3], redeemScript) {
		t.Fatal("expected the final witness item to be the redeem script")
	}
	// Confirm the sighash-type byte was appended.
	if witness[1][len(witness[1])-1] != byte(txscript.SigHashAll) {
		t.Fatal("expected the sighash type byte to be appended to each signature")
	}
}

func TestAssembleBtcVaultWitness_TrimsExcessSignaturesToThreshold(t *testing.T) {
	pubKeyHexesRaw := generateTestBtcPubKeys(t, 3)
	sortedPubKeys := make([][]byte, len(pubKeyHexesRaw))
	for i, h := range pubKeyHexesRaw {
		b, _ := hex.DecodeString(h)
		sortedPubKeys[i] = b
	}
	redeemScript := manualMultisigRedeemScript(t, 2, sortedPubKeys)

	derSigs := map[string][]byte{
		hex.EncodeToString(sortedPubKeys[0]): {0x01},
		hex.EncodeToString(sortedPubKeys[1]): {0x02},
		hex.EncodeToString(sortedPubKeys[2]): {0x03},
	}

	witness, err := AssembleBtcVaultWitness(redeemScript, sortedPubKeys, derSigs, 2, txscript.SigHashAll)
	if err != nil {
		t.Fatalf("AssembleBtcVaultWitness failed: %v", err)
	}
	// dummy + exactly 2 sigs (not 3) + redeem script.
	if len(witness) != 4 {
		t.Fatalf("expected exactly threshold(2) signatures in the witness (dummy+2+script=4 items), got %d items", len(witness))
	}
}

func TestAssembleBtcVaultWitness_ErrorsWhenBelowThreshold(t *testing.T) {
	pubKeyHexesRaw := generateTestBtcPubKeys(t, 3)
	sortedPubKeys := make([][]byte, len(pubKeyHexesRaw))
	for i, h := range pubKeyHexesRaw {
		b, _ := hex.DecodeString(h)
		sortedPubKeys[i] = b
	}
	redeemScript := manualMultisigRedeemScript(t, 2, sortedPubKeys)

	derSigs := map[string][]byte{
		hex.EncodeToString(sortedPubKeys[0]): {0x01},
	}
	if _, err := AssembleBtcVaultWitness(redeemScript, sortedPubKeys, derSigs, 2, txscript.SigHashAll); err == nil {
		t.Fatal("expected an error when fewer signatures than the threshold are available")
	}
}

func TestEncodeDecodeBtcVaultDepositMemo_RoundTrips(t *testing.T) {
	ccAddr := hex.EncodeToString(bytes.Repeat([]byte{0x11}, 20))
	pub := hex.EncodeToString(bytes.Repeat([]byte{0x22}, 32))

	memo, err := encodeBtcVaultDepositMemo(ccAddr, pub)
	if err != nil {
		t.Fatalf("encodeBtcVaultDepositMemo failed: %v", err)
	}
	if len(memo) != btcVaultDepositMemoLen {
		t.Fatalf("expected a %d-byte memo, got %d", btcVaultDepositMemoLen, len(memo))
	}

	opReturnScript, err := txscript.NullDataScript(memo)
	if err != nil {
		t.Fatalf("failed to build an OP_RETURN script from the memo: %v", err)
	}
	vout := []btcTxOutDetail{
		{ScriptPubKey: hex.EncodeToString(opReturnScript), ScriptPubKeyType: "op_return", Value: 0},
	}

	gotAddr, gotPub, ok := decodeBtcVaultDepositMemo(vout)
	if !ok {
		t.Fatal("expected decodeBtcVaultDepositMemo to find the well-formed memo")
	}
	if gotAddr != ccAddr {
		t.Fatalf("depositor address mismatch: got %s want %s", gotAddr, ccAddr)
	}
	if gotPub != pub {
		t.Fatalf("depositor public key mismatch: got %s want %s", gotPub, pub)
	}
}

func TestDecodeBtcVaultDepositMemo_RejectsMissingOrMalformedMemo(t *testing.T) {
	if _, _, ok := decodeBtcVaultDepositMemo(nil); ok {
		t.Fatal("expected no memo to be found in an empty output list")
	}

	shortMemoScript, err := txscript.NullDataScript([]byte{0x01, 0x02, 0x03})
	if err != nil {
		t.Fatalf("failed to build a short OP_RETURN script: %v", err)
	}
	vout := []btcTxOutDetail{
		{ScriptPubKey: hex.EncodeToString(shortMemoScript), ScriptPubKeyType: "op_return"},
	}
	if _, _, ok := decodeBtcVaultDepositMemo(vout); ok {
		t.Fatal("expected a too-short OP_RETURN payload to be rejected, not misparsed as a valid memo")
	}
}

func TestBtcVaultDepositOutputIndex_FindsTheVaultPayingOutput(t *testing.T) {
	vout := []btcTxOutDetail{
		{ScriptPubKeyType: "op_return", Value: 0},
		{ScriptPubKeyAddress: "tb1qsomeothersaddress", ScriptPubKeyType: "v0_p2wpkh", Value: 1000},
		{ScriptPubKeyAddress: "tb1qvaultaddresshere", ScriptPubKeyType: "v0_p2wsh", Value: 250000},
	}
	index, sats, ok := btcVaultDepositOutputIndex(vout, "tb1qvaultaddresshere")
	if !ok {
		t.Fatal("expected to find the vault-paying output")
	}
	if index != 2 {
		t.Fatalf("expected index 2, got %d", index)
	}
	if sats != 250000 {
		t.Fatalf("expected 250000 sats, got %d", sats)
	}

	if _, _, ok := btcVaultDepositOutputIndex(vout, "tb1qnomatch"); ok {
		t.Fatal("expected no match for an address that doesn't appear in the outputs")
	}
}

func TestBtcVaultNetParams_DefaultsToTestnetUnlessMainnetMode(t *testing.T) {
	t.Setenv("MAINNET_MODE", "")
	if got := btcVaultNetParams(); got.Net != chaincfg.TestNet3Params.Net {
		t.Fatalf("expected testnet params by default, got %v", got.Net)
	}
	t.Setenv("MAINNET_MODE", "true")
	if got := btcVaultNetParams(); got.Net != chaincfg.MainNetParams.Net {
		t.Fatalf("expected mainnet params when MAINNET_MODE=true, got %v", got.Net)
	}
}
