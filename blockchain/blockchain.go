package blockchain

import "log"

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

func (bc *Blockchain) AddBlock(newBlock Block, validator string, stakePool *StakePool) {
	// Validate the block before adding it to the chain.
	if ValidateBlock(newBlock, bc.GetLatestBlock(), validator, stakePool) {
		bc.Chain = append(bc.Chain, newBlock)
		log.Printf("Block %d added to the chain.\n", newBlock.BlockNumber)
	} else {
		log.Printf("Block %d failed validation and was not added.\n", newBlock.BlockNumber)
	}
}



// GetLatestBlock retrieves the most recent block in the chain
func (bc *Blockchain) GetLatestBlock() Block {
	return bc.Chain[len(bc.Chain)-1]
}


func ValidateBlock(newBlock Block, previousBlock Block, validator string, stakePool *StakePool) bool {
	// Check previous hash
	if newBlock.PreviousHash != previousBlock.Hash {
		log.Println("Validation failed: Previous hash mismatch.")
		return false
	}

	// Check block number
	if newBlock.BlockNumber != previousBlock.BlockNumber+1 {
		log.Println("Validation failed: Block number is incorrect.")
		return false
	}

	// Check hash validity
	if newBlock.Hash != calculateHash(newBlock) {
		log.Println("Validation failed: Hash mismatch.")
		return false
	}

	// Check PoW difficulty
	if !isHashValid(newBlock.Hash, newBlock.Difficulty) {
		log.Println("Validation failed: Hash does not meet difficulty.")
		return false
	}

	// Ensure the validator is staked
	if _, exists := stakePool.Stakes[validator]; !exists {
		log.Println("Validation failed: Validator not staked.")
		return false
	}

	return true
}




// ValidateGenesisBlock ensures all nodes use the same genesis block
func ValidateGenesisBlock(bc Blockchain, genesis Block) bool {
	return bc.Chain[0].Hash == genesis.Hash
}



