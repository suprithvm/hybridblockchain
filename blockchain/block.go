package blockchain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
	"bytes"
	"encoding/gob"
	"strconv"
	"log"
)

// Block represents a single block in the blockchain
type Block struct {
	BlockNumber   int
	PreviousHash  string
	Timestamp     int64
	PatriciaRoot  string        // Root of the Patricia Trie
	Transactions  *PatriciaTrie // Replace list with Patricia Trie
	Nonce         int
	Hash          string
	Difficulty    int
}

// GenesisBlock creates the first block in the blockchain
func GenesisBlock() Block {
	genesis := Block{
		BlockNumber: 0,
		PreviousHash: "0x00000000000000000000000000000000",
		Timestamp: time.Now().Unix(),
		Transactions: nil,
		Nonce: 0,
		Difficulty: 1,
	}
	genesis.Hash = calculateHash(genesis)
	return genesis
}

// function to calculate the hash of a block
func calculateHash(block Block) string {
	data := strconv.Itoa(block.BlockNumber) + block.PreviousHash + strconv.FormatInt(block.Timestamp, 10) + strconv.Itoa(block.Nonce)
	
	hash := sha256.Sum256([]byte(data))

	return hex.EncodeToString(hash[:])
}

// NewBlock creates a new block in the blockchain.
func NewBlock(previousBlock Block, transactions []Transaction, difficulty int) Block {
	trie := NewPatriciaTrie()
	for _, tx := range transactions {
		trie.Insert(tx)
	}

	block := Block{
		BlockNumber:  previousBlock.BlockNumber + 1,
		PreviousHash: previousBlock.Hash,
		Timestamp:    time.Now().Unix(),
		PatriciaRoot: trie.GenerateRootHash(),
		Transactions: trie,
		Nonce:        0,
		Difficulty:   difficulty,
	}
	block.Hash = calculateHash(block)
	return block
}


// MineBlock performs mining with validator selection.
func MineBlock(block *Block, previousBlock Block, stakePool *StakePool, targetTime int64) error {
	// Select validator based on stake pool.
	validator, err := stakePool.SelectValidator()
	if err != nil {
		return err
	}
	log.Printf("Selected Validator: %s\n", validator)

	// Adjust difficulty based on the target time.
	block.Difficulty = AdjustDifficulty(previousBlock, targetTime)

	// Perform Proof of Work.
	for {
		block.Hash = calculateHash(*block)
		if isHashValid(block.Hash, block.Difficulty) {
			break
		}
		block.Nonce++
	}

	return nil
}


// isHashValid checks if a hash meets the difficulty target
func isHashValid(hash string, difficulty int) bool {
	prefix := ""
	for i := 0; i < difficulty; i++ {
		prefix += "0"
	}
	return hash[:difficulty] == prefix
}

// SerializeBlock serializes a block
func SerializeBlock(block Block) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := gob.NewEncoder(&buffer)
	err := encoder.Encode(block)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// DeserializeBlock deserializes a block
func DeserializeBlock(data []byte) (Block, error) {
	var block Block
	decoder := gob.NewDecoder(bytes.NewReader(data))
	err := decoder.Decode(&block)
	return block, err
}

func AdjustDifficulty(previousBlock Block, targetTime int64) int {
	actualTime := time.Now().Unix() - previousBlock.Timestamp
	if actualTime < targetTime/2 {
		return previousBlock.Difficulty + 1 // Increase difficulty
	} else if actualTime > targetTime*2 {
		return previousBlock.Difficulty - 1 // Decrease difficulty
	}
	return previousBlock.Difficulty
}

