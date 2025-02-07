package blockchain

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"blockchain-core/blockchain/db"
)

var (
	testLogger *TestLogger
	testDBPath string
)

func init() {
	var err error
	testLogger, err = NewTestLogger("blockchain_store_test")
	if err != nil {
		panic(fmt.Sprintf("Failed to create test logger: %v", err))
	}
	testDBPath = filepath.Join(os.TempDir(), "blockchain-test")
}

// TestMain handles test setup and teardown
func TestMain(m *testing.M) {
	testLogger.Log("=== Starting Blockchain Tests ===")
	testLogger.Log("Test database path: %s", testDBPath)

	// Run tests
	code := m.Run()

	// Cleanup
	testLogger.Log("Cleaning up test database...")
	os.RemoveAll(testDBPath)
	testLogger.Log("=== Test Suite Completed ===")

	os.Exit(code)
}

// Helper functions for creating test data

func createTestBlock(blockNumber uint64, txCount int) *Block {
	header := &BlockHeader{
		Version:      1,
		BlockNumber:  blockNumber,
		PreviousHash: fmt.Sprintf("prev_block_%d", blockNumber-1),
		Timestamp:    time.Now().Unix(),
		MerkleRoot:   fmt.Sprintf("merkle_root_%d", blockNumber),
		StateRoot:    fmt.Sprintf("state_root_%d", blockNumber),
		ReceiptsRoot: fmt.Sprintf("receipts_root_%d", blockNumber),
		Difficulty:   1,
		Nonce:        0,
		GasLimit:     BaseGasLimit,
		GasUsed:      0,
		MinedBy:      "test_miner",
		ValidatedBy:  "test_validator",
		ExtraData:    []byte("test_data"),
	}

	body := &BlockBody{
		Transactions: NewPatriciaTrie(),
		Receipts:     make([]*TxReceipt, 0),
	}

	// Add transactions to the block
	for i := 0; i < txCount; i++ {
		tx := createTestTransaction(blockNumber, uint64(i))
		body.Transactions.Insert(*tx)

		receipt := &TxReceipt{
			TxHash:        tx.TransactionID,
			BlockNumber:   blockNumber,
			GasUsed:       21000,
			Status:        1,
			CumulativeGas: uint64(i+1) * 21000,
		}
		body.Receipts = append(body.Receipts, receipt)
	}

	return &Block{
		Header: header,
		Body:   body,
	}
}

func createTestTransaction(blockNumber, index uint64) *Transaction {
	return &Transaction{
		TransactionID: fmt.Sprintf("tx_%d_%d", blockNumber, index),
		Sender:        fmt.Sprintf("sender_%d_%d", blockNumber, index),
		Receiver:      fmt.Sprintf("receiver_%d_%d", blockNumber, index),
		Amount:        float64(1000 * (index + 1)),
		Timestamp:     time.Now().Unix(),
		GasLimit:      21000,
		GasPrice:      1000,
		Priority:      1,
		MaxFeePerGas:  2000,
	}
}

func createTestUTXO(txID string, index int, amount float64) *UTXO {
	return &UTXO{
		TransactionID: txID,
		OutputIndex:   index,
		Receiver:      fmt.Sprintf("recipient_%s_%d", txID, index),
		Amount:        amount,
		Owner:         fmt.Sprintf("owner_%s_%d", txID, index),
	}
}

func generateTestHash(input string) []byte {
	hash := sha256.Sum256([]byte(input))
	return hash[:]
}

// Test Cases

func TestBlockStorage(t *testing.T) {
	testLogger.Log("=== Running Block Storage Tests ===")

	store := createTestStore(t)
	defer store.Close()

	// Test 1: Save and retrieve a block
	t.Run("Basic Block Operations", func(t *testing.T) {
		block := createTestBlock(1, 5)
		testLogger.Log("Saving block: Number=%d, Hash=%s",
			block.Header.BlockNumber, block.Header.PreviousHash)

		err := store.SaveBlock(block)
		if err != nil {
			t.Fatalf("Failed to save block: %v", err)
		}

		retrieved, err := store.GetBlock(1)
		if err != nil {
			t.Fatalf("Failed to retrieve block: %v", err)
		}

		if retrieved.Header.BlockNumber != block.Header.BlockNumber {
			t.Errorf("Block number mismatch: got %d, want %d",
				retrieved.Header.BlockNumber, block.Header.BlockNumber)
		}

		testLogger.Log("Successfully retrieved block with %d receipts",
			len(retrieved.Body.Receipts))
	})

	// Test 2: Handle invalid blocks
	t.Run("Invalid Block Handling", func(t *testing.T) {
		invalidBlock := createTestBlock(0, 1) // Height 0 is invalid
		testLogger.Log("Attempting to save invalid block with number 0")

		err := store.SaveBlock(invalidBlock)
		if err == nil {
			t.Error("Expected error when saving block with number 0")
		}

		testLogger.Log("Attempting to retrieve non-existent block")
		_, err = store.GetBlock(999)
		if err == nil {
			t.Error("Expected error when retrieving non-existent block")
		}
	})

	// Test 3: Block height indexing
	t.Run("Block Height Indexing", func(t *testing.T) {
		testLogger.Log("Testing sequential block storage...")

		for i := uint64(1); i <= 10; i++ {
			block := createTestBlock(i, 3)
			err := store.SaveBlock(block)
			if err != nil {
				t.Fatalf("Failed to save block at number %d: %v", i, err)
			}
			testLogger.Log("Saved block at number %d", i)
		}

		testLogger.Log("Verifying block sequence...")
		for i := uint64(1); i <= 10; i++ {
			block, err := store.GetBlock(i)
			if err != nil {
				t.Errorf("Failed to retrieve block at number %d: %v", i, err)
				continue
			}
			if block.Header.BlockNumber != i {
				t.Errorf("Block number mismatch at position %d: got %d", i, block.Header.BlockNumber)
			}
		}
	})
}

func TestTransactionStorage(t *testing.T) {
	testLogger.Log("=== Running Transaction Storage Tests ===")

	store := createTestStore(t)
	defer store.Close()

	// Test 1: Basic transaction operations
	t.Run("Basic Transaction Operations", func(t *testing.T) {
		tx := createTestTransaction(1, 0)
		testLogger.Log("Saving transaction: ID=%s, Amount=%f",
			tx.TransactionID, tx.Amount)

		err := store.SaveTransaction(tx)
		if err != nil {
			t.Fatalf("Failed to save transaction: %v", err)
		}

		retrieved, err := store.GetTransaction(tx.TransactionID)
		if err != nil {
			t.Fatalf("Failed to retrieve transaction: %v", err)
		}

		if retrieved.Amount != tx.Amount {
			t.Errorf("Transaction amount mismatch: got %f, want %f",
				retrieved.Amount, tx.Amount)
		}
	})

	// Test 2: Duplicate transactions
	t.Run("Duplicate Transaction Handling", func(t *testing.T) {
		tx := createTestTransaction(1, 1)
		testLogger.Log("Testing duplicate transaction handling: ID=%s", tx.TransactionID)

		err := store.SaveTransaction(tx)
		if err != nil {
			t.Fatalf("Failed to save initial transaction: %v", err)
		}

		err = store.SaveTransaction(tx)
		if err == nil {
			t.Error("Expected error when saving duplicate transaction")
		}
	})
}

func TestUTXOOperations(t *testing.T) {
	testLogger.Log("=== Running UTXO Tests ===")

	store := createTestStore(t)
	defer store.Close()

	// Test 1: UTXO lifecycle
	t.Run("UTXO Lifecycle", func(t *testing.T) {
		utxo := createTestUTXO("test_tx_1", 0, 1000.0)
		testLogger.Log("Saving UTXO: TxID=%s, Index=%d, Amount=%f",
			utxo.TransactionID, utxo.OutputIndex, utxo.Amount)

		err := store.SaveUTXO(utxo)
		if err != nil {
			t.Fatalf("Failed to save UTXO: %v", err)
		}

		retrieved, err := store.GetUTXO(utxo.TransactionID, uint32(utxo.OutputIndex))
		if err != nil {
			t.Fatalf("Failed to retrieve UTXO: %v", err)
		}

		if retrieved.Amount != utxo.Amount {
			t.Errorf("UTXO amount mismatch: got %f, want %f",
				retrieved.Amount, utxo.Amount)
		}
	})

	// Test 2: UTXO set consistency
	t.Run("UTXO Set Consistency", func(t *testing.T) {
		testLogger.Log("Testing UTXO set consistency...")

		utxoCount := 5
		for i := 0; i < utxoCount; i++ {
			utxo := createTestUTXO(fmt.Sprintf("test_tx_%d", i), i, float64(1000*(i+1)))
			err := store.SaveUTXO(utxo)
			if err != nil {
				t.Fatalf("Failed to save UTXO %d: %v", i, err)
			}
		}

		for i := 0; i < utxoCount; i++ {
			utxo, err := store.GetUTXO(fmt.Sprintf("test_tx_%d", i), uint32(i))
			if err != nil {
				t.Errorf("Failed to retrieve UTXO %d: %v", i, err)
				continue
			}

			expectedAmount := float64(1000 * (i + 1))
			if utxo.Amount != expectedAmount {
				t.Errorf("UTXO amount mismatch: got %f, want %f",
					utxo.Amount, expectedAmount)
			}
		}
	})
}

func TestStressOperations(t *testing.T) {
	testLogger.Log("=== Running Stress Tests ===")

	store := createTestStore(t)
	defer store.Close()

	// Test 1: Large block processing
	t.Run("Large Block Processing", func(t *testing.T) {
		txCount := 1000
		testLogger.Log("Creating block with %d transactions", txCount)

		block := createTestBlock(1, txCount)
		startTime := time.Now()

		err := store.SaveBlock(block)
		if err != nil {
			t.Fatalf("Failed to save large block: %v", err)
		}

		duration := time.Since(startTime)
		testLogger.Log("Saved large block in %v", duration)

		retrieved, err := store.GetBlock(1)
		if err != nil {
			t.Fatalf("Failed to retrieve large block: %v", err)
		}

		if len(retrieved.Body.Receipts) != txCount {
			t.Errorf("Transaction receipt count mismatch: got %d, want %d",
				len(retrieved.Body.Receipts), txCount)
		}
	})

	// Test 2: Concurrent operations
	t.Run("Concurrent Operations", func(t *testing.T) {
		var wg sync.WaitGroup
		errorChan := make(chan error, 100)
		operationCount := 50

		testLogger.Log("Starting %d concurrent operations", operationCount)

		for i := 0; i < operationCount; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()

				// Save block
				block := createTestBlock(uint64(index+100), 5) // Start at height 100 to avoid conflicts
				if err := store.SaveBlock(block); err != nil {
					errorChan <- fmt.Errorf("failed to save block %d: %v", index+100, err)
					return
				}

				// Save transactions
				for j := 0; j < 5; j++ {
					tx := createTestTransaction(uint64(index+100), uint64(j))
					if err := store.SaveTransaction(tx); err != nil {
						errorChan <- fmt.Errorf("failed to save transaction %d-%d: %v",
							index+100, j, err)
						return
					}
				}

				testLogger.Log("Completed operation set %d", index)
			}(i)
		}

		wg.Wait()
		close(errorChan)

		errorCount := 0
		for err := range errorChan {
			errorCount++
			testLogger.Log("Error during concurrent operation: %v", err)
		}

		if errorCount > 0 {
			t.Errorf("Encountered %d errors during concurrent operations", errorCount)
		}
	})
}

// Helper function to create a test store
func createTestStore(t *testing.T) *Store {
	testLogger.Log("Creating test store for: %s", t.Name())

	storePath := filepath.Join(testDBPath, t.Name())
	if err := os.MkdirAll(storePath, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	store, err := NewStore(&db.Config{
		Path:         storePath,
		CacheSize:    64,
		MaxOpenFiles: 100,
		Compression:  true,
		Name:         "test-db",
		Logger:       testLogger,
	})
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	testLogger.Log("Test store created at: %s", storePath)
	return store
}
