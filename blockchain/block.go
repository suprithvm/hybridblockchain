package blockchain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
	"bytes"
	"encoding/gob"
)

//Block represents a single block in the blockchain
type Block struct{
	BlockNumber int
	PreviousHash string
	Timestamp int64
	Transactions []Transaction
	Nonce int
	Hash string
	Difficulty int
}

//GensisBlock creates the first block in the blockchain

func GenesisBlock() Block{
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

//function to calculate the hash of a block
func calculateHash(block Block) string{
	data := string(block.BlockNumber) + block.PreviousHash + string(block.Timestamp) + string(block.Nonce)
	
	hash := sha256.Sum256([]byte(data))

	return hex.EncodeToString(hash[:])
}


//newblock creates a new block in the blockchain

func NewBlock(previousBlock Block, transactions []Transaction, difficulty int)Block{
	block := Block{
		BlockNumber: previousBlock.BlockNumber + 1,
		PreviousHash: previousBlock.Hash,
		Timestamp: time.Now().Unix(),
		Transactions: transactions,
		Nonce: 0,
		Difficulty: difficulty,
	}
	block.Hash = calculateHash(block)
	return block
}


// MineBlock performs Proof of Work to find a valid nonce
func MineBlock(block *Block) {
	for {
		block.Hash = calculateHash(*block)
		if isHashValid(block.Hash, block.Difficulty) {
			break
		}
		block.Nonce++
	}
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