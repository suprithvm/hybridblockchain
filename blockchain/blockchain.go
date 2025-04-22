package blockchain

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	Chain            []Block
	Node             *Node
	mu               sync.RWMutex
	currentHash      string
	utxoPool         *UTXOPool
	db               db.Database
	mempool          *Mempool
	p2pHost          host.Host
	ctx              context.Context
	cancel           context.CancelFunc
	StakePool        *StakePool
	utxoSet          map[string]UTXO
	Validators       map[string]*Validator
	consensus        *ConsensusEngine
	rewardCalculator *RewardCalculator
	slashingManager  *SlashingManager
	communityPool    float64
	balances         map[string]float64
	validator        *Validator
	node             *Node
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

	// Initialize database
	db := initDB(dbConfig)
	if db == nil {
		log.Fatalf("❌ Failed to initialize database")
	}

	// Initialize blockchain with database
	bc := &Blockchain{
		Chain:         []Block{},
		Node:          nil,
		mu:            sync.RWMutex{},
		currentHash:   "",
		utxoPool:      NewUTXOPool(db), // Initialize UTXOPool with database
		db:            db,
		utxoSet:       make(map[string]UTXO),
		Validators:    make(map[string]*Validator),
		communityPool: 0,
		balances:      make(map[string]float64),
		p2pHost:       nil,
	}

	genesis := GenesisBlock()
	bc.Chain = append(bc.Chain, genesis)

	// Initialize stakePool after blockchain is created
	bc.StakePool = NewStakePool(bc)

	// Initialize validator selector and consensus engine
	selector := NewValidatorSelector(bc.StakePool)
	bc.consensus = NewConsensusEngine(bc, selector)

	// Initialize mempool
	bc.mempool = NewMempool(nil) // Will be set properly when node is created

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
func (bc *Blockchain) AddBlock(block *Block, mempool *Mempool, stakePool *StakePool, utxos map[string]UTXO, host host.Host) error {
	previousBlock := bc.GetLatestBlock()

	// Skip validator selection for miner nodes
	if bc.node != nil && !bc.node.IsInitializedValidator() {
		log.Printf("⛏️ Miner node skipping validator selection")
		return bc.addBlockWithoutValidation(block)
	}

	// Check if we have any validators in the stake pool
	if len(stakePool.Stakes) == 0 {
		log.Printf("⚠️ No validators available in stake pool, attempting to sync...")
		if host != nil {
			// Broadcast a request for validator information
			if err := bc.node.BroadcastValidatorRequest(); err != nil {
				log.Printf("Failed to broadcast validator request: %v", err)
			}
		}
		return fmt.Errorf("no validators available")
	}

	// Select validator
	validatorWallet, validatorHost, err := stakePool.SelectValidator(host)
	if err != nil {
		log.Printf("Failed to select validator: %v", err)
		return err
	}

	// Create the new block with validator wallet address
	newBlock := NewBlock(previousBlock, mempool, utxos, previousBlock.Header.Difficulty, validatorWallet)

	// Mine and validate the block
	err = MineBlock(&newBlock, previousBlock, stakePool, 10, host)
	if err != nil {
		log.Printf("Failed to mine block: %v", err)
		return err
	}

	// Start consensus round for the new block
	if err := bc.consensus.StartConsensusRound(&newBlock); err != nil {
		return fmt.Errorf("consensus failed: %w", err)
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

	return nil
}

func (bc *Blockchain) addBlockWithoutValidation(block *Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Verify block hash
	if block.Hash() != block.hash {
		return fmt.Errorf("invalid block hash")
	}

	// Verify block number
	if block.Header.BlockNumber != uint64(len(bc.Chain)) {
		return fmt.Errorf("invalid block number")
	}

	// Verify previous hash
	if len(bc.Chain) > 0 {
		if block.Header.PreviousHash != bc.Chain[len(bc.Chain)-1].Hash() {
			return fmt.Errorf("invalid previous hash")
		}
	}

	// Add block to chain
	bc.Chain = append(bc.Chain, *block)

	// Update latest block
	bc.currentHash = block.Hash()

	// Store block in database
	if bc.db != nil {
		blockData, err := block.Serialize()
		if err != nil {
			return fmt.Errorf("failed to serialize block: %v", err)
		}
		blockKey := db.CreateKey(db.BlockPrefix, []byte(block.Hash()))
		if err := bc.db.Put(blockKey, blockData); err != nil {
			return fmt.Errorf("failed to store block: %v", err)
		}
		if err := bc.db.Put([]byte("latest_block"), []byte(block.Hash())); err != nil {
			return fmt.Errorf("failed to update latest block: %v", err)
		}
	}

	log.Printf("✅ Block #%d added to chain", block.Header.BlockNumber)
	return nil
}

// GetLatestBlock returns the latest block in the chain
func (bc *Blockchain) GetLatestBlock() Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	// If we have blocks in memory, return the latest
	if len(bc.Chain) > 0 {
		return bc.Chain[len(bc.Chain)-1]
	}

	// If no blocks in memory, try to get from database
	if bc.db != nil {
		// Get latest block hash from database
		latestHash, err := bc.db.Get([]byte("latest_block"))
		if err == nil && len(latestHash) > 0 {
			// Get the block data
			blockKey := db.CreateKey(db.BlockPrefix, latestHash)
			blockData, err := bc.db.Get(blockKey)
			if err == nil {
				var block Block
				if err := json.Unmarshal(blockData, &block); err == nil {
					return block
				}
			}
		}
	}

	// If all else fails, return empty block
	return Block{}
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

	// Add block to chain
	bc.Chain = append(bc.Chain, *block)
	bc.currentHash = block.hash

	log.Printf("✅ Added block #%d to chain without validation", block.Header.BlockNumber)
	return nil
}

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

	var h uint64
	switch v := height.(type) {
	case int:
		h = uint64(v)
	case uint64:
		h = v
	default:
		return nil
	}

	// First check in memory
	if h < uint64(len(bc.Chain)) {
		block := &bc.Chain[h]
		// Recalculate hash to ensure consistency
		block.hash = block.Hash()
		return block
	}

	// If not in memory, try to get from database
	if bc.db != nil {
		// Get block by height from database
		heightBytes := []byte(fmt.Sprintf("%020d", h))
		key := db.CreateKey(db.BlockPrefix, heightBytes)
		blockData, err := bc.db.Get(key)
		if err == nil {
			var block Block
			if err := json.Unmarshal(blockData, &block); err == nil {
				// Recalculate hash to ensure consistency
				block.hash = block.CalculateHash()
				return &block
			}
		}
	}

	return nil
}

// GetHeight returns the current height of the blockchain
func (bc *Blockchain) GetHeight() uint64 {
	if len(bc.Chain) == 0 {
		return 0
	}
	return bc.Chain[len(bc.Chain)-1].Header.BlockNumber
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
	return bc.balances[address]
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
		Chain:         []Block{},
		Node:          nil,
		mu:            sync.RWMutex{},
		currentHash:   "",
		utxoPool:      NewUTXOPool(store.db), // Initialize UTXOPool with database
		db:            store.db,
		utxoSet:       make(map[string]UTXO),
		Validators:    make(map[string]*Validator),
		communityPool: 0,
		balances:      make(map[string]float64),
		p2pHost:       nil,
	}

	genesis := GenesisBlock()
	bc.Chain = append(bc.Chain, genesis)

	// Initialize stakePool after blockchain is created
	bc.StakePool = NewStakePool(bc)

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

	// Create a new block with safe state root handling
	newBlock := Block{
		Header: &BlockHeader{
			Version:      1,
			BlockNumber:  previousBlock.Header.BlockNumber + 1,
			PreviousHash: previousBlock.hash,
			Timestamp:    time.Now().Unix(),
			Difficulty:   difficulty,
			GasLimit:     BaseGasLimit,
			MinedBy:      minerAddress,
			MerkleRoot:   txTrie.GenerateRootHash(),
			StateRoot:    bc.GetState().RootHash,
			ReceiptsRoot: "", // Initialize as empty, will be set after UTXO processing
		},
		Body: &BlockBody{
			Transactions: txTrie,
			Receipts:     make([]*TxReceipt, 0),
		},
		CumulativeDifficulty: previousBlock.CumulativeDifficulty + uint64(difficulty),
	}

	// Process transactions and update UTXO set with nil check
	if bc.utxoPool != nil {
		for _, tx := range pendingTxs {
			bc.utxoPool.AddUTXO(&tx, newBlock.Header.BlockNumber)
		}
		// Set ReceiptsRoot after processing transactions
		newBlock.Header.ReceiptsRoot = bc.utxoPool.GetRootHash()
	} else {
		log.Printf("⚠️ No UTXOPool available, using empty receipts root")
		newBlock.Header.ReceiptsRoot = "0x0000000000000000000000000000000000000000000000000000000000000000"
	}

	// Mine the block using the existing MineBlock function from block.go
	log.Printf("⛏️ Mining block #%d - searching for valid hash...", newBlock.Header.BlockNumber)
	startTime := time.Now()

	// Use the blockchain's existing StakePool instead of creating a new one
	if err := MineBlock(&newBlock, previousBlock, bc.StakePool, difficulty, bc.Node.Host); err != nil {
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

	// Save block by hash using the same key format as GetBlock
	blockKey := db.CreateKey(db.BlockPrefix, []byte(block.Hash()))
	if err := bc.db.Put(blockKey, blockData); err != nil {
		return fmt.Errorf("failed to save block: %v", err)
	}

	// Update latest block pointer
	if err := bc.db.Put([]byte("latest_block"), []byte(block.Hash())); err != nil {
		return fmt.Errorf("failed to update latest block: %v", err)
	}

	log.Printf("📦 Block #%d saved to database", block.Header.BlockNumber)

	// Verify block was saved correctly using a separate function that doesn't cause deadlock
	if err := bc.verifyBlockInDB(block.Hash(), block); err != nil {
		log.Printf("⚠️ Warning: Block verification failed: %v", err)
	} else {
		log.Printf("✅ Block #%d verified in database", block.Header.BlockNumber)
		log.Printf("   • Hash: %s", block.hash)
		log.Printf("   • Previous Hash: %s", block.Header.PreviousHash)
		log.Printf("   • Timestamp: %s (%d)", time.Unix(block.Header.Timestamp, 0).Format(time.RFC3339), block.Header.Timestamp)
		log.Printf("   • Transactions: %d", len(block.Body.Transactions.GetAllTransactions()))
		log.Printf("   • Mined By: %s", block.Header.MinedBy)
		log.Printf("   • Validated By: %s", block.Header.ValidatedBy)
		log.Printf("   • Difficulty: %d", block.Header.Difficulty)
		log.Printf("   • Gas Used: %d", block.Header.GasUsed)
		log.Printf("   • Gas Limit: %d", block.Header.GasLimit)
		log.Printf("   • Version: %d", block.Header.Version)
		log.Printf("   • State Root: %s", block.Header.StateRoot)
		log.Printf("   • Merkle Root: %s", block.Header.MerkleRoot)
		log.Printf("   • Receipts Root: %s", block.Header.ReceiptsRoot)
		log.Printf("   • Block Size: %d bytes", block.Size())
		if len(block.Header.ExtraData) > 0 {
			log.Printf("   • Extra Data: %x", block.Header.ExtraData)
		}
	}

	return nil
}

// Add this method to the Block struct
// Serialize converts a block to bytes
func (b Block) Serialize() ([]byte, error) {
	return json.Marshal(b)
}

// verifyBlockInDB verifies that a block exists in the database without using the blockchain's mutex
// This avoids deadlock when called from saveBlock
func (bc *Blockchain) verifyBlockInDB(hash string, expectedBlock Block) error {
	if bc.db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Get the block directly from the database without using the blockchain's mutex
	key := db.CreateKey(db.BlockPrefix, []byte(hash))
	blockData, err := bc.db.Get(key)
	if err != nil {
		return fmt.Errorf("failed to retrieve block from database: %v", err)
	}

	// Deserialize the block data
	var block Block
	if err := json.Unmarshal(blockData, &block); err != nil {
		return fmt.Errorf("failed to deserialize block data: %v", err)
	}

	// Verify the block matches what we expected
	if block.Hash() != expectedBlock.Hash() {
		return fmt.Errorf("block hash mismatch: expected %s, got %s", expectedBlock.Hash(), block.Hash())
	}

	return nil
}

// InitializeChain initializes the blockchain with the genesis block
func (bc *Blockchain) InitializeChain() error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Check if chain is already initialized
	if bc.GetHeight() > 0 {
		return nil
	}

	log.Printf("🌟 Creating genesis block...")

	// Create genesis block
	genesisBlock := GenesisBlock()

	// Set validator information if available
	if bc.Node != nil && bc.Node.wallet != nil {
		walletAddress := bc.Node.wallet.Address
		genesisBlock.Header.ValidatedBy = walletAddress
		genesisBlock.Header.ValidatorAddress = walletAddress
		log.Printf("🔐 Setting genesis validator to wallet address: %s", walletAddress)
	}

	// Calculate state root for genesis block
	genesisBlock.Header.StateRoot = calculateStateRoot(bc.utxoSet)

	// Calculate and set the hash after all fields are set
	genesisBlock.hash = genesisBlock.Hash()

	// Save genesis block to database
	if err := bc.saveBlock(genesisBlock); err != nil {
		return fmt.Errorf("failed to save genesis block: %v", err)
	}

	// Update chain state
	bc.Chain = []Block{genesisBlock}
	bc.currentHash = genesisBlock.hash

	// Save latest block hash
	if err := bc.db.Put([]byte("latest_block"), []byte(genesisBlock.hash)); err != nil {
		return fmt.Errorf("failed to save latest block hash: %v", err)
	}

	return nil
}

// GetGenesisValidator returns the first registered validator (genesis validator)
func (bc *Blockchain) GetGenesisValidator() *Validator {
	if bc.StakePool == nil {
		return nil
	}

	// Find the first active validator
	for _, validator := range bc.Validators {
		if validator.Status == ValidatorStatusActive {
			return validator
		}
	}
	return nil
}

func (bc *Blockchain) processBlocks() {
	if bc == nil || bc.mempool == nil || bc.StakePool == nil {
		log.Printf("❌ Cannot start block processing: blockchain not properly initialized")
		return
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Process pending transactions
			if len(bc.mempool.GetTransactions()) > 0 {
				// Create new block
				previousBlock := bc.GetLatestBlock()
				difficulty := bc.calculateDifficulty(previousBlock)

				// Select validator
				validator := ""
				if bc.consensus != nil && bc.consensus.state != nil {
					validator = bc.consensus.state.CurrentValidator
				}

				newBlock := NewBlock(previousBlock, bc.mempool, bc.utxoSet, difficulty, validator)

				// Try to add the block
				if err := bc.AddBlock(&newBlock, bc.mempool, bc.StakePool, bc.utxoSet, bc.p2pHost); err != nil {
					log.Printf("⚠️ Failed to add new block: %v", err)
					continue
				}
			}
		}
	}
}

// VerifyStake checks if a stake is valid and mature
func (bc *Blockchain) VerifyStake(address string) error {
	stake, exists := bc.StakePool.Stakes[address]
	if !exists {
		return fmt.Errorf("no stake found for address: %s", address)
	}

	if time.Since(stake.StartTime) < MinStakeAge {
		return fmt.Errorf("stake not mature yet, needs %v more",
			MinStakeAge-time.Since(stake.StartTime))
	}

	return nil
}

// ValidateValidator checks if a validator is eligible
func (bc *Blockchain) ValidateValidator(validator *Validator) error {
	// Verify stake
	if err := bc.VerifyStake(validator.Address); err != nil {
		return fmt.Errorf("stake verification failed: %w", err)
	}

	// Check validator status
	if validator.Status == ValidatorStatusSlashed {
		return fmt.Errorf("validator has been slashed")
	}

	// Verify minimum score for active validators
	if validator.Status == ValidatorStatusActive && validator.Score < 100 {
		return fmt.Errorf("validator score too low: %d", validator.Score)
	}

	return nil
}

// UpdateValidatorSet updates the active validator set
func (bc *Blockchain) UpdateValidatorSet() error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	for address, validator := range bc.Validators {
		if err := bc.ValidateValidator(validator); err != nil {
			// Move to probation if validation fails
			validator.Status = ValidatorStatusProbation
			bc.Validators[address] = validator
		}
	}

	return nil
}

func (bc *Blockchain) finalizeBlock(block *Block, validator *Validator) error {
	// ... existing code ...

	// Distribute rewards
	if err := bc.rewardCalculator.DistributeRewards(block, validator); err != nil {
		return fmt.Errorf("failed to distribute rewards: %w", err)
	}

	// Check for violations
	if err := bc.slashingManager.CheckTimeoutViolations(validator); err != nil {
		log.Printf("Validator %s slashed for timeout violations", validator.Address)
	}
	if err := bc.slashingManager.CheckConsensusViolations(validator); err != nil {
		log.Printf("Validator %s slashed for consensus violations", validator.Address)
	}

	return nil
}

// Add balance management methods
func (bc *Blockchain) AddBalance(address string, amount float64) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.balances == nil {
		bc.balances = make(map[string]float64)
	}

	bc.balances[address] += amount
	return nil
}

// Add these methods to the Blockchain struct
func (bc *Blockchain) GetState() *ChainState {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return &ChainState{
		Height:      bc.GetHeight(),
		RootHash:    bc.currentHash,
		UTXOSetRoot: bc.utxoPool.GetRootHash(),
		StateRoot:   bc.currentHash,
		Timestamp:   time.Now().Unix(),
	}
}

func (bc *Blockchain) GetUTXOState() *ChainState {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	// Add safety check for nil utxoPool
	if bc.utxoPool == nil {
		return &ChainState{
			Height:      bc.GetHeight(),
			RootHash:    "", // Return empty string for nil pool
			UTXOSetRoot: "",
			StateRoot:   "",
			Timestamp:   time.Now().Unix(),
		}
	}

	return &ChainState{
		Height:      bc.GetHeight(),
		RootHash:    bc.utxoPool.GetRootHash(),
		UTXOSetRoot: bc.utxoPool.GetRootHash(),
		StateRoot:   bc.GetState().RootHash,
		Timestamp:   time.Now().Unix(),
	}
}

type ChainState struct {
	Height      uint64
	RootHash    string
	UTXOSetRoot string
	StateRoot   string
	Timestamp   int64
}

func (s *ChainState) Hash() string {
	return s.RootHash
}

// Add this method to UTXOPool struct
func (up *UTXOPool) GetRootHash() string {
	up.mu.RLock()
	defer up.mu.RUnlock()

	// Create a hash of all UTXOs
	hasher := sha256.New()
	for txID, utxo := range up.utxos {
		hasher.Write([]byte(txID))
		hasher.Write([]byte(utxo.Owner))
		binary.Write(hasher, binary.BigEndian, utxo.Amount)
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// GetUTXOSet returns the current UTXO set
func (bc *Blockchain) GetUTXOSet() map[string]UTXO {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.utxoSet
}

// SetStakePool sets the stake pool for the blockchain
func (bc *Blockchain) SetStakePool(pool *StakePool) {
	bc.StakePool = pool
}

// GetStakePool returns the blockchain's stake pool
func (bc *Blockchain) GetStakePool() *StakePool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.StakePool
}

// GetBlock retrieves a block by its hash from either memory or database
func (bc *Blockchain) GetBlock(hash string) (*Block, error) {
	// Use read lock for thread safety
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	log.Printf("🔍 Attempting to retrieve block with hash: %s", hash)

	// First try to find the block in memory
	for _, block := range bc.Chain {
		if block.Hash() == hash {
			log.Printf("✅ Block found in memory")
			return &block, nil
		}
	}
	log.Printf("⚠️ Block not found in memory, checking database")

	// If not found in memory, try to find it in the database
	if bc.db != nil {
		key := db.CreateKey(db.BlockPrefix, []byte(hash))
		log.Printf("🔑 Using database key: %x", key)

		blockData, err := bc.db.Get(key)
		if err != nil {
			if err == db.ErrKeyNotFound {
				log.Printf("❌ Block not found in database: %v", err)
				return nil, fmt.Errorf("block with hash %s not found", hash)
			}
			log.Printf("❌ Database error: %v", err)
			return nil, fmt.Errorf("failed to retrieve block from database: %v", err)
		}
		log.Printf("✅ Block data retrieved from database, size: %d bytes", len(blockData))

		// Deserialize the block data
		var block Block
		if err := json.Unmarshal(blockData, &block); err != nil {
			log.Printf("❌ Failed to deserialize block data: %v", err)
			return nil, fmt.Errorf("failed to deserialize block data: %v", err)
		}
		log.Printf("✅ Block successfully deserialized")

		return &block, nil
	}

	log.Printf("❌ Database not initialized")
	return nil, fmt.Errorf("block with hash %s not found", hash)
}
