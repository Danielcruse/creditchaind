package api

import (
	"os"
)

func LoadBlockchain() {

	_, err :=
		os.Stat(
			"blockchain.json",
		)

	if err != nil {
		return
	}

	blockchain.Load(
		"blockchain.json",
	)
}