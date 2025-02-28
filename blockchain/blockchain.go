package blockchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"blockchain-core/blockchain/db"

	"github.com/libp2p/go-libp2p/core/host"
)

// chain of blocks are stored
type Blockchain struct {
	Chain       []Block
	Node        *Node
	mu          sync.RWMutex
	currentHash string
	utxoPool    *UTXOPool
	db          db.Database
	mempool     *Mempool
	p2pHost     host.Host
}

//intializes the blockchain with the genesis block

func InitialiseBlockchain(dbConfig *DatabaseConfig) *Blockchain {
	// Initialize database
	if dbConfig == nil {
		dbConfig = &DatabaseConfig{
			Type:      "leveldb",
			Path:      "blockchain_data",
			CacheSize: 256,
		}
	}

	// Initialize blockchain with database
	bc := &Blockchain{
		Chain:       []Block{},
		Node:        nil,
		mu:          sync.RWMutex{},
		currentHash: "",
		utxoPool:    nil,
		db:          initDB(dbConfig),
	}

	genesis := GenesisBlock()
	bc.Chain = append(bc.Chain, genesis)
	return bc
}

func initDB(config *DatabaseConfig) db.Database {
	log.Printf("🚀 Initializing Database:")
	log.Printf("   • Config Type: %s", config.Type)
	log.Printf("   • Config Path: %s", config.Path)

	opts := db.DefaultOptions()

	// Only override if values are provided
	if config.Type != "" {
		opts.Type = config.Type
	}
	if config.Path != "" {
		opts.Path = config.Path
	}
	if config.CacheSize > 0 {
		opts.CacheSize = config.CacheSize
	}
	if config.MaxOpenFiles > 0 {
		opts.MaxOpenFiles = config.MaxOpenFiles
	}

	log.Printf("📝 Final Database Options:")
	log.Printf("   • Type: %s", opts.Type)
	log.Printf("   • Path: %s", opts.Path)
	log.Printf("   • MaxOpenFiles: %d", opts.MaxOpenFiles)

	database, err := db.NewDatabase(opts)
	if err != nil {
		log.Printf("❌ Database initialization failed: %v", err)
		log.Fatalf("Failed to initialize database: %v", err)
	}
	log.Printf("✅ Database initialized successfully")
	return database
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
	newBlock := NewBlock(previousBlock, mempool, utxoSet, previousBlock.Header.Difficulty, validatorWallet)

	// Mine and validate the block
	err = MineBlock(&newBlock, previousBlock, stakePool, 10, peerHost)
	if err != nil {
		log.Printf("Failed to mine block: %v", err)
		return
	}

	// Validate and add the block to the chain
	if ValidateBlock(newBlock, previousBlock, validatorWallet, stakePool) {
		bc.Chain = append(bc.Chain, newBlock)
		log.Printf("Block %d added by validator Wallet=%s HostID=%s.\n",
			newBlock.Header.BlockNumber, validatorWallet, validatorHost)

		// Remove transactions from mempool
		for _, tx := range newBlock.Body.Transactions.GetAllTransactions() {
			mempool.RemoveTransaction(tx.TransactionID)
		}

		// Update account states
		updates := make(map[string]*AccountState)
		for _, tx := range newBlock.Body.Transactions.GetAllTransactions() {
			state, _ := bc.Node.accountManager.GetAccountState(tx.Sender)
			state.Nonce++
			updates[tx.Sender] = state
		}
		bc.Node.accountManager.BatchUpdateAccounts(updates)
	} else {
		log.Printf("Block %d validation failed.\n", newBlock.Header.BlockNumber)
	}
}

// GetLatestBlock retrieves the most recent block in the chain
func (bc *Blockchain) GetLatestBlock() Block {
	return bc.Chain[len(bc.Chain)-1]
}

// ValidateGenesisBlock ensures all nodes use the same genesis block
func ValidateGenesisBlock(bc *Blockchain, genesis Block) bool {
	storedChainHash := bc.Chain[0].hash
	storedGenesisHash := genesis.hash
	if storedChainHash == "" || storedGenesisHash == "" {
		// If either hash is not stored, calculate them
		calculatedChainHash := calculateHash(bc.Chain[0])
		calculatedGenesisHash := calculateHash(genesis)
		return calculatedChainHash == calculatedGenesisHash
	}
	return storedChainHash == storedGenesisHash
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
			if bc.Chain[j].Hash() == candidateBlock.Hash() {
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
		if segment[i].Header.PreviousHash != segment[i-1].Hash() {
			return false
		}

		// Validate block numbers
		if segment[i].Header.BlockNumber != segment[i-1].Header.BlockNumber+1 {
			return false
		}

		// Validate cumulative difficulty
		expectedDifficulty := segment[i-1].CumulativeDifficulty + uint64(segment[i].Header.Difficulty)
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
	if candidateChain[0].Header.BlockNumber == 0 {
		if candidateChain[0].Header.PreviousHash != "0x00000000000000000000000000000000" ||
			candidateChain[0].CumulativeDifficulty != uint64(candidateChain[0].Header.Difficulty) {
			return false
		}
	}

	// Validate the rest of the chain
	for i := 1; i < len(candidateChain); i++ {
		currentBlock := candidateChain[i]
		previousBlock := candidateChain[i-1]

		// Basic block validation
		if currentBlock.Header.BlockNumber != previousBlock.Header.BlockNumber+1 ||
			currentBlock.Header.PreviousHash != previousBlock.Hash() {
			return false
		}

		// Validate cumulative difficulty
		expectedCumulative := previousBlock.CumulativeDifficulty + uint64(currentBlock.Header.Difficulty)
		if currentBlock.CumulativeDifficulty != expectedCumulative {
			log.Printf("[Validation] Block %d has incorrect cumulative difficulty. Expected %d, got %d",
				currentBlock.Header.BlockNumber, expectedCumulative, currentBlock.CumulativeDifficulty)
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
	log.Printf("🔍 Performing comprehensive validation of block #%d", block.Header.BlockNumber)

	// Verify block hash
	if !bc.ValidateBlockHash(block) {
		log.Printf("❌ Invalid block hash for block #%d", block.Header.BlockNumber)
		return fmt.Errorf("invalid block hash")
	}
	log.Printf("✓ Block hash verification passed")

	// Verify previous hash
	prevBlock := bc.GetLatestBlock()
	if block.Header.PreviousHash != prevBlock.Hash() {
		log.Printf("❌ Previous hash mismatch: expected %s, got %s", prevBlock.Hash(), block.Header.PreviousHash)
		return fmt.Errorf("invalid previous hash")
	}
	log.Printf("✓ Previous hash verification passed")

	// Verify block number
	if block.Header.BlockNumber != prevBlock.Header.BlockNumber+1 {
		log.Printf("❌ Block number mismatch: expected %d, got %d", prevBlock.Header.BlockNumber+1, block.Header.BlockNumber)
		return fmt.Errorf("invalid block number")
	}
	log.Printf("✓ Block number verification passed")

	// Verify transactions
	log.Printf("🧾 Validating %d transactions in block", len(block.Body.Transactions.GetAllTransactions()))
	for _, tx := range block.Body.Transactions.GetAllTransactions() {
		if err := bc.ValidateTransaction(&tx); err != nil {
			log.Printf("❌ Transaction validation failed for tx %s: %v", tx.TransactionID, err)
			return fmt.Errorf("invalid transaction: %v", err)
		}
	}
	log.Printf("✓ All transactions successfully validated")

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
				Version:      block.Header.Version,
				BlockNumber:  block.Header.BlockNumber,
				PreviousHash: block.Header.PreviousHash,
				Timestamp:    block.Header.Timestamp,
				MerkleRoot:   block.Header.MerkleRoot,
				StateRoot:    block.Header.StateRoot,
				ReceiptsRoot: block.Header.ReceiptsRoot,
				Difficulty:   block.Header.Difficulty,
				Nonce:        block.Header.Nonce,
				GasLimit:     block.Header.GasLimit,
				GasUsed:      block.Header.GasUsed,
				MinedBy:      block.Header.MinedBy,
				ValidatedBy:  block.Header.ValidatedBy,
				ExtraData:    block.Header.ExtraData,
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
		Header: &BlockHeader{
			Version:      1,
			BlockNumber:  uint64(cp.Height),
			PreviousHash: "0x00000000000000000000000000000000",
			Timestamp:    cp.Timestamp,
			Difficulty:   0, // Will be updated when syncing remaining blocks
		},
		Body: &BlockBody{
			Transactions: NewPatriciaTrie(),
		},
		CumulativeDifficulty: 0,
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

// Add this function
func calculateHash(block Block) string {
	header := block.Header
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
	return hex.EncodeToString(hash[:])
}

// VerifyBalance checks if an address has sufficient balance
func (bc *Blockchain) VerifyBalance(address string, amount float64) bool {
	balance := bc.GetBalance(address)
	return balance >= amount
}

// GetBalance calculates balance from UTXO set
func (bc *Blockchain) GetBalance(address string) float64 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	balance := 0.0
	utxoSet := bc.utxoPool.GetUTXOsForAddress(address)

	for _, utxo := range utxoSet {
		balance += utxo.Amount
	}

	return balance
}

// GetNonce gets the next nonce for an address from UTXO set
func (bc *Blockchain) GetNonce(address string) uint64 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	// In UTXO model, nonce is tracked by transaction count
	return uint64(len(bc.utxoPool.GetUTXOsForAddress(address)))
}

// CalculateHash calculates block hash
func (b *Block) CalculateHash() string {
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
	return hex.EncodeToString(hash[:])
}

// InitialiseBlockchainWithStore initializes the blockchain with an existing store
func InitialiseBlockchainWithStore(store *Store) *Blockchain {
	log.Printf("🔄 Initializing blockchain with existing store")

	bc := &Blockchain{
		Chain:       []Block{},
		Node:        nil,
		mu:          sync.RWMutex{},
		currentHash: "",
		utxoPool:    nil,
		db:          store.db,
	}

	genesis := GenesisBlock()
	bc.Chain = append(bc.Chain, genesis)
	return bc
}

// Add GetDB method to access the database
func (bc *Blockchain) GetDB() db.Database {
	return bc.db
}

// ValidateBlockHash validates the hash of a block
func (bc *Blockchain) ValidateBlockHash(block *Block) bool {
	calculatedHash := calculateHash(*block)
	return calculatedHash == block.Hash()
}

// ValidateTransaction validates a transaction
func (bc *Blockchain) ValidateTransaction(tx *Transaction) error {
	// Implement transaction validation logic here
	return nil
}

// NewBlockchain initializes a new blockchain with the given data directory
func NewBlockchain(dataDir string) (*Blockchain, error) {
	log.Printf("🔄 Initializing blockchain database at %s", dataDir)

	// Initialize database
	dbConfig := &db.Config{
		Path: dataDir,
	}
	database, err := db.NewLevelDB(dbConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %v", err)
	}

	// Create a store with the database
	store := &Store{
		db: database,
	}

	// Initialize blockchain with the store
	bc := InitialiseBlockchainWithStore(store)
	log.Printf("✅ Blockchain initialized with genesis block")

	return bc, nil
}

// MineBlock mines a new block with the provided miner address
func (bc *Blockchain) MineBlock(minerAddress string) (*Block, error) {
	log.Printf("🔄 Starting mining process for miner %s", minerAddress)

	// Get the latest block
	bc.mu.RLock()
	previousBlock := bc.GetLatestBlock()
	bc.mu.RUnlock()

	// Calculate appropriate difficulty
	difficulty := bc.calculateDifficulty(previousBlock)
	log.Printf("🎯 Mining with difficulty: %d", difficulty)

	// Get pending transactions from mempool
	var pendingTxs []Transaction
	if bc.mempool != nil {
		pendingTxs = bc.mempool.GetTransactions()
		log.Printf("📥 Retrieved %d transactions from mempool for new block", len(pendingTxs))
	} else {
		log.Printf("⚠️ No mempool available, mining empty block")
		pendingTxs = []Transaction{}
	}

	// Create transaction trie
	txTrie := NewPatriciaTrie()
	for _, tx := range pendingTxs {
		txTrie.Insert(tx)
	}

	// Create a new block
	newBlock := Block{
		Header: &BlockHeader{
			Version:      1,
			BlockNumber:  previousBlock.Header.BlockNumber + 1,
			PreviousHash: previousBlock.Hash(),
			Timestamp:    time.Now().Unix(),
			Difficulty:   difficulty,
			GasLimit:     BaseGasLimit,
			MinedBy:      minerAddress,
		},
		Body: &BlockBody{
			Transactions: txTrie,
			Receipts:     make([]*TxReceipt, 0),
		},
		CumulativeDifficulty: previousBlock.CumulativeDifficulty + uint64(difficulty),
	}

	// Mine the block using the existing MineBlock function from block.go
	stakePool := NewStakePool()
	log.Printf("⛏️ Mining block #%d - searching for valid hash...", newBlock.Header.BlockNumber)
	startTime := time.Now()

	if err := MineBlock(&newBlock, previousBlock, stakePool, difficulty, bc.p2pHost); err != nil {
		log.Printf("❌ Mining failed: %v", err)
		return nil, err
	}

	miningTime := time.Since(startTime)
	log.Printf("✅ Successfully mined block #%d with hash %s",
		newBlock.Header.BlockNumber, newBlock.Hash())
	log.Printf("⏱️ Mining completed in %s", miningTime)

	// Add the block to the chain
	bc.mu.Lock()
	bc.Chain = append(bc.Chain, newBlock)
	bc.currentHash = newBlock.Hash()
	bc.mu.Unlock()

	// Save the block to the database
	if err := bc.saveBlock(newBlock); err != nil {
		log.Printf("⚠️ Warning: Failed to save block to database: %v", err)
	}

	// Remove the transactions from the mempool
	if bc.mempool != nil {
		for _, tx := range pendingTxs {
			bc.mempool.RemoveTransaction(tx.TransactionID)
		}
		log.Printf("🧹 Removed %d processed transactions from mempool", len(pendingTxs))
	}

	// Update UTXO set if available
	if bc.utxoPool != nil {
		bc.updateUTXOSet(newBlock)
	}

	// Calculate and log mining reward
	reward := calculateBlockReward(newBlock)
	log.Printf("💰 Mining reward of %.8f tokens sent to %s", reward, minerAddress)

	return &newBlock, nil
}

// Add this method to the Blockchain struct
// updateUTXOSet updates the UTXO set with the transactions in the block
func (bc *Blockchain) updateUTXOSet(block Block) {
	if bc.utxoPool == nil {
		return
	}

	// Process all transactions in the block
	for _, tx := range block.Body.Transactions.GetAllTransactions() {
		bc.utxoPool.AddUTXO(&tx, block.Header.BlockNumber)
	}
}

// Add the calculateDifficulty method to the Blockchain struct
// calculateDifficulty calculates the mining difficulty based on previous blocks
func (bc *Blockchain) calculateDifficulty(previousBlock Block) uint32 {
	// For simplicity, use a fixed difficulty for now
	// In a real implementation, this would adjust based on block times
	return previousBlock.Header.Difficulty
}

// Add the saveBlock method to the Blockchain struct
// saveBlock saves a block to the database
func (bc *Blockchain) saveBlock(block Block) error {
	// If we have a store, use it to save the block
	if bc.db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Convert block to bytes
	blockData, err := block.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize block: %v", err)
	}

	// Save block by hash
	blockKey := []byte("block_" + block.Hash())
	if err := bc.db.Put(blockKey, blockData); err != nil {
		return fmt.Errorf("failed to save block: %v", err)
	}

	// Update latest block pointer
	if err := bc.db.Put([]byte("latest_block"), []byte(block.Hash())); err != nil {
		return fmt.Errorf("failed to update latest block: %v", err)
	}

	log.Printf("📦 Block #%d saved to database", block.Header.BlockNumber)
	return nil
}

// Add this method to the Block struct
// Serialize converts a block to bytes
func (b Block) Serialize() ([]byte, error) {
	return json.Marshal(b)
}
