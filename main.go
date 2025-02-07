package main

import (
	"log"
	"os"
	"path/filepath"

	"blockchain-core/blockchain"
	"blockchain-core/blockchain/db"
	"blockchain-core/blockchain/sync"
)

type logWrapper struct {
    *log.Logger
}

func (l *logWrapper) Debug(v ...interface{}) {
    l.Println(v...)
}

func main() {
	log.Printf("\n🔗 Blockchain Node Initialization")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Get the current working directory
	currentDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("❌ Failed to get current directory: %v", err)
	}

	// Create data directories
	nodeDataDir := filepath.Join(currentDir, "node_data")
	if err := os.MkdirAll(nodeDataDir, 0755); err != nil {
		log.Fatalf("❌ Failed to create node data directory: %v", err)
	}

	log.Printf("📂 Data Directory: %s", nodeDataDir)

	// Initialize database
	dbConfig := &db.Config{
		Type:         db.LevelDB,
		Path:         filepath.Join(nodeDataDir, "blockchain"),
		CacheSize:    512,
		MaxOpenFiles: 64,
		Compression:  true,
		Logger:      &logWrapper{log.Default()},
	}

	// Create blockchain store
	store, err := blockchain.NewStore(dbConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create blockchain store: %v", err)
	}
	defer store.Close()

	log.Printf("📦 Blockchain store initialized")

	// Initialize blockchain
	bc := blockchain.InitialiseBlockchain()
	if bc == nil {
		log.Fatalf("❌ Failed to initialize blockchain")
	}

	log.Printf("⛓️ Blockchain initialized")

	// Initialize sync service
	syncConfig := sync.DefaultSyncConfig()
	syncService, err := sync.NewSyncService(syncConfig, bc, store)
	if err != nil {
		log.Fatalf("❌ Failed to create sync service: %v", err)
	}

	// Start sync service
	if err := syncService.Start(":50505"); err != nil {
		log.Fatalf("❌ Failed to start sync service: %v", err)
	}

	log.Printf("🔄 Sync Service: Running on port 50505")
	log.Printf("✅ Blockchain node is running")

	// Keep the application running
	select {}
}
