package blockchain

import (
	"fmt"
	"log"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
)

// chain of blocks are stored
type Blockchain struct {
	Chain       []Block
	Node        *Node
	mu          sync.RWMutex
	currentHash string
}

//intializes the blockchain with the genesis block

func InitialiseBlockchain() *Blockchain {

	genesis := GenesisBlock()
	blockchain := &Blockchain{}

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
func ValidateGenesisBlock(bc *Blockchain, genesis Block) bool {
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
func (bc *Blockchain) ResolveFork(candidateChain []Block) bool {
	// Validate chain length
	if len(candidateChain) <= len(bc.Chain) {
		log.Println("[Fork Resolution] Candidate chain is not longer than current chain")
		return false
	}

	// Find common ancestor
	commonAncestorIndex := bc.findCommonAncestor(candidateChain)
	if commonAncestorIndex == -1 {
		log.Println("[Fork Resolution] No common ancestor found")
		return false
	}

	// Validate the candidate chain from common ancestor
	if !bc.validateChainSegment(candidateChain[commonAncestorIndex:]) {
		log.Println("[Fork Resolution] Invalid chain segment from common ancestor")
		return false
	}

	// Compare cumulative difficulty
	currentDifficulty := bc.Chain[len(bc.Chain)-1].CumulativeDifficulty
	candidateDifficulty := candidateChain[len(candidateChain)-1].CumulativeDifficulty

	if candidateDifficulty <= currentDifficulty {
		log.Printf("[Fork Resolution] Candidate chain difficulty (%d) not higher than current chain (%d)",
			candidateDifficulty, currentDifficulty)
		return false
	}

	// Reorganize the chain
	return bc.reorganizeChain(candidateChain, commonAncestorIndex)
}

// Add helper method to find common ancestor
func (bc *Blockchain) findCommonAncestor(candidateChain []Block) int {
	for i := len(candidateChain) - 1; i >= 0; i-- {
		candidateBlock := candidateChain[i]
		for j := len(bc.Chain) - 1; j >= 0; j-- {
			if bc.Chain[j].Hash == candidateBlock.Hash {
				return j
			}
		}
	}
	return -1
}

// Add helper method to validate chain segment
func (bc *Blockchain) validateChainSegment(segment []Block) bool {
	for i := 1; i < len(segment); i++ {
		// Validate block links
		if segment[i].PreviousHash != segment[i-1].Hash {
			return false
		}

		// Validate block numbers
		if segment[i].BlockNumber != segment[i-1].BlockNumber+1 {
			return false
		}

		// Validate cumulative difficulty
		expectedDifficulty := segment[i-1].CumulativeDifficulty + segment[i].Difficulty
		if segment[i].CumulativeDifficulty != expectedDifficulty {
			return false
		}
	}
	return true
}

// Add helper method to reorganize chain
func (bc *Blockchain) reorganizeChain(newChain []Block, commonAncestorIndex int) bool {
	// Create backup of current chain
	oldChain := make([]Block, len(bc.Chain))
	copy(oldChain, bc.Chain)

	// Attempt reorganization
	bc.Chain = append(bc.Chain[:commonAncestorIndex+1], newChain[commonAncestorIndex+1:]...)

	// Verify the new chain state
	if !bc.ValidateCandidateChain(bc.Chain) {
		// Restore old chain if validation fails
		bc.Chain = oldChain
		log.Println("[Fork Resolution] Chain reorganization failed, reverting to previous chain")
		return false
	}

	log.Printf("[Fork Resolution] Successfully reorganized chain. New height: %d", len(bc.Chain))
	return true
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

// ValidateBlock validates a block before adding it to the chain
func (bc *Blockchain) ValidateBlock(block *Block) error {
	// Verify block number
	if block.BlockNumber != bc.GetLatestBlock().BlockNumber+1 {
		return fmt.Errorf("invalid block number")
	}

	// Verify previous hash
	if block.PreviousHash != bc.GetLatestBlock().Hash {
		return fmt.Errorf("invalid previous hash")
	}

	// Verify block hash
	if block.Hash != block.CalculateHash() {
		return fmt.Errorf("invalid block hash")
	}

	return nil
}

// AddBlockWithoutValidation adds a block to the chain without validation (for sync purposes)
func (bc *Blockchain) AddBlockWithoutValidation(block *Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	bc.Chain = append(bc.Chain, *block)
	return nil
}

// Add these methods to the Blockchain struct

// GetCheckpoints retrieves checkpoints between start and end heights
func (bc *Blockchain) GetCheckpoints(startHeight, endHeight uint64) []*Checkpoint {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	var checkpoints []*Checkpoint
	currentHeight := startHeight

	for currentHeight <= endHeight {
		if block := bc.GetBlockByHeight(int(currentHeight)); block != nil {
			// Create checkpoint at interval
			if currentHeight%CheckpointInterval == 0 {
				checkpoint := block.CreateCheckpoint()
				checkpoints = append(checkpoints, checkpoint)
			}
		}
		currentHeight++
	}
	return checkpoints
}

// GetHeadersSinceCheckpoint gets block headers after the last checkpoint
func (bc *Blockchain) GetHeadersSinceCheckpoint(checkpointHeight, endHeight uint64) []BlockHeader {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	var headers []BlockHeader
	for height := checkpointHeight + 1; height <= endHeight; height++ {
		if block := bc.GetBlockByHeight(int(height)); block != nil {
			header := BlockHeader{
				Hash:              block.Hash,
				PreviousHash:      block.PreviousHash,
				Height:            uint64(block.BlockNumber),
				Timestamp:         block.Timestamp,
				MerkleRoot:        block.Transactions.GenerateRootHash(),
				StateRoot:         block.PatriciaRoot,
				Difficulty:        uint64(block.Difficulty),
				TotalTransactions: uint32(block.Transactions.Len()),
			}
			headers = append(headers, header)
		}
	}
	return headers
}

// FastForwardToCheckpoint fast forwards the chain to a verified checkpoint
func (bc *Blockchain) FastForwardToCheckpoint(cp *Checkpoint) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Verify checkpoint integrity
	if err := bc.verifyCheckpoint(cp); err != nil {
		return fmt.Errorf("invalid checkpoint: %v", err)
	}

	// Create genesis-like block from checkpoint
	checkpointBlock := Block{
		BlockNumber:          int(cp.Height),
		Hash:                 cp.Hash,
		PatriciaRoot:         cp.StateRoot,
		Timestamp:            cp.Timestamp,
		CumulativeDifficulty: 0, // Will be updated when syncing remaining blocks
	}

	// Reset chain to checkpoint
	bc.Chain = make([]Block, 0, cp.Height+1)
	bc.Chain = append(bc.Chain, checkpointBlock)
	bc.currentHash = cp.Hash

	return nil
}

// verifyCheckpoint verifies checkpoint data integrity
func (bc *Blockchain) verifyCheckpoint(cp *Checkpoint) error {
	if cp == nil {
		return fmt.Errorf("nil checkpoint")
	}

	// Verify checkpoint height is at interval
	if cp.Height%CheckpointInterval != 0 {
		return fmt.Errorf("invalid checkpoint height: %d", cp.Height)
	}

	// Verify state roots
	if len(cp.StateRoot) == 0 || len(cp.UTXORoot) == 0 {
		return fmt.Errorf("missing state roots")
	}

	return nil
}

// Remove the duplicate method and update the existing one to handle both types
func (bc *Blockchain) GetBlockByHeight(height interface{}) *Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	
	var h int
	switch v := height.(type) {
	case int:
		h = v
	case uint64:
		h = int(v)
	default:
		return nil
	}
	
	if h < 0 || h >= len(bc.Chain) {
		return nil
	}
	return &bc.Chain[h]
}

// Add these methods to Blockchain struct
func (bc *Blockchain) GetHeight() uint64 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return uint64(len(bc.Chain) - 1)
}

func (bc *Blockchain) RollbackToHeight(height uint64) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	
	if height >= uint64(len(bc.Chain)) {
		return fmt.Errorf("invalid rollback height")
	}
	
	bc.Chain = bc.Chain[:height+1]
	return nil
}
