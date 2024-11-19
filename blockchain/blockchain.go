package blockchain

//chain of blocks are stored
type Blockchain struct{
	Chain []Block
}

//intializes the blockchain with the genesis block 

func InitialiseBlockchain() Blockchain{

	genesis := GenesisBlock()
	blockchain := Blockchain{}

	blockchain.Chain = append(blockchain.Chain,genesis)
	return blockchain
}


//Add new block to the blockchain

func (bc *Blockchain) AddBlock(newBlock Block){
	if ValidateBlock(newBlock,bc.GetLatestBlock()){
		bc.Chain = append(bc.Chain,newBlock)
	}
}


// GetLatestBlock retrieves the most recent block in the chain
func (bc *Blockchain) GetLatestBlock() Block {
	return bc.Chain[len(bc.Chain)-1]
}


// ValidateBlock ensures the block follows the chain's rules
func ValidateBlock(newBlock Block, previousBlock Block) bool {
	if newBlock.PreviousHash != previousBlock.Hash {
		return false
	}
	if newBlock.BlockNumber != previousBlock.BlockNumber+1 {
		return false
	}
	if newBlock.Hash != calculateHash(newBlock) {
		return false
	}
	if !isHashValid(newBlock.Hash, newBlock.Difficulty) {
		return false
	}
	return true
}


// ValidateGenesisBlock ensures all nodes use the same genesis block
func ValidateGenesisBlock(bc Blockchain, genesis Block) bool {
	return bc.Chain[0].Hash == genesis.Hash
}



