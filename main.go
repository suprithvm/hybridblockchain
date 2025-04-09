package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// Block represents a blockchain block
type Block struct {
	Hash            string                 `json:"hash"`
	PreviousHash    string                 `json:"previous_hash"`
	BlockNumber     int64                  `json:"block_number"`
	Timestamp       int64                  `json:"timestamp"`
	MerkleRoot      string                 `json:"merkle_root"`
	StateRoot       string                 `json:"state_root"`
	Difficulty      int64                  `json:"difficulty"`
	ValidatedBy     string                 `json:"validated_by"`
	TransactionData map[string]interface{} `json:"transaction_data"`
}

func main() {
	// Get the database path from command line or use default
	dbPath := "blockchain.db"
	if len(os.Args) > 1 {
		dbPath = os.Args[1]
	}

	// Check if the database directory exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Fatalf("Database directory not found: %s", dbPath)
	}

	// Open the database
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Process and display blocks
	fmt.Println("=== BLOCKCHAIN DATABASE VIEWER ===")
	fmt.Printf("Database: %s\n\n", dbPath)

	// Get all blocks
	blocks := []Block{}
	iter := db.NewIterator(util.BytesPrefix([]byte("block:")), nil)
	for iter.Next() {
		var block Block
		if err := json.Unmarshal(iter.Value(), &block); err != nil {
			log.Printf("Error unmarshaling block: %v", err)
				continue
		}
		blocks = append(blocks, block)
	}
	iter.Release()

	// Check for errors from iterating
	if err := iter.Error(); err != nil {
		log.Fatalf("Error iterating over blocks: %v", err)
	}

	// Display block count
	fmt.Printf("Total blocks in database: %d\n\n", len(blocks))

	// Group blocks by block number to identify duplicates
	blockGroups := make(map[int64][]Block)
	for _, block := range blocks {
		blockGroups[block.BlockNumber] = append(blockGroups[block.BlockNumber], block)
	}

	// Display blocks with duplicates highlighted
	for blockNum := int64(0); blockNum <= getMaxBlockNumber(blocks); blockNum++ {
		group := blockGroups[blockNum]
		if len(group) == 0 {
			continue
		}

		fmt.Printf("=== BLOCK #%d ===\n", blockNum)
		if len(group) > 1 {
			fmt.Printf("⚠️ DUPLICATE BLOCK DETECTED: %d copies found\n", len(group))
		}

		for i, block := range group {
			if len(group) > 1 {
				fmt.Printf("--- Copy #%d ---\n", i+1)
			}

			fmt.Printf("Hash: %s\n", block.Hash)
			fmt.Printf("Previous Hash: %s\n", block.PreviousHash)
			fmt.Printf("Timestamp: %s\n", time.Unix(block.Timestamp, 0).Format(time.RFC3339))
			fmt.Printf("Merkle Root: %s\n", block.MerkleRoot)
			fmt.Printf("State Root: %s\n", block.StateRoot)
			fmt.Printf("Difficulty: %d\n", block.Difficulty)
			fmt.Printf("Validated By: %s\n", block.ValidatedBy)

			// Display transaction data
			if block.TransactionData != nil {
				fmt.Println("Transaction Data:")
				for key, value := range block.TransactionData {
					fmt.Printf("  %s: %v\n", key, value)
				}
			}

			fmt.Println()
		}
	}

	// Display summary of duplicates
	fmt.Println("=== DUPLICATE BLOCK SUMMARY ===")
	for blockNum, group := range blockGroups {
		if len(group) > 1 {
			fmt.Printf("Block #%d: %d copies\n", blockNum, len(group))
			for i, block := range group {
				fmt.Printf("  Copy #%d: Hash=%s, Timestamp=%s\n", 
					i+1, 
					block.Hash, 
					time.Unix(block.Timestamp, 0).Format(time.RFC3339))
			}
		}
	}

	// Display other database information
	fmt.Println("\n=== DATABASE INFORMATION ===")
	
	// Count keys by prefix
	prefixes := []string{"block:", "tx:", "state:", "peer:", "validator:"}
	for _, prefix := range prefixes {
		count := 0
		iter := db.NewIterator(util.BytesPrefix([]byte(prefix)), nil)
		for iter.Next() {
			count++
		}
		iter.Release()
		if err := iter.Error(); err != nil {
			log.Printf("Error counting %s: %v", prefix, err)
			continue
		}
		fmt.Printf("%s: %d entries\n", strings.TrimSuffix(prefix, ":"), count)
	}
}

func getMaxBlockNumber(blocks []Block) int64 {
	var max int64
	for _, block := range blocks {
		if block.BlockNumber > max {
			max = block.BlockNumber
		}
	}
	return max
}
