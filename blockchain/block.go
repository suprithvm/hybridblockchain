package blockchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
)

const BlockReward = 50.0                  // Reward for mining a block
const AvgTransactionSize = 250            // Average transaction size in bytes
const MaxBlockSizeLimit = 1 * 1024 * 1024 // 1MB block size limit

// Block represents a single block in the blockchain
type Block struct {
	BlockNumber          int
	PreviousHash         string
	Timestamp            int64
	PatriciaRoot         string        // Root of the Patricia Trie
	Transactions         *PatriciaTrie // Replace list with Patricia Trie
	Nonce                int
	Hash                 string
	Difficulty           int
	CumulativeDifficulty int // Sum of difficulties up to this block
}

// GenesisBlock creates the first block in the blockchain
func GenesisBlock() Block {
	genesis := Block{
		BlockNumber:          0,
		PreviousHash:         "0x00000000000000000000000000000000",
		Timestamp:            time.Now().Unix(),
		Transactions:         nil,
		Nonce:                0,
		Difficulty:           1,
		CumulativeDifficulty: 1,
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

// NewBlock creates a new block with validated transactions
func NewBlock(previousBlock Block, mempool *Mempool, utxoSet map[string]UTXO, difficulty int, validator string) Block {
	// Calculate the dynamic block size
	mempoolSize := len(mempool.GetTransactions())
	dynamicSize := CalculateDynamicBlockSize(mempoolSize)

	// Get prioritized transactions
	transactions := mempool.GetPrioritizedTransactions(dynamicSize)

	validTransactions := []Transaction{}
	totalGasFees := 0.0

	log.Printf("[DEBUG] Dynamic Block Size: %d", dynamicSize)

	// Validate transactions and update UTXO set
	for _, tx := range transactions {
		if mempool.ValidateTransaction(tx, utxoSet) {
			validTransactions = append(validTransactions, tx)
			totalGasFees += tx.GasFee
			UpdateUTXOSet(tx, utxoSet)
			log.Printf("[DEBUG] Valid Transaction: %+v", tx)
		} else {
			log.Printf("[DEBUG] Invalid Transaction Skipped: %+v", tx)
		}
	}

	// Add block reward transaction
	rewardAmount := BlockReward + totalGasFees
	rewardTxID := fmt.Sprintf("reward-%d", previousBlock.BlockNumber+1)
	rewardUTXO := UTXO{
		TransactionID: rewardTxID,
		OutputIndex:   0,
		Amount:        rewardAmount,
		Receiver:      validator,
	}

	rewardTransaction := Transaction{
		TransactionID: rewardTxID,
		Sender:        "BLOCKCHAIN",
		Receiver:      validator,
		Amount:        rewardAmount,
		GasFee:        0,
		Timestamp:     time.Now().Unix(),
		Outputs:       []UTXO{rewardUTXO},
	}
	utxoSet[fmt.Sprintf("%s-0", rewardTxID)] = rewardUTXO
	validTransactions = append(validTransactions, rewardTransaction)

	// Construct the Patricia Trie for transactions
	trie := NewPatriciaTrie()
	for _, tx := range validTransactions {
		trie.Insert(tx)
		log.Printf("[DEBUG] Transaction Added to Trie: %+v", tx)
	}

	log.Printf("[DEBUG] Patricia Trie Root Hash: %s", trie.GenerateRootHash())

	// Create the new block
	block := Block{
		BlockNumber:          previousBlock.BlockNumber + 1,
		PreviousHash:         previousBlock.Hash,
		Timestamp:            time.Now().Unix(),
		PatriciaRoot:         trie.GenerateRootHash(),
		Transactions:         trie,
		Difficulty:           difficulty,
		CumulativeDifficulty: previousBlock.CumulativeDifficulty + difficulty,
	}
	block.Hash = calculateHash(block)

	log.Printf("[DEBUG] New Block Created: %+v", block)
	return block
}

// MineBlock performs mining with validator selection.
func MineBlock(block *Block, previousBlock Block, stakePool *StakePool, targetTime int64, peerHost host.Host) error {
	// Select validator based on stake pool.
	validatorWallet, validatorHost, err := stakePool.SelectValidator(peerHost)
	if err != nil {
		return err
	}
	log.Printf("Selected Validator: Wallet=%s, HostID=%s\n", validatorWallet, validatorHost)

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
	lengthPrefix := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthPrefix, uint32(buffer.Len()))
	return append(lengthPrefix, buffer.Bytes()...), nil
}

// DeserializeBlock deserializes a block
func DeserializeBlock(data []byte) (Block, error) {
	if len(data) < 4 {
		return Block{}, fmt.Errorf("data too short to contain length prefix")
	}

	length := binary.BigEndian.Uint32(data[:4])
	if int(length) != len(data[4:]) {
		return Block{}, fmt.Errorf("data length mismatch: expected %d, got %d", length, len(data[4:]))
	}

	var block Block
	decoder := gob.NewDecoder(bytes.NewReader(data[4:]))
	err := decoder.Decode(&block)
	if err != nil {
		return Block{}, err
	}
	return block, nil
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

// Dynamic difficulty adjustment considering network latency and node power
func AdjustDifficultyDynamic(previousBlock Block, networkLatency int64, nodeProcessingPower float64) int {
	// Scale difficulty based on latency and processing power
	if networkLatency > 100 && nodeProcessingPower < 0.5 {
		return previousBlock.Difficulty - 1 // Lower difficulty for slower nodes
	} else if networkLatency < 50 && nodeProcessingPower > 1.0 {
		return previousBlock.Difficulty + 1 // Increase difficulty for faster nodes
	}
	return previousBlock.Difficulty
}

func AdjustDifficultyForTest() int {
	return 1 // Minimum difficulty for faster tests
}

// CalculateDynamicBlockSize calculates the appropriate block size based on mempool size and network constraints
func CalculateDynamicBlockSize(mempoolSize int) int {
	const minBlockSize = 1
	const maxBlockSize = MaxBlockSizeLimit / AvgTransactionSize

	if mempoolSize < minBlockSize {
		return minBlockSize
	}
	if mempoolSize > maxBlockSize {
		return maxBlockSize
	}
	return mempoolSize
}
