package core

func (bc *Blockchain) ReplaceChain(
	blocks []Block,
) {
	if len(blocks) >
		len(bc.Blocks) {

		bc.Blocks =
			blocks
	}
}