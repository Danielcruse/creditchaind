package core

type Blockchain struct {
	Blocks []Block
}

func NewBlockchain() *Blockchain {
	genesis := NewGenesisBlock()

	return &Blockchain{
		Blocks: []Block{
			genesis,
		},
	}
}

func (bc *Blockchain) LatestBlock() Block {
	return bc.Blocks[len(bc.Blocks)-1]
}

func (bc *Blockchain) AddBlock(
	validator string,
	txs []Transaction,
) {
	latest := bc.LatestBlock()

	block := NewBlock(
		latest.Index+1,
		latest.Hash,
		validator,
		txs,
	)

	bc.Blocks = append(
		bc.Blocks,
		block,
	)
}

func (bc *Blockchain) AddBlockByValidator(
	validator *Validator,
	txs []Transaction,
) {
	bc.AddBlock(
		validator.Wallet.Address,
		txs,
	)

	validator.Rewards += 10
	validator.BlocksProduced++
	validator.VotingPower++
}