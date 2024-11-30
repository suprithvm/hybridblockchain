package blockchain

import (
	"fmt"
	"log"

	"github.com/libp2p/go-libp2p/core/host"
)

// chain of blocks are stored
type Blockchain struct {
	Chain []Block
}

//intializes the blockchain with the genesis block

func InitialiseBlockchain() Blockchain {

	genesis := GenesisBlock()
	blockchain := Blockchain{}

	blockchain.Chain = append(blockchain.Chain, genesis)
	return blockchain
}

// Add peerHost as a parameter to blockchain methods where necessary
func (bc *Blockchain) AddBlock(mempool *Mempool, stakePool *StakePool, utxoSet map[string]UTXO, peerHost host.Host) {
	previousBlock := bc.GetLatestBlock()

	// Select validator
	validatorWallet, validatorHost, err := stakePool.SelectValidator(peerHost)
	if err != nil {
		log.Printf("Failed to select validator: %v", err)
		return
	}

	// Create the new block with correct cumulative difficulty
	newBlock := NewBlock(previousBlock, mempool, utxoSet, previousBlock.Difficulty, validatorWallet)
	newBlock.CumulativeDifficulty = previousBlock.CumulativeDifficulty + newBlock.Difficulty

	// Mine and validate the block
	err = MineBlock(&newBlock, previousBlock, stakePool, 10, peerHost)
	if err != nil {
		log.Printf("Failed to mine block: %v", err)
		return
	}

	// Validate and add the block to the chain
	if ValidateBlock(newBlock, previousBlock, validatorWallet, stakePool) {
		bc.Chain = append(bc.Chain, newBlock)
		log.Printf("Block %d added by validator Wallet=%s HostID=%s.\n", newBlock.BlockNumber, validatorWallet, validatorHost)

		// Remove transactions from mempool
		blockTxs := newBlock.Transactions.GetAllTransactions()
		for _, tx := range blockTxs {
			mempool.RemoveTransaction(tx.TransactionID)
		}
	} else {
		log.Printf("Block %d validation failed.\n", newBlock.BlockNumber)
	}
}

// GetLatestBlock retrieves the most recent block in the chain
func (bc *Blockchain) GetLatestBlock() Block {
	return bc.Chain[len(bc.Chain)-1]
}

func ValidateBlock(newBlock Block, previousBlock Block, validator string, stakePool *StakePool) bool {
	if newBlock.PreviousHash != previousBlock.Hash {
		log.Println("Validation failed: Previous hash mismatch. Checking fork resolution...")

		// Compare chain lengths or cumulative difficulty
		if newBlock.Difficulty > previousBlock.Difficulty || previousBlock.Transactions.Len() > 0 {
			log.Println("Switching to the longer or higher difficulty chain.")
			return true
		}
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

	// Validate cumulative difficulty
	expectedCumulativeDifficulty := previousBlock.CumulativeDifficulty + newBlock.Difficulty
	if newBlock.CumulativeDifficulty != expectedCumulativeDifficulty {
		log.Printf("Invalid cumulative difficulty. Expected: %d, Got: %d",
			expectedCumulativeDifficulty, newBlock.CumulativeDifficulty)
		return false
	}

	return true
}

// ValidateGenesisBlock ensures all nodes use the same genesis block
func ValidateGenesisBlock(bc Blockchain, genesis Block) bool {
	return bc.Chain[0].Hash == genesis.Hash
}

func (bc *Blockchain) ExecuteMultiSigTransaction(tx *MultiSigTransaction, wallet *MultiSigwWallet, utxoSet map[string]UTXO) error {
	// Validate sufficient balance
	if wallet.Balance < tx.Amount {
		return fmt.Errorf("insufficient balance in multi-signature wallet")
	}

	// Validate required signatures
	if !tx.ValidateSignatures(wallet, wallet.PublicKeyMap) {
		return fmt.Errorf("insufficient valid signatures for transaction")
	}

	// Deduct funds from wallet
	if err := wallet.DeductFunds(tx.Amount); err != nil {
		return err
	}

	// Update UTXO set
	UpdateUTXOSet(tx.Transaction, utxoSet)
	return nil
}

// ResolveFork resolves forks by selecting the chain with the highest cumulative difficulty
// ResolveFork selects the chain with the highest cumulative difficulty.
func (bc *Blockchain) ResolveFork(candidateChain []Block) {
	currentCumulativeDifficulty := bc.Chain[len(bc.Chain)-1].CumulativeDifficulty
	candidateCumulativeDifficulty := candidateChain[len(candidateChain)-1].CumulativeDifficulty

	// Validate candidate chain
	if !bc.ValidateCandidateChain(candidateChain) {
		log.Println("[Fork Resolution] Candidate chain is invalid.")
		return
	}

	// Compare cumulative difficulties
	if candidateCumulativeDifficulty > currentCumulativeDifficulty {
		log.Printf("[Fork Resolution] Adopting new chain. Candidate cumulative difficulty: %d > Current: %d",
			candidateCumulativeDifficulty, currentCumulativeDifficulty)
		bc.Chain = candidateChain
	} else {
		log.Println("[Fork Resolution] Retaining current chain.")
	}
}

// ValidateCandidateChain checks the structural and cryptographic validity of a candidate chain.
func (bc *Blockchain) ValidateCandidateChain(candidateChain []Block) bool {
	if len(candidateChain) == 0 {
		return false
	}

	// Validate genesis block if it's included
	if candidateChain[0].BlockNumber == 0 {
		if candidateChain[0].PreviousHash != "0x00000000000000000000000000000000" ||
			candidateChain[0].CumulativeDifficulty != candidateChain[0].Difficulty {
			return false
		}
	}

	// Validate the rest of the chain
	for i := 1; i < len(candidateChain); i++ {
		currentBlock := candidateChain[i]
		previousBlock := candidateChain[i-1]

		// Basic block validation
		if currentBlock.BlockNumber != previousBlock.BlockNumber+1 ||
			currentBlock.PreviousHash != previousBlock.Hash {
			return false
		}

		// Validate cumulative difficulty
		expectedCumulative := previousBlock.CumulativeDifficulty + currentBlock.Difficulty
		if currentBlock.CumulativeDifficulty != expectedCumulative {
			log.Printf("[Validation] Block %d has incorrect cumulative difficulty. Expected %d, got %d",
				currentBlock.BlockNumber, expectedCumulative, currentBlock.CumulativeDifficulty)
			return false
		}
	}
	return true
}

// ReplaceChain replaces the current chain with a new one after validation
func (bc *Blockchain) ReplaceChain(newChain []Block) {
	if len(newChain) <= len(bc.Chain) {
		log.Println("Chain replacement rejected: New chain is not longer.")
		return
	}
	if bc.ValidateCandidateChain(newChain) {
		bc.Chain = newChain
		log.Println("Chain successfully replaced.")
	} else {
		log.Println("Chain replacement failed: Validation of the new chain failed.")
	}
}

func (bc *Blockchain) ResolveChainConflict(receivedChain []Block) bool {
	localDifficulty := bc.Chain[len(bc.Chain)-1].CumulativeDifficulty
	remoteDifficulty := receivedChain[len(receivedChain)-1].CumulativeDifficulty

	if remoteDifficulty > localDifficulty {
		log.Println("Adopting new chain due to higher cumulative difficulty.")
		bc.Chain = receivedChain
		return true
	}
	log.Println("Keeping current chain due to higher or equal difficulty.")
	return false
}
