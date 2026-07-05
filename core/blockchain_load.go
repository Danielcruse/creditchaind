package core

import (
	"encoding/json"
	"os"
)

func (bc *Blockchain) Load(
	filename string,
) error {

	data, err :=
		os.ReadFile(
			filename,
		)

	if err != nil {
		return err
	}

	var blocks []Block

	err =
		json.Unmarshal(
			data,
			&blocks,
		)

	if err != nil {
		return err
	}

	bc.Blocks =
		blocks

	return nil
}