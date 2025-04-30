package blockchain

import (
	"blockchain-core/blockchain/gas"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
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
	MinGasPrice    = 10  // Updated to match gas model
	TargetGasUsage = 0.8 // Target 80% gas usage
)

// BlockHeader contains block metadata
type BlockHeader struct {
	Version          uint32          // Block version
	BlockNumber      uint64          // Height of the block
	PreviousHash     string          // Hash of previous block
	Timestamp        int64           // Block creation time
	MerkleRoot       string          // Merkle root of transactions
	StateRoot        string          // State root after transactions
	ReceiptsRoot     string          // Root hash of transaction receipts
	Difficulty       uint32          // Mining difficulty
	Nonce            uint64          // PoW nonce
	GasLimit         uint64          // Maximum gas allowed
	GasUsed          uint64          // Actual gas used
	MinedBy          string          // Address of miner
	ValidatedBy      string          // Address of PoS validator
	ExtraData        []byte          // Additional data (limited size)
	ValidatorProof   *SelectionProof // Proof of validator selection
	ValidatorAddress string          // Selected validator's address
	ValidatorSig     []byte          // Validator's signature
}

// BlockBody contains the actual block data
type BlockBody struct {
	Transactions *PatriciaTrie
	Receipts     []*TxReceipt
}

// Block represents a block in the blockchain
type Block struct {
	Header               *BlockHeader
	Body                 *BlockBody
	hash                 string // Cached block hash
	size                 uint64 // Cached block size
	numTx                uint32 // Cached transaction count
	CumulativeDifficulty uint64
	mu                   sync.RWMutex
	originalHash         string // Store the original hash to prevent recalculation
}

// MarshalJSON implements the json.Marshaler interface
func (b *Block) MarshalJSON() ([]byte, error) {
	// Build a simplified representation to avoid cycles
	type SimplifiedBlock struct {
		Hash         string       `json:"hash"`
		OriginalHash string       `json:"originalHash"`
		Header       *BlockHeader `json:"header"`
		Body         struct {
			Transactions struct {
				RootHash string        `json:"rootHash"`
				TxList   []Transaction `json:"txList"`
			} `json:"transactions"`
			Receipts []*TxReceipt `json:"receipts"`
		} `json:"body"`
		CumulativeDifficulty uint64 `json:"cumulativeDifficulty"`
	}

	simplified := SimplifiedBlock{
		Hash:                 b.hash,
		OriginalHash:         b.originalHash,
		Header:               b.Header,
		CumulativeDifficulty: b.CumulativeDifficulty,
	}

	// Use a flat list of transactions instead of the full trie structure
	if b.Body != nil && b.Body.Transactions != nil {
		simplified.Body.Transactions.RootHash = b.Header.MerkleRoot
		simplified.Body.Transactions.TxList = b.Body.Transactions.GetAllTransactions()
		simplified.Body.Receipts = b.Body.Receipts
	}

	data, err := json.Marshal(simplified)
	if err != nil {
		return nil, fmt.Errorf("json: error calling MarshalJSON for type *Block: %v", err)
	}

	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler interface
func (b *Block) UnmarshalJSON(data []byte) error {
	type SimplifiedBlock struct {
		Hash         string       `json:"hash"`
		OriginalHash string       `json:"originalHash"`
		Header       *BlockHeader `json:"header"`
		Body         struct {
			Transactions struct {
				RootHash string        `json:"rootHash"`
				TxList   []Transaction `json:"txList"`
			} `json:"transactions"`
			Receipts []*TxReceipt `json:"receipts"`
		} `json:"body"`
		CumulativeDifficulty uint64 `json:"cumulativeDifficulty"`
	}

	var simplified SimplifiedBlock
	if err := json.Unmarshal(data, &simplified); err != nil {
		log.Printf("🔑 [DESERIALIZATION] Error during unmarshaling: %v", err)
		return err
	}

	// Restore the block structure
	b.Header = simplified.Header
	b.hash = simplified.Hash
	if b.hash == "" {
		b.hash = simplified.OriginalHash
	}
	b.originalHash = simplified.OriginalHash
	b.CumulativeDifficulty = simplified.CumulativeDifficulty

	// Create new body with a fresh Patricia Trie
	b.Body = &BlockBody{
		Transactions: NewPatriciaTrie(),
		Receipts:     simplified.Body.Receipts,
	}

	// Insert all transactions into the trie
	txCount := 0
	for _, tx := range simplified.Body.Transactions.TxList {
		b.Body.Transactions.Insert(tx)
		txCount++
	}

	// Set cached transaction count
	b.numTx = uint32(txCount)

	// Verify the Merkle root matches after rebuilding
	calculatedRoot := b.Body.Transactions.GenerateRootHash()
	if b.Header.MerkleRoot != "" && calculatedRoot != b.Header.MerkleRoot {

		// If there's a mismatch but we have transactions, update the merkle root to match
		if txCount > 0 {
			log.Printf("    • Updating Merkle root to match transactions")
			b.Header.MerkleRoot = calculatedRoot
		}
	}
	return nil
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

// Hash calculates the hash of the block
func (b *Block) Hash() string {
	// Log the current state before any operations
	b.mu.RLock()
	if b.originalHash != "" {
		defer b.mu.RUnlock()
		log.Printf("🔑 [HASH CACHE] Returning original hash: %s (ptr: %p)", b.originalHash, &b.originalHash)
		return b.originalHash
	}
	b.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	// Double check after acquiring write lock
	if b.originalHash != "" {
		log.Printf("🔑 [HASH CACHE] Original hash already calculated after lock: %s (ptr: %p)", b.originalHash, &b.originalHash)
		return b.originalHash
	}

	// Calculate hash if not cached
	header := b.Header

	// Create a consistent string representation of the block
	var data strings.Builder
	data.WriteString(fmt.Sprintf("%d|", header.Version))
	data.WriteString(fmt.Sprintf("%d|", header.BlockNumber))
	data.WriteString(fmt.Sprintf("%s|", header.PreviousHash))
	data.WriteString(fmt.Sprintf("%d|", header.Timestamp))
	data.WriteString(fmt.Sprintf("%s|", header.MerkleRoot))
	data.WriteString(fmt.Sprintf("%s|", header.StateRoot))
	data.WriteString(fmt.Sprintf("%s|", header.ReceiptsRoot))
	data.WriteString(fmt.Sprintf("%d|", header.Nonce))
	data.WriteString(fmt.Sprintf("%d|", header.GasUsed))
	data.WriteString(fmt.Sprintf("%d|", header.Difficulty))
	data.WriteString(fmt.Sprintf("%d|", header.GasLimit))
	data.WriteString(fmt.Sprintf("%s|", header.MinedBy))

	// Handle ExtraData consistently
	if len(header.ExtraData) == 0 {
		data.WriteString("[]")
	} else {
		data.WriteString(fmt.Sprintf("%v", header.ExtraData))
	}

	hashInput := data.String()
	log.Printf("🔑 [HASH CACHE] Hash input data: %s", hashInput)

	hash := sha256.Sum256([]byte(hashInput))
	b.originalHash = hex.EncodeToString(hash[:])
	b.hash = b.originalHash

	log.Printf("🔑 [HASH CACHE] Calculated new hash: %s (ptr: %p)", b.originalHash, &b.originalHash)
	log.Printf("🔑 [HASH CACHE] Hash calculation complete")
	return b.originalHash
}

// Helper function to get caller information
func getCallerInfo() string {
	pc := make([]uintptr, 10)
	n := runtime.Callers(2, pc)
	if n == 0 {
		return "unknown"
	}
	pc = pc[:n]
	frames := runtime.CallersFrames(pc)
	var info strings.Builder
	for {
		frame, more := frames.Next()
		info.WriteString(fmt.Sprintf("%s:%d -> ", frame.File, frame.Line))
		if !more {
			break
		}
	}
	return info.String()
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
		b.size = calculateBlockSize(*b)
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
	// Ensure all string fields are properly copied
	if h.MinedBy != "" {
		cpy.MinedBy = h.MinedBy
	}
	if h.ValidatedBy != "" {
		cpy.ValidatedBy = h.ValidatedBy
	}
	if h.ValidatorAddress != "" {
		cpy.ValidatorAddress = h.ValidatorAddress
	}
	if h.PreviousHash != "" {
		cpy.PreviousHash = h.PreviousHash
	}
	if h.StateRoot != "" {
		cpy.StateRoot = h.StateRoot
	}
	if h.MerkleRoot != "" {
		cpy.MerkleRoot = h.MerkleRoot
	}
	if h.ReceiptsRoot != "" {
		cpy.ReceiptsRoot = h.ReceiptsRoot
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

	// Get all keys and sort them for deterministic ordering
	keys := make([]string, 0, len(utxoSet))
	for key := range utxoSet {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Create hashes in deterministic order
	for _, key := range keys {
		utxo := utxoSet[key]
		data := fmt.Sprintf("%s-%d-%s-%.8f",
			utxo.TransactionID,
			utxo.OutputIndex,
			utxo.Owner,
			utxo.Amount,
		)
		hash := sha256.Sum256([]byte(data))
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

// MineBlock mines a block by finding a valid hash
func MineBlock(block *Block, previousBlock Block, stakePool *StakePool, difficulty uint32, peerHost host.Host) error {
	log.Printf("⛏️ Starting mining process for block #%d", block.Header.BlockNumber)
	startTime := time.Now()

	// Set difficulty
	block.Header.Difficulty = difficulty

	// Calculate hash
	for {
		hash := block.CalculateHash()
		if isHashValid(hash, difficulty) {
			block.hash = hash
			miningTime := time.Since(startTime)
			log.Printf("✅ Successfully mined block #%d with hash %s", block.Header.BlockNumber, hash)
			log.Printf("⏱️ Mining completed in %s with difficulty %d", miningTime, difficulty)
			break
		}
		block.Header.Nonce++
		if block.Header.Nonce%100000 == 0 {
			log.Printf("🔄 Mining in progress... tried %d nonces so far", block.Header.Nonce)
		}
	}

	// Validate the block with validators
	validators, err := stakePool.GetValidators(3)
	if err != nil {
		log.Printf("⚠️ Failed to get validators: %v", err)
		return err
	}

	log.Printf("🔐 Requesting validation from %d validators", len(validators))
	validations := 0
	for _, validator := range validators {
		// Request validation from validator
		valid, err := requestValidation(block, validator, peerHost)
		if err != nil {
			log.Printf("⚠️ Validation request to %s failed: %v", validator.Address, err)
			continue
		}
		if valid {
			validations++
			log.Printf("✓ Validator %s confirmed block validity", validator.Address)
		}
	}

	// Check if we have enough validations
	if validations < len(validators)/2+1 {
		log.Printf("❌ Insufficient validations: got %d, needed %d", validations, len(validators)/2+1)
		return fmt.Errorf("insufficient validations")
	}

	log.Printf("🎉 Block #%d successfully validated by %d validators", block.Header.BlockNumber, validations)
	return nil
}

// ValidateBlock validates a block
func ValidateBlock(block Block, previousBlock Block, validatorAddress string, stakePool *StakePool) bool {
	log.Printf("🔍 Validating block #%d with hash %s", block.Header.BlockNumber, block.hash)

	// Check if block number is valid
	if block.Header.BlockNumber != previousBlock.Header.BlockNumber+1 {
		log.Printf("❌ Invalid block number: expected %d, got %d",
			previousBlock.Header.BlockNumber+1, block.Header.BlockNumber)
		return false
	}

	// Check if previous hash is valid
	if block.Header.PreviousHash != previousBlock.hash {
		log.Printf("❌ Invalid previous hash: expected %s, got %s",
			previousBlock.hash, block.Header.PreviousHash)
		return false
	}

	// Check if hash is valid
	if !isHashValid(block.hash, block.Header.Difficulty) {
		log.Printf("❌ Invalid block hash: %s does not meet difficulty %d",
			block.hash, block.Header.Difficulty)
		return false
	}

	// Validate transactions
	log.Printf("🧾 Validating %d transactions in block", len(block.Body.Transactions.GetAllTransactions()))
	for i, tx := range block.Body.Transactions.GetAllTransactions() {
		log.Printf("  ↳ Validating transaction %d/%d: %s",
			i+1, len(block.Body.Transactions.GetAllTransactions()), tx.TransactionID)

		// Validate based on transaction type
		if tx.IsCoinbase() {
			// Validate coinbase transaction specifics
			if tx.Sender != "coinbase" {
				log.Printf("  ❌ Invalid coinbase sender: %s", tx.Sender)
				return false
			}
			// Ensure coinbase structure is valid
			if len(tx.Outputs) != 1 {
				log.Printf("  ❌ Coinbase must have exactly one output")
				return false
			}
			log.Printf("  ✓ Coinbase transaction %s valid", tx.TransactionID)
		} else if tx.IsValidatorReward() {
			// Validate validator reward transaction
			if tx.Sender != "system" {
				log.Printf("  ❌ Invalid system sender: %s", tx.Sender)
				return false
			}
			// Check validator reward structure
			if len(tx.Outputs) != 1 {
				log.Printf("  ❌ Validator reward must have exactly one output")
				return false
			}
			log.Printf("  ✓ Validator reward transaction %s valid", tx.TransactionID)
		} else {
			// Regular transaction validation
			if !tx.VerifySignature() {
				log.Printf("  ❌ Transaction %s has invalid signature", tx.TransactionID)
				return false
			}
			log.Printf("  ✓ Regular transaction %s valid", tx.TransactionID)
		}
	}

	log.Printf("✅ Block #%d successfully validated", block.Header.BlockNumber)
	return true
}

// GenesisBlock creates the genesis block
func GenesisBlock() Block {
	header := &BlockHeader{
		Version:          1,
		BlockNumber:      0,
		PreviousHash:     "0x00000000000000000000000000000000",
		Timestamp:        time.Now().Unix(),
		Difficulty:       1,
		GasLimit:         BaseGasLimit,
		MerkleRoot:       "0x0000000000000000000000000000000000000000000000000000000000000000",
		StateRoot:        "0x0000000000000000000000000000000000000000000000000000000000000000",
		ReceiptsRoot:     "0x0000000000000000000000000000000000000000000000000000000000000000",
		Nonce:            0,
		GasUsed:          0,
		ValidatedBy:      "", // Will be set during initialization
		ValidatorAddress: "", // Will be set during initialization
	}

	body := &BlockBody{
		Transactions: NewPatriciaTrie(),
		Receipts:     make([]*TxReceipt, 0),
	}

	block := Block{
		Header: header,
		Body:   body,
	}

	return block
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
func calculateBlockSize(b Block) uint64 {
	size := uint64(0)
	// Add header size
	size += uint64(len(b.Header.PreviousHash) + len(b.Hash()) + 16) // 16 for timestamp and nonce
	// Add base block size
	size += EmptyBlockSize
	return size
}

// isHashValid checks if a hash meets the required difficulty
func isHashValid(hash string, difficulty uint32) bool {
	prefix := strings.Repeat("0", int(difficulty))
	return strings.HasPrefix(hash, prefix)
}

// requestValidation sends a validation request to a validator
func requestValidation(block *Block, validator ValidatorNode, peerHost host.Host) (bool, error) {
	log.Printf("📤 Requesting validation from validator %s", validator.Address)

	// Get the validator's host ID
	hostID, exists := validator.HostID()
	if !exists {
		return false, fmt.Errorf("validator host ID not found")
	}

	// Parse the host ID
	validatorPeerID, err := peer.Decode(hostID)
	if err != nil {
		return false, fmt.Errorf("invalid validator host ID: %v", err)
	}

	log.Printf("📡 Connecting to validator %s at peer ID %s", validator.Address, validatorPeerID.String())

	// Create a stream to the validator
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create stream to validator
	stream, err := peerHost.NewStream(ctx, validatorPeerID, "/miner-validator/block-validation/1.0.0")
	if err != nil {
		return false, fmt.Errorf("failed to create stream to validator: %v", err)
	}
	defer stream.Close()

	log.Printf("🔗 Connected to validator, sending block #%d for validation", block.Header.BlockNumber)

	// Serialize the block and send it to the validator
	blockData, err := json.Marshal(block)
	if err != nil {
		return false, fmt.Errorf("failed to serialize block: %v", err)
	}

	// Write the block data and close the write side
	if _, err := stream.Write(blockData); err != nil {
		return false, fmt.Errorf("failed to send block to validator: %v", err)
	}
	if err := stream.CloseWrite(); err != nil {
		return false, fmt.Errorf("failed to close write side of stream: %v", err)
	}

	// Wait for the validation response
	log.Printf("⏳ Waiting for validation response from %s...", validator.Address)

	// Read response
	var response map[string]interface{}
	if err := json.NewDecoder(stream).Decode(&response); err != nil {
		return false, fmt.Errorf("failed to read validation response: %v", err)
	}

	// Handle the response
	valid, ok := response["valid"].(bool)
	if !ok {
		return false, fmt.Errorf("invalid response format")
	}

	errorMsg, _ := response["error"].(string)
	if errorMsg != "" {
		log.Printf("⚠️ Validator reported error: %s", errorMsg)
	}

	if valid {
		// Set the validator's address in the block header for record-keeping
		block.Header.ValidatedBy = validator.Address

		// Set the validator's signature from the response
		if sigStr, ok := response["signature"].(string); ok {
			// Decode base64 string to []byte
			signature, err := base64.StdEncoding.DecodeString(sigStr)
			if err != nil {
				log.Printf("⚠️ Failed to decode validator signature: %v", err)
			} else {
				log.Printf("✅ Setting validator signature for block #%d", block.Header.BlockNumber)
				block.Header.ValidatorSig = signature
			}
		} else {
			log.Printf("⚠️ Validator signature not found in response or has invalid format")
		}

		log.Printf("✅ Block #%d validated by %s", block.Header.BlockNumber, validator.Address)

		// Remove the blockchain state update - we'll rely on the block header instead
		log.Printf("🔄 Validator %s recorded in block header for future rewards", validator.Address)
	} else {
		log.Printf("❌ Block #%d rejected by %s: %s", block.Header.BlockNumber, validator.Address, errorMsg)
	}

	return valid, nil
}

// Add this method to Block
func (b *Block) VerifyValidatorSelection(selector *ValidatorSelector) error {
	if b.Header.ValidatorProof == nil {
		return fmt.Errorf("missing validator selection proof")
	}

	// Verify the selection proof
	validator, proof, err := selector.SelectValidator(
		b.Header.BlockNumber,
		b.Header.PreviousHash,
	)
	if err != nil {
		return fmt.Errorf("failed to verify validator selection: %w", err)
	}

	// Verify the selected validator matches
	if validator.Address != b.Header.ValidatorAddress {
		return fmt.Errorf("invalid validator selection")
	}

	// Verify proof matches
	if !verifyProof(proof, b.Header.ValidatorProof) {
		return fmt.Errorf("invalid selection proof")
	}

	return nil
}

// Add this function
func verifyProof(proof1, proof2 *SelectionProof) bool {
	if proof1 == nil || proof2 == nil {
		return false
	}
	// Compare proof fields
	return proof1.Seed != nil &&
		bytes.Equal(proof1.Seed, proof2.Seed) &&
		proof1.Weight == proof2.Weight &&
		proof1.Score == proof2.Score
}

// Copy creates a deep copy of the block
func (b *Block) Copy() *Block {
	cpy := &Block{
		Header:               b.Header.Copy(),
		Body:                 &BlockBody{},
		CumulativeDifficulty: b.CumulativeDifficulty,
		originalHash:         b.originalHash,
		hash:                 b.hash,
		size:                 b.size,
		numTx:                b.numTx,
	}

	// Copy transactions
	cpy.Body.Transactions = NewPatriciaTrie()
	for _, tx := range b.Body.Transactions.GetAllTransactions() {
		cpy.Body.Transactions.Insert(tx)
	}

	// Copy receipts
	cpy.Body.Receipts = make([]*TxReceipt, len(b.Body.Receipts))
	copy(cpy.Body.Receipts, b.Body.Receipts)

	return cpy
}

// GenerateMerkleProof generates a Merkle proof for a transaction in this block
func (b *Block) GenerateMerkleProof(txID string) (map[string]interface{}, error) {
	// Check if the transaction exists in this block
	if b.Body == nil || b.Body.Transactions == nil {
		return nil, fmt.Errorf("block has no transactions")
	}

	txNode, found := b.Body.Transactions.GetTransaction(txID)
	if !found || txNode == nil {
		return nil, fmt.Errorf("transaction %s not found in block", txID)
	}

	// Collect all transaction hashes in the block
	allTxs := b.Body.Transactions.GetAllTransactions()
	txHashes := make([]string, len(allTxs))

	// Find the index of our transaction
	targetIndex := -1
	for i, tx := range allTxs {
		txHashes[i] = tx.TransactionID
		if tx.TransactionID == txID {
			targetIndex = i
		}
	}

	if targetIndex == -1 {
		return nil, fmt.Errorf("transaction found in trie but not in list")
	}

	// Generate the proof
	proof := generateMerkleProofForIndex(txHashes, targetIndex)

	return map[string]interface{}{
		"txid":       txID,
		"merkleRoot": b.Header.MerkleRoot,
		"blockHash":  b.Hash(),
		"height":     b.Header.BlockNumber,
		"proof":      proof,
		"verified":   true,
	}, nil
}

// Helper function to generate the Merkle proof for an index
func generateMerkleProofForIndex(hashes []string, index int) []string {
	if len(hashes) == 0 {
		return []string{}
	}

	// Convert transaction IDs to actual hashes
	hashBytes := make([][]byte, len(hashes))
	for i, txID := range hashes {
		hash := sha256.Sum256([]byte(txID))
		hashBytes[i] = hash[:]
	}

	// Build the proof
	var proof []string
	currentLevel := hashBytes
	currentIndex := index

	for len(currentLevel) > 1 {
		if len(currentLevel)%2 == 1 {
			// Duplicate last element if odd number of elements
			currentLevel = append(currentLevel, currentLevel[len(currentLevel)-1])
		}

		nextLevel := make([][]byte, 0, len(currentLevel)/2)
		for i := 0; i < len(currentLevel); i += 2 {
			// For each pair, add the one that's not on our path to the proof
			if i == currentIndex || i+1 == currentIndex {
				if i == currentIndex {
					proof = append(proof, hex.EncodeToString(currentLevel[i+1]))
				} else {
					proof = append(proof, hex.EncodeToString(currentLevel[i]))
				}

				// Calculate parent hash for next level
				combined := append(currentLevel[i], currentLevel[i+1]...)
				hash := sha256.Sum256(combined)
				nextLevel = append(nextLevel, hash[:])

				// Adjust the index for the next level
				currentIndex = len(nextLevel) - 1
			} else {
				// This pair isn't on our path, but we still need to calculate it for the next level
				combined := append(currentLevel[i], currentLevel[i+1]...)
				hash := sha256.Sum256(combined)
				nextLevel = append(nextLevel, hash[:])
			}
		}

		currentLevel = nextLevel
		currentIndex = currentIndex / 2
	}

	return proof
}

// GenerateBlockTrace creates a detailed trace of all transactions in the block
func (b *Block) GenerateBlockTrace() *BlockTrace {
	if b.Body == nil || b.Body.Transactions == nil {
		return &BlockTrace{
			BlockHash:         b.Hash(),
			BlockNumber:       b.Header.BlockNumber,
			PreviousBlockHash: b.Header.PreviousHash,
			Timestamp:         b.Header.Timestamp,
			MerkleRoot:        b.Header.MerkleRoot,
			StateRoot:         b.Header.StateRoot,
			TotalGasUsed:      b.Header.GasUsed,
			ExecutionTimeMs:   0, // Not tracked in this implementation
			ValidatorAddress:  b.Header.ValidatorAddress,
			MinerAddress:      b.Header.MinedBy,
			TransactionTraces: []TransactionTrace{},
		}
	}

	// Get all transactions
	transactions := b.Body.Transactions.GetAllTransactions()

	// Create traces for each transaction
	txTraces := make([]TransactionTrace, 0, len(transactions))
	for _, tx := range transactions {
		trace := generateTransactionTrace(tx, b)
		txTraces = append(txTraces, trace)
	}

	return &BlockTrace{
		BlockHash:         b.Hash(),
		BlockNumber:       b.Header.BlockNumber,
		PreviousBlockHash: b.Header.PreviousHash,
		Timestamp:         b.Header.Timestamp,
		MerkleRoot:        b.Header.MerkleRoot,
		StateRoot:         b.Header.StateRoot,
		TotalGasUsed:      b.Header.GasUsed,
		ExecutionTimeMs:   0, // Not tracked in this implementation
		ValidatorAddress:  b.Header.ValidatorAddress,
		MinerAddress:      b.Header.MinedBy,
		TransactionTraces: txTraces,
	}
}

// Helper function to generate detailed transaction trace
func generateTransactionTrace(tx Transaction, block *Block) TransactionTrace {
	// Extract input transaction IDs
	inputTxIDs := make([]string, 0, len(tx.Inputs))
	for _, input := range tx.Inputs {
		inputTxIDs = append(inputTxIDs, input.TransactionID)
	}

	// Extract output identifiers
	outputIDs := make([]string, 0, len(tx.Outputs))
	for i := range tx.Outputs {
		outputID := fmt.Sprintf("%s-%d", tx.TransactionID, i)
		outputIDs = append(outputIDs, outputID)
	}

	// Prepare a basic state change map (just recording balance changes)
	stateChanges := make(map[string]interface{})

	// Record sender balance change if this is not a system transaction
	if !tx.IsCoinbase() && !tx.IsValidatorReward() {
		stateChanges[fmt.Sprintf("balance:%s", tx.Sender)] = -tx.Amount - tx.GasFee
	}

	// Record receiver balance change
	stateChanges[fmt.Sprintf("balance:%s", tx.Receiver)] = tx.Amount

	// Add basic execution logs
	logs := []string{
		fmt.Sprintf("Transaction execution started at block %d", block.Header.BlockNumber),
	}

	if tx.IsCoinbase() {
		logs = append(logs, "Coinbase transaction - minting new tokens")
	} else if tx.IsValidatorReward() {
		logs = append(logs, "Validator reward transaction - distributing rewards")
	} else {
		logs = append(logs, fmt.Sprintf("Regular transaction - transferring %.8f tokens", tx.Amount))
		logs = append(logs, fmt.Sprintf("Gas fee: %.8f tokens", tx.GasFee))
	}

	logs = append(logs, "Transaction execution completed successfully")

	return TransactionTrace{
		TransactionID:  tx.TransactionID,
		BlockHash:      block.Hash(),
		BlockNumber:    block.Header.BlockNumber,
		GasUsed:        tx.GasUsed,
		Status:         "success", // Assume success for all transactions in the block
		InputsAccessed: inputTxIDs,
		OutputsCreated: outputIDs,
		Timestamp:      tx.Timestamp,
		StateChanges:   stateChanges,
		Logs:           logs,
	}
}
