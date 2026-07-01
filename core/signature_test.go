package core

import "testing"

func TestSignature(
	t *testing.T,
) {

	pub,
	priv,
	_ :=
		GenerateKeyPair()

	message :=
		[]byte(
			"creditchain",
		)

	signature :=
		SignMessage(
			priv,
			message,
		)

	if !VerifySignature(
		pub,
		message,
		signature,
	) {
		t.Fail()
	}
}