package blockchain

import (
	"log"
	"fmt"
	"github.com/libp2p/go-libp2p/core/host"
)

//chain of blocks are stored
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

	// Create the new block
	newBlock := NewBlock(previousBlock, mempool, utxoSet, previousBlock.Difficulty, validatorWallet)

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
		if newBlock.Difficulty > previousBlock.Difficulty || previousBlock.Transactions.Len() > 0  {
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
    for i := 1; i < len(candidateChain); i++ {
        currentBlock := candidateChain[i]
        previousBlock := candidateChain[i-1]

        // Check block linkage
        if currentBlock.PreviousHash != previousBlock.Hash {
            log.Printf("[Validation] Block %d has an invalid previous hash.", currentBlock.BlockNumber)
            return false
        }

        // Validate PoW difficulty
        if !isHashValid(currentBlock.Hash, currentBlock.Difficulty) {
            log.Printf("[Validation] Block %d does not meet difficulty requirements.", currentBlock.BlockNumber)
            return false
        }

        // Ensure cumulative difficulty is consistent
        expectedCumulative := previousBlock.CumulativeDifficulty + currentBlock.Difficulty
        if currentBlock.CumulativeDifficulty != expectedCumulative {
            log.Printf("[Validation] Block %d has inconsistent cumulative difficulty.", currentBlock.BlockNumber)
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
