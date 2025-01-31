package blockchain

import (
	"blockchain-core/blockchain/gas"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
)

const (
	BlockReward        = 50.0
	AvgTransactionSize = 250
	MaxBlockSizeLimit  = 1 * 1024 * 1024
	CheckpointInterval = 2
	MaxCheckpointAge   = 10
	EmptyBlockSize     = 10 * 1024       // 10KB for empty block
	MaxBlockSize       = 1 * 1024 * 1024 // 1MB max block size

	// Gas constants
	BaseGasLimit   = 15_000_000
	MinGasPrice    = 1_000
	TargetGasUsage = 0.8 // Target 80% gas usage
)

// BlockHeader contains block metadata
type BlockHeader struct {
	Version      uint32 // Block version
	BlockNumber  uint64 // Height of the block
	PreviousHash string // Hash of previous block
	Timestamp    int64  // Block creation time
	MerkleRoot   string // Merkle root of transactions
	StateRoot    string // State root after transactions
	ReceiptsRoot string // Root hash of transaction receipts
	Difficulty   uint32 // Mining difficulty
	Nonce        uint64 // PoW nonce
	GasLimit     uint64 // Maximum gas allowed
	GasUsed      uint64 // Actual gas used
	MinedBy      string // Address of miner
	ValidatedBy  string // Address of PoS validator
	ExtraData    []byte // Additional data (limited size)
}

// BlockBody contains the actual block data
type BlockBody struct {
	Transactions *PatriciaTrie
	Receipts     []*TxReceipt
}

// Block represents a complete block
type Block struct {
	Header               *BlockHeader
	Body                 *BlockBody
	hash                 string // Cached block hash
	size                 uint64 // Cached block size
	numTx                uint32 // Cached transaction count
	CumulativeDifficulty uint64 // Add this field
}

// TxReceipt stores transaction execution results
type TxReceipt struct {
	TxHash        string
	BlockHash     string
	BlockNumber   uint64
	GasUsed       uint64
	Status        uint64 // 1 success, 0 failure
	CumulativeGas uint64 // Total gas used up to this tx
}

// NewBlock creates a new block
func NewBlock(previousBlock Block, mempool *Mempool, utxoSet map[string]UTXO, difficulty uint32, validator string) Block {
	header := &BlockHeader{
		Version:      1,
		BlockNumber:  previousBlock.Header.BlockNumber + 1,
		PreviousHash: previousBlock.Hash(),
		Timestamp:    time.Now().Unix(),
		Difficulty:   difficulty,
		GasLimit:     BaseGasLimit,
		ValidatedBy:  validator,
	}

	body := &BlockBody{
		Transactions: NewPatriciaTrie(),
		Receipts:     make([]*TxReceipt, 0),
	}

	// Process transactions
	txs := mempool.GetPrioritizedTransactions(calculateDynamicBlockSize(len(mempool.GetTransactions())))
	cumulativeGas := uint64(0)

	for _, tx := range txs {
		gasNeeded := calculateGas(tx)
		if cumulativeGas+gasNeeded > header.GasLimit {
			break
		}

		if mempool.ValidateTransaction(tx, utxoSet) {
			body.Transactions.Insert(tx)

			receipt := &TxReceipt{
				TxHash:        tx.Hash(),
				BlockNumber:   header.BlockNumber,
				GasUsed:       gasNeeded,
				Status:        1,
				CumulativeGas: cumulativeGas + gasNeeded,
			}

			body.Receipts = append(body.Receipts, receipt)
			cumulativeGas += gasNeeded
			UpdateUTXOSet(tx, utxoSet)
		}
	}

	header.GasUsed = cumulativeGas
	header.MerkleRoot = body.Transactions.GenerateRootHash()
	header.StateRoot = calculateStateRoot(utxoSet)
	header.ReceiptsRoot = calculateReceiptsRoot(body.Receipts)

	return Block{
		Header:               header,
		Body:                 body,
		CumulativeDifficulty: previousBlock.CumulativeDifficulty + uint64(difficulty),
	}
}

// Helper methods for API and utilities
func (b *Block) Hash() string {
	if b.hash != "" {
		return b.hash
	}

	header := b.Header
	data := fmt.Sprintf("%d%d%s%d%s%s%s%d%d",
		header.Version,
		header.BlockNumber,
		header.PreviousHash,
		header.Timestamp,
		header.MerkleRoot,
		header.StateRoot,
		header.ReceiptsRoot,
		header.Nonce,
		header.GasUsed,
	)

	hash := sha256.Sum256([]byte(data))
	b.hash = hex.EncodeToString(hash[:])
	return b.hash
}

// API helper methods
func (b *Block) Number() uint64 {
	return b.Header.BlockNumber
}

func (b *Block) Time() time.Time {
	return time.Unix(b.Header.Timestamp, 0)
}

func (b *Block) GasInfo() (uint64, uint64) {
	return b.Header.GasUsed, b.Header.GasLimit
}

func (b *Block) Size() uint64 {
	if b.size == 0 {
		b.size = calculateBlockSize(b)
	}
	return b.size
}

func (b *Block) TransactionCount() uint32 {
	if b.numTx == 0 {
		b.numTx = uint32(b.Body.Transactions.Len())
	}
	return b.numTx
}

// Create an immutable copy of the block header
func (h *BlockHeader) Copy() *BlockHeader {
	cpy := *h
	if len(h.ExtraData) > 0 {
		cpy.ExtraData = make([]byte, len(h.ExtraData))
		copy(cpy.ExtraData, h.ExtraData)
	}
	return &cpy
}

func calculateDynamicGasLimit(previousBlock *Block) uint64 {
	calculator := gas.NewBlockGasCalculator(BaseGasLimit)

	if previousBlock == nil {
		return BaseGasLimit
	}

	return calculator.CalculateDynamicGasLimit(
		previousBlock.Header.GasUsed,
		previousBlock.Header.GasLimit,
	)
}

func calculateDynamicBlockSize(txCount int) int {
	calculator := gas.NewBlockGasCalculator(BaseGasLimit)
	return int(calculator.CalculateBlockSize(txCount))
}

func calculateGas(tx Transaction) uint64 {
	// Base cost for any transaction
	gasUsed := uint64(21000)

	// Add gas for data
	data := tx.GetData()
	if len(data) > 0 {
		gasUsed += uint64(len(data)) * 16 // 16 gas per byte of data
	}

	// Add gas for signature verification
	gasUsed += 2000

	return gasUsed
}

func calculateStateRoot(utxoSet map[string]UTXO) string {
	if len(utxoSet) == 0 {
		return "0x0000000000000000000000000000000000000000000000000000000000000000"
	}

	// Create merkle tree from UTXO states
	utxoHashes := make([]string, 0, len(utxoSet))
	for _, utxo := range utxoSet {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%v", utxo)))
		utxoHashes = append(utxoHashes, hex.EncodeToString(hash[:]))
	}

	return calculateMerkleRoot(utxoHashes)
}

func calculateReceiptsRoot(receipts []*TxReceipt) string {
	if len(receipts) == 0 {
		return "0x0000000000000000000000000000000000000000000000000000000000000000"
	}

	// Create merkle tree from receipt hashes
	receiptHashes := make([]string, 0, len(receipts))
	for _, receipt := range receipts {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%v", receipt)))
		receiptHashes = append(receiptHashes, hex.EncodeToString(hash[:]))
	}

	return calculateMerkleRoot(receiptHashes)
}

// Helper function to calculate merkle root
func calculateMerkleRoot(hashes []string) string {
	if len(hashes) == 0 {
		return "0x0000000000000000000000000000000000000000000000000000000000000000"
	}

	for len(hashes) > 1 {
		if len(hashes)%2 != 0 {
			hashes = append(hashes, hashes[len(hashes)-1])
		}

		var temp []string
		for i := 0; i < len(hashes); i += 2 {
			combined := hashes[i] + hashes[i+1]
			hash := sha256.Sum256([]byte(combined))
			temp = append(temp, hex.EncodeToString(hash[:]))
		}
		hashes = temp
	}

	return "0x" + hashes[0]
}

// Add this function
func MineBlock(block *Block, previousBlock Block, stakePool *StakePool, maxAttempts int, peerHost host.Host) error {
	// Start with nonce 0
	block.Header.Nonce = 0

	// Try up to maxAttempts times
	for i := 0; i < maxAttempts; i++ {
		// Calculate hash with current nonce
		hash := block.Hash()

		// Check if hash meets difficulty requirement
		if isHashValid(hash, block.Header.Difficulty) {
			block.hash = hash
			return nil
		}

		block.Header.Nonce++
	}

	return fmt.Errorf("failed to mine block after %d attempts", maxAttempts)
}

// Add helper function
func isHashValid(hash string, difficulty uint32) bool {
	prefix := strings.Repeat("0", int(difficulty))
	return strings.HasPrefix(hash, prefix)
}

// Add this function
func GenesisBlock() Block {
	header := &BlockHeader{
		Version:      1,
		BlockNumber:  0,
		PreviousHash: "0x00000000000000000000000000000000",
		Timestamp:    time.Now().Unix(),
		Difficulty:   1,
		GasLimit:     BaseGasLimit,
	}

	body := &BlockBody{
		Transactions: NewPatriciaTrie(),
		Receipts:     make([]*TxReceipt, 0),
	}

	return Block{
		Header: header,
		Body:   body,
	}
}

// Add this method
func (b *Block) CreateCheckpoint() *Checkpoint {
	return &Checkpoint{
		Height:    uint64(b.Header.BlockNumber),
		Hash:      b.Hash(),
		StateRoot: b.Header.StateRoot,
		UTXORoot:  b.Header.StateRoot, // Using StateRoot as UTXORoot for now
		Timestamp: b.Header.Timestamp,
	}
}

// calculateBlockSize returns the approximate size of the block in bytes
func calculateBlockSize(b *Block) uint64 {
	size := uint64(0)
	// Add header size
	size += uint64(len(b.Header.PreviousHash) + len(b.Hash()) + 16) // 16 for timestamp and nonce
	// Add base block size
	size += EmptyBlockSize
	return size
}
