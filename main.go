package main

import (
	"bufio"
	"context"
	"crypto/elliptic"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blockchain-core/blockchain"
	"blockchain-core/blockchain/db"
	"blockchain-core/blockchain/sync"
)

type NodeRole int

const (
	RoleObserver NodeRole = iota
	RoleMiner
	RoleValidator
	RoleBootstrap
)

type NodeConfig struct {
	Role           NodeRole
	DataDir        string
	BootstrapNodes []string
	ListenAddr     string
	RPCAddr        string
	ValidatorStake float64
	MinerThreads   int
	EnableMetrics  bool
	LogLevel       string
	NetworkID      string
}

func main() {
	log.Printf("🚀 Starting blockchain node")

	// Parse command line flags
	config := parseFlags()

	log.Printf("📋 Configuration loaded")

	// Initialize logging
	setupLogging(config.LogLevel)
	log.Printf("\n🔗 Blockchain Node Initialization - Role: %s", getRoleName(config.Role))
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Create data directories
	if err := setupDataDir(config.DataDir); err != nil {
		log.Fatalf("❌ Failed to create data directory: %v", err)
	}

	log.Printf("📂 Data directory setup: %s", config.DataDir)

	// Initialize database
	db, store := initializeDatabase(config)
	defer db.Close()

	// Initialize node based on role
	switch config.Role {
	case RoleBootstrap:
		runBootstrapNode(config)
	case RoleMiner:
		runMinerNode(config, store)
	case RoleValidator:
		runValidatorNode(config, store)
	case RoleObserver:
		runObserverNode(config, store)
	}

	// Keep the application running
	select {}
}

func parseFlags() *NodeConfig {
	config := &NodeConfig{}

	// Basic node configuration
	role := flag.String("role", "miner", "Node role (bootstrap, validator, or miner)")
	dataDir := flag.String("datadir", "./data", "Data directory for the node")
	listenAddr := flag.String("listen", ":50505", "Listen address for p2p")
	rpcAddr := flag.String("rpc", ":8545", "RPC server address")
	networkID := flag.String("network", "testnet", "Network identifier")

	// Bootstrap configuration
	bootstrapNodes := flag.String("bootnodes", "", "Comma separated bootstrap node addresses")

	// Validator configuration
	stake := flag.Float64("stake", 0.0, "Amount to stake (for validators)")

	// Miner configuration
	threads := flag.Int("threads", 1, "Number of mining threads")

	// Additional options
	metrics := flag.Bool("metrics", false, "Enable metrics collection")
	logLevel := flag.String("loglevel", "info", "Logging level (debug, info, warn, error)")

	flag.Parse()

	// Parse role
	config.Role = parseRole(*role)
	config.DataDir = *dataDir
	config.ListenAddr = *listenAddr
	config.RPCAddr = *rpcAddr
	config.NetworkID = *networkID
	config.ValidatorStake = *stake
	config.MinerThreads = *threads
	config.EnableMetrics = *metrics
	config.LogLevel = *logLevel

	// Parse bootstrap nodes
	if *bootstrapNodes != "" {
		config.BootstrapNodes = strings.Split(*bootstrapNodes, ",")
	}

	return config
}

func runBootstrapNode(config *NodeConfig) {
	log.Printf("🌟 Starting Bootstrap Node")

	bootConfig := &blockchain.BootstrapNodeConfig{
		ListenPort:         extractPort(config.ListenAddr),
		DataDir:            config.DataDir,
		EnableRelay:        true,
		EnableNAT:          true,
		EnablePeerExchange: true,
		StoragePath:        config.DataDir,
		NetworkID:          config.NetworkID,
		EnableMetrics:      config.EnableMetrics,
	}

	// Initialize bootstrap node with context
	node, err := blockchain.NewBootstrapNode(bootConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create bootstrap node: %v", err)
	}

	// Start the node
	if err := node.Start(); err != nil {
		log.Fatalf("❌ Failed to start bootstrap node: %v", err)
	}

	log.Printf("✅ Bootstrap node is running on %s", config.ListenAddr)
}

func runMinerNode(config *NodeConfig, store *blockchain.Store) error {
	log.Printf("⛏️ Starting Miner Node")

	// Handle wallet setup
	wallet, err := setupWallet(config.DataDir)
	if err != nil {
		log.Fatalf("❌ Failed to setup wallet: %v", err)
	}

	// Initialize blockchain with store's database
	dbConfig := &blockchain.DatabaseConfig{
		Type:         "leveldb",
		Path:         filepath.Join(config.DataDir, "chaindata"),
		CacheSize:    256,
		MaxOpenFiles: 64,
		Compression:  true,
	}

	// Initialize blockchain
	bc := blockchain.InitialiseBlockchain(dbConfig)

	// Read bootnode address from file if not provided in config
	if len(config.BootstrapNodes) == 0 {
		bootnodeAddr, err := os.ReadFile("bootnode.addr")
		if err != nil {
			log.Fatalf("❌ Failed to read bootnode address: %v", err)
		}
		config.BootstrapNodes = []string{strings.TrimSpace(string(bootnodeAddr))}
		log.Printf("📡 Using bootnode address from file: %s", config.BootstrapNodes[0])
	}

	// Create network configuration
	networkConfig := &blockchain.NetworkConfig{
		P2PPort:        extractPort(config.ListenAddr),
		RPCPort:        extractPort(config.RPCAddr),
		BootstrapNodes: config.BootstrapNodes,
		NetworkID:      config.NetworkID,
		ChainID:        parseChainID(config.NetworkID),
		NetworkPath:    config.DataDir,
		Blockchain:     bc,
		Wallet:         wallet,
		DHTServerMode:  true,
	}

	// Create node
	node, err := blockchain.NewNode(networkConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create node: %v", err)
	}

	// Start the node
	if err := node.Start(); err != nil {
		log.Fatalf("❌ Failed to start node: %v", err)
	}

	// Log node ID
	log.Printf("🌐 P2P node initialized with ID: %s", node.Host.ID())

	// Connect to bootstrap nodes with retries
	log.Printf("🔄 Connecting to bootstrap nodes...")
	maxRetries := 5
	retryDelay := 5 * time.Second
	connected := false

	for i := 0; i < maxRetries; i++ {
		log.Printf("📡 Attempt %d/%d: Connecting to bootstrap nodes: %v", i+1, maxRetries, config.BootstrapNodes)
		if err := node.ConnectToBootstrapNodes(context.Background()); err != nil {
			log.Printf("⚠️ Attempt %d/%d: Failed to connect to bootstrap nodes: %v", i+1, maxRetries, err)
			if i < maxRetries-1 {
				log.Printf("⏳ Retrying in %v...", retryDelay)
				time.Sleep(retryDelay)
				continue
			}
		} else {
			connected = true
			log.Printf("✅ Successfully connected to bootstrap nodes")
			break
		}
	}

	if !connected {
		log.Printf("❌ Failed to connect to any bootstrap nodes after %d attempts", maxRetries)
		return fmt.Errorf("failed to connect to bootstrap nodes")
	}

	// Only proceed with peer discovery if connected to bootnode
	if connected {
		log.Printf("🔍 Starting peer discovery...")
		if err := node.DiscoverPeers(); err != nil {
			log.Printf("⚠️ Peer discovery warning: %v", err)
		}
	}

	// Register blockchain handlers
	if err := node.RegisterBlockchainHandlers(bc); err != nil {
		log.Printf("⚠️ Warning: Failed to register blockchain handlers: %v", err)
	}

	// Start periodic peer discovery and blockchain syncing
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Only discover new peers if we have a bootnode connection
				if len(node.Host.Network().Peers()) > 0 {
					if err := node.DiscoverPeers(); err != nil {
						log.Printf("⚠️ Peer discovery warning: %v", err)
					}

					// Sync with non-bootstrap peers
					for _, peerID := range node.Host.Network().Peers() {
						if !node.IsPeerBootstrapNode(peerID) {
							if err := node.SyncWithPeer(peerID); err != nil {
								log.Printf("⚠️ Failed to sync with peer %s: %v", peerID, err)
							}
						}
					}
				} else {
					log.Printf("⚠️ No peers connected, attempting to reconnect to bootstrap nodes...")
					if err := node.ConnectToBootstrapNodes(context.Background()); err != nil {
						log.Printf("❌ Failed to reconnect to bootstrap nodes: %v", err)
					}
				}
			}
		}
	}()

	return nil
}

func runValidatorNode(config *NodeConfig, store *blockchain.Store) {
	log.Printf("🚀 Starting validator node...")

	// Step 1: Initialize wallet
	wallet, err := setupWallet(config.DataDir)
	if err != nil {
		log.Fatalf("❌ Failed to setup wallet: %v", err)
	}

	// Step 2: Initialize blockchain with existing store
	bc := blockchain.InitialiseBlockchainWithStore(store)

	// Read bootnode address from file if not provided in config
	if len(config.BootstrapNodes) == 0 {
		bootnodeAddr, err := os.ReadFile("bootnode.addr")
		if err != nil {
			log.Fatalf("❌ Failed to read bootnode address: %v", err)
		}
		config.BootstrapNodes = []string{strings.TrimSpace(string(bootnodeAddr))}
		log.Printf("📡 Using bootnode address from file: %s", config.BootstrapNodes[0])
	}

	// Step 3: Initialize P2P network
	networkConfig := &blockchain.NetworkConfig{
		P2PPort:        extractPort(config.ListenAddr),
		RPCPort:        extractPort(config.RPCAddr),
		BootstrapNodes: config.BootstrapNodes,
		NetworkID:      config.NetworkID,
		ChainID:        parseChainID(config.NetworkID),
		NetworkPath:    config.DataDir,
		Blockchain:     bc,
		Wallet:         wallet,
		DHTServerMode:  true,
	}

	// Step 4: Create and start P2P node
	node, err := blockchain.NewNode(networkConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create P2P node: %v", err)
	}

	// Start the node
	if err := node.Start(); err != nil {
		log.Fatalf("❌ Failed to start node: %v", err)
	}

	// Set node in blockchain and initialize stake pool
	bc.Node = node
	bc.SetStakePool(blockchain.NewStakePool(bc))

	// Step 5: Connect to bootstrap nodes with retries
	log.Printf("🔄 Connecting to bootstrap nodes...")
	maxRetries := 5
	retryDelay := 5 * time.Second
	connected := false

	for i := 0; i < maxRetries; i++ {
		log.Printf("📡 Attempt %d/%d: Connecting to bootstrap nodes: %v", i+1, maxRetries, config.BootstrapNodes)
		if err := node.ConnectToBootstrapNodes(context.Background()); err != nil {
			log.Printf("⚠️ Attempt %d/%d: Failed to connect to bootstrap nodes: %v", i+1, maxRetries, err)
			if i < maxRetries-1 {
				log.Printf("⏳ Retrying in %v...", retryDelay)
				time.Sleep(retryDelay)
				continue
			}
		} else {
			connected = true
			log.Printf("✅ Successfully connected to bootstrap nodes")
			break
		}
	}

	if !connected {
		log.Printf("❌ Failed to connect to any bootstrap nodes after %d attempts", maxRetries)
		return
	}

	// Step 6: Start peer discovery and sync
	log.Printf("🔍 Starting peer discovery and blockchain sync...")

	// First, discover peers
	log.Printf("👥 Starting peer discovery...")
	if err := node.DiscoverPeers(); err != nil {
		log.Printf("⚠️ Peer discovery warning: %v", err)
	}

	// Wait for minimum peer connections (excluding bootnode)
	peerCheckTicker := time.NewTicker(5 * time.Second)
	peerTimeout := time.After(2 * time.Minute)
	minPeers := 1
	peerDiscoveryComplete := false

	log.Printf("⏳ Waiting for peer connections (minimum %d non-bootnode peers)...", minPeers)

peerDiscoveryLoop:
	for !peerDiscoveryComplete {
		select {
		case <-peerTimeout:
			log.Printf("⚠️ Peer discovery timed out after 2 minutes")
			break peerDiscoveryLoop

		case <-peerCheckTicker.C:
			peers := node.Host.Network().Peers()
			nonBootnodePeers := 0
			for _, peer := range peers {
				if !node.IsPeerBootstrapNode(peer) {
					nonBootnodePeers++
				}
			}
			log.Printf("📊 Current peer count: %d (non-bootnode peers: %d)", len(peers), nonBootnodePeers)

			if nonBootnodePeers >= minPeers {
				log.Printf("✅ Connected to sufficient non-bootnode peers: %d", nonBootnodePeers)
				peerDiscoveryComplete = true
				break peerDiscoveryLoop
			}

			// Try to discover more peers
			if err := node.DiscoverPeers(); err != nil {
				log.Printf("⚠️ Peer discovery attempt failed: %v", err)
			}
		}
	}

	peerCheckTicker.Stop()

	// Now proceed with blockchain sync only with non-bootnode peers
	log.Printf("🔄 Starting blockchain synchronization with non-bootnode peers...")

	// Initialize sync state
	syncComplete := false
	syncTimeout := time.After(5 * time.Minute)
	syncTicker := time.NewTicker(5 * time.Second)
	defer syncTicker.Stop()

syncLoop:
	for !syncComplete {
		select {
		case <-syncTimeout:
			log.Printf("⚠️ Blockchain sync timed out after 5 minutes")
			break syncLoop

		case <-syncTicker.C:
			// Try to sync with non-bootnode peers
			peers := node.Host.Network().Peers()
			log.Printf("🔄 Attempting to sync with non-bootnode peers...")

			for _, peerID := range peers {
				// Skip bootnode for sync
				if node.IsPeerBootstrapNode(peerID) {
					log.Printf("⏭️ Skipping sync with bootnode peer: %s", peerID)
					continue
				}

				log.Printf("🔄 Syncing with non-bootnode peer %s...", peerID)
				if err := node.SyncWithPeer(peerID); err != nil {
					log.Printf("⚠️ Failed to sync with peer %s: %v", peerID, err)
					continue
				}

				// Check if we have a genesis block
				if bc.GetHeight() > 0 {
					log.Printf("✅ Successfully synced blockchain. Current height: %d", bc.GetHeight())
					syncComplete = true
					break
				}
			}

			// If no peers have a blockchain yet and we're the first validator
			if !syncComplete && bc.GetHeight() == 0 {
				log.Printf("🌟 No existing blockchain found. Checking if we should create genesis block...")

				// Check if we're the first validator
				isFirstValidator := true
				for _, peer := range peers {
					if !node.IsPeerBootstrapNode(peer) {
						isFirstValidator = false
						break
					}
				}

				if isFirstValidator {
					log.Printf("🌟 We appear to be the first validator. Initializing genesis block...")
					genesisBlock := blockchain.GenesisBlock()
					if err := bc.AddBlock(&genesisBlock, node.Mempool, bc.GetStakePool(), bc.GetUTXOSet(), node.Host); err != nil {
						log.Printf("❌ Failed to add genesis block: %v", err)
						continue
					}
					log.Printf("✅ Genesis block created and added to chain")
					syncComplete = true
				} else {
					log.Printf("⏳ Waiting for blockchain sync from other validators...")
				}
			}
		}
	}

	// Step 7: Initialize validator
	log.Printf("🔐 Initializing validator...")
	validatorConfig := &blockchain.ValidatorConfig{
		Stake:        config.ValidatorStake,
		MinStake:     0,
		RewardRate:   0.01,
		SlashingRate: 0.5,
		BlockTimeout: 30 * time.Second,
		MaxMissed:    10,
	}

	validator, err := blockchain.NewValidator(bc, validatorConfig, wallet.Address)
	if err != nil {
		log.Fatalf("❌ Failed to create validator: %v", err)
	}

	// Step 8: Start validator
	if err := validator.Start(); err != nil {
		log.Fatalf("❌ Failed to start validator: %v", err)
	}

	log.Printf("✅ Validator node is running")

	// Start periodic peer discovery and blockchain syncing
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Discover new peers
				if err := node.DiscoverPeers(); err != nil {
					log.Printf("⚠️ Peer discovery warning: %v", err)
				}

				// Sync with non-bootnode peers
				for _, peerID := range node.Host.Network().Peers() {
					if !node.IsPeerBootstrapNode(peerID) {
						if err := node.SyncWithPeer(peerID); err != nil {
							log.Printf("⚠️ Failed to sync with peer %s: %v", peerID, err)
						}
					}
				}
			}
		}
	}()
}

func runObserverNode(config *NodeConfig, store *blockchain.Store) {
	log.Printf("👀 Starting Observer Node")

	// Initialize blockchain
	dbConfig := &blockchain.DatabaseConfig{
		Type:      "leveldb",
		Path:      filepath.Join(config.DataDir, "blockchain"),
		CacheSize: 256,
	}
	bc := blockchain.InitialiseBlockchain(dbConfig)

	// Initialize and start sync service
	startSyncService(config, bc, store)
}

// Helper functions...
func setupLogging(level string) {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	switch strings.ToLower(level) {
	case "debug":
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Llongfile)
	case "warn":
		// Add custom warning prefix
		log.SetPrefix("WARNING: ")
	case "error":
		// Add custom error prefix
		log.SetPrefix("ERROR: ")
	}
}

func setupDataDir(dataDir string) error {
	return os.MkdirAll(dataDir, 0755)
}

func initializeDatabase(config *NodeConfig) (db.Database, *blockchain.Store) {
	log.Printf("🔧 Initializing Database in main.go")

	// Create database options
	dbConfig := &db.Options{
		Type:         "leveldb",
		Path:         filepath.Join(config.DataDir, "nodedata"),
		CacheSize:    256,
		MaxOpenFiles: 64,
		Compression:  true,
	}

	log.Printf("📝 Database Options Created:")
	log.Printf("   • Type: %s", dbConfig.Type)
	log.Printf("   • Path: %s", dbConfig.Path)
	log.Printf("   • MaxOpenFiles: %d", dbConfig.MaxOpenFiles)

	database, err := db.NewDatabase(dbConfig)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Create store with the database interface
	store, err := blockchain.NewStore(database)
	if err != nil {
		log.Fatalf("❌ Failed to create blockchain store: %v", err)
	}

	log.Printf("📦 Database initialized: %s", dbConfig.Path)
	return database, store
}

func startSyncService(config *NodeConfig, bc *blockchain.Blockchain, store *blockchain.Store) {
	// Get the host from the blockchain's node
	host := bc.Node.Host
	log.Printf("🔄 Sync Service: Running on %s", config.ListenAddr)

	// Create sync service with the host
	syncService := sync.NewSyncService(&sync.SyncConfig{
		ListenAddr:     config.ListenAddr,
		BootstrapNodes: config.BootstrapNodes,
		NetworkID:      config.NetworkID,
		EnableMetrics:  config.EnableMetrics,
	}, bc, store, host)

	// Start the sync service
	if err := syncService.Start(config.ListenAddr); err != nil {
		log.Printf("⚠️ Failed to start sync service: %v", err)
		return
	}

	// Only sync with non-bootnode peers
	for _, peerID := range bc.Node.Host.Network().Peers() {
		// Skip bootnode peers
		if bc.Node.IsPeerBootstrapNode(peerID) {
			continue
		}

		log.Printf("🔄 Attempting to sync with peer %s", peerID.String())
		if err := syncService.SyncWithPeer(peerID); err != nil {
			log.Printf("⚠️ Failed to sync with peer %s: %v", peerID.String(), err)
			continue
		}
	}

	log.Printf("✅ Sync service started on %s", config.ListenAddr)
}

func parseRole(role string) NodeRole {
	switch strings.ToLower(role) {
	case "bootstrap":
		return RoleBootstrap
	case "miner":
		return RoleMiner
	case "validator":
		return RoleValidator
	default:
		return RoleObserver
	}
}

func getRoleName(role NodeRole) string {
	switch role {
	case RoleBootstrap:
		return "Bootstrap Node"
	case RoleMiner:
		return "Miner"
	case RoleValidator:
		return "Validator"
	default:
		return "Observer"
	}
}

func extractPort(addr string) int {
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		return 50505 // Default port
	}
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)
	return port
}

type logWrapper struct {
	*log.Logger
}

func (l *logWrapper) Debug(v ...interface{}) {
	l.Printf("DEBUG: %v", v...)
}

func (l *logWrapper) Error(v ...interface{}) {
	l.Printf("ERROR: %v", v...)
}

func (l *logWrapper) Info(v ...interface{}) {
	l.Printf("INFO: %v", v...)
}

func parseChainID(networkID string) uint64 {
	switch networkID {
	case "mainnet":
		return 1
	case "testnet":
		return 2
	default:
		return 3 // devnet
	}
}

func setupWallet(dataDir string) (*blockchain.Wallet, error) {
	walletPath := filepath.Join(dataDir, "wallet.json")

	// Check if wallet exists
	if _, err := os.Stat(walletPath); err == nil {
		// Load existing wallet
		log.Printf("💼 Loading existing wallet from %s", walletPath)
		wallet, err := blockchain.LoadWalletFromFile(walletPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load wallet: %v", err)
		}
		log.Printf("📝 Wallet address: %s", wallet.Address)
		return wallet, nil
	}

	// Ask user if they want to create a new wallet or restore from mnemonic
	fmt.Println("\n💼 Wallet not found. Choose an option:")
	fmt.Println("1. Create a new wallet")
	fmt.Println("2. Restore from mnemonic phrase")

	var choice int
	fmt.Print("\nEnter your choice (1-2): ")
	fmt.Scanf("%d", &choice)

	var wallet *blockchain.Wallet
	var err error

	switch choice {
	case 1:
		// Create new wallet
		fmt.Println("\n🔑 Creating new wallet...")
		wallet, err = blockchain.NewWallet()
		if err != nil {
			return nil, fmt.Errorf("failed to create wallet: %v", err)
		}

		// Display wallet information
		fmt.Println("\n✅ Wallet created successfully!")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📝 Wallet Address:", wallet.Address)
		fmt.Println("🔐 Public Key:", hex.EncodeToString(elliptic.Marshal(wallet.PublicKey.Curve, wallet.PublicKey.X, wallet.PublicKey.Y)))
		privateKeyBytes, _ := wallet.PrivateKey.D.MarshalText()
		fmt.Println("🔑 Private Key:", string(privateKeyBytes))
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("⚠️ IMPORTANT: Write down your mnemonic phrase and keep it safe!")
		fmt.Println("🔤 Mnemonic:", wallet.Mnemonic)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		// Ask user to confirm they've saved the mnemonic
		fmt.Print("\nHave you saved your mnemonic phrase? (y/n): ")
		var confirm string
		fmt.Scanf("%s", &confirm)
		if confirm != "y" && confirm != "Y" {
			fmt.Println("⚠️ Please save your mnemonic phrase before continuing!")
			fmt.Println("🔤 Mnemonic:", wallet.Mnemonic)
			fmt.Print("\nPress Enter when you have saved it...")
			fmt.Scanln()
		}

	case 2:
		// Restore from mnemonic
		fmt.Println("\n🔄 Wallet Recovery")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("Please enter your 12-word mnemonic phrase:")

		var mnemonic string
		// Clear any leftover input
		bufio.NewReader(os.Stdin).ReadString('\n')

		// Use ReadString instead of Scanner for more reliable input
		fmt.Print("> ")
		reader := bufio.NewReader(os.Stdin)
		mnemonic, err = reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read mnemonic: %v", err)
		}

		// Trim whitespace and newlines
		mnemonic = strings.TrimSpace(mnemonic)

		// Validate mnemonic has 12 words
		words := strings.Fields(mnemonic)
		if len(words) != 12 {
			return nil, fmt.Errorf("invalid mnemonic: expected 12 words, got %d", len(words))
		}

		fmt.Println("\n🔄 Restoring wallet from mnemonic...")
		wallet, err = blockchain.RecoverWallet(mnemonic)
		if err != nil {
			return nil, fmt.Errorf("failed to restore wallet: %v", err)
		}

		// Display recovered wallet information
		fmt.Println("\n✅ Wallet recovered successfully!")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📝 Wallet Address:", wallet.Address)
		fmt.Println("🔐 Public Key:", hex.EncodeToString(elliptic.Marshal(wallet.PublicKey.Curve, wallet.PublicKey.X, wallet.PublicKey.Y)))
		privateKeyBytes, _ := wallet.PrivateKey.D.MarshalText()
		fmt.Println("🔑 Private Key:", string(privateKeyBytes))
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	default:
		return nil, fmt.Errorf("invalid choice")
	}

	// Save wallet
	if err := wallet.SaveToFile(walletPath); err != nil {
		return nil, fmt.Errorf("failed to save wallet: %v", err)
	}

	log.Printf("💼 Wallet saved to %s", walletPath)
	log.Printf("📝 Wallet address: %s", wallet.Address)

	return wallet, nil
}

// Initialize miner node
func initMinerNode(config *NodeConfig) error {
	log.Printf("🏗️ Initializing miner node with ID: %s", config.DataDir)

	// Load or create wallet
	wallet, err := setupWallet(config.DataDir)
	if err != nil {
		return err
	}
	log.Printf("💼 Miner wallet initialized with address: %s", wallet.Address)

	// Initialize blockchain
	log.Printf("⛓️ Initializing blockchain database at %s", config.DataDir)
	bc, err := blockchain.NewBlockchain(config.DataDir)
	if err != nil {
		return err
	}
	log.Printf("✅ Blockchain initialized - current height: %d", bc.GetHeight())

	// Initialize P2P network
	log.Printf("🌐 Setting up P2P network on port %d", extractPort(config.ListenAddr))
	node, err := initP2PNetwork(config, bc, wallet)
	fmt.Println("node", node)
	if err != nil {
		return err
	}
	log.Printf("🔌 P2P network initialized - connecting to bootstrap nodes")

	// Connect to bootstrap nodes
	if len(config.BootstrapNodes) > 0 {
		log.Printf("🔄 Connecting to %d bootstrap nodes", len(config.BootstrapNodes))
		for _, addr := range config.BootstrapNodes {
			log.Printf("  ↳ Attempting connection to %s", addr)
			// Connection logic
		}
	}

	// Start mining
	log.Printf("⛏️ Starting mining process with address %s", wallet.Address)
	go startMining(bc, wallet.Address)
	log.Printf("✨ Miner node fully initialized and operational")

	return nil
}

// Start mining process
func startMining(bc *blockchain.Blockchain, minerAddress string) {
	log.Printf("⚒️ Mining service activated for address %s", minerAddress)

	for {
		log.Printf("🔄 Starting new mining cycle")
		block, err := bc.MineBlock(minerAddress)
		if err != nil {
			log.Printf("❌ Mining error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		log.Printf("💎 Successfully mined block #%d with %d transactions",
			block.Header.BlockNumber, len(block.Body.Transactions.GetAllTransactions()))
		log.Printf(" Block stats: Hash: %s, Nonce: %d",
			block.Hash(), block.Header.Nonce)

		// Short pause between mining cycles
		time.Sleep(100 * time.Millisecond)
	}
}

// Initialize validator node
func initValidatorNode(config *NodeConfig) error {
	log.Printf("🏗️ Initializing validator node with ID: %s", config.DataDir)

	// Load or create wallet
	wallet, err := setupWallet(config.DataDir)
	if err != nil {
		return err
	}
	log.Printf("💼 Validator wallet initialized with address: %s", wallet.Address)

	// Initialize blockchain
	log.Printf("⛓️ Initializing blockchain database at %s", config.DataDir)
	bc, err := blockchain.NewBlockchain(config.DataDir)
	if err != nil {
		return err
	}
	log.Printf("✅ Blockchain initialized - current height: %d", bc.GetHeight())

	// Initialize P2P network
	log.Printf("🌐 Setting up P2P network on port %d", extractPort(config.ListenAddr))
	node, err := initP2PNetwork(config, bc, wallet)
	fmt.Println("node", node)
	if err != nil {
		return err
	}
	log.Printf("🔌 P2P network initialized - connecting to bootstrap nodes")

	// Create validator config
	validatorConfig := &blockchain.ValidatorConfig{
		Stake:        config.ValidatorStake,
		MinStake:     0,
		RewardRate:   config.ValidatorStake * 0.05,
		SlashingRate: config.ValidatorStake * 0.10,
	}
	log.Printf("🔐 Creating validator with stake: %.4f tokens", config.ValidatorStake)

	// Initialize validator
	validator, err := blockchain.NewValidator(bc, validatorConfig, wallet.Address)
	if err != nil {
		return err
	}

	// Start validation
	log.Printf("🚀 Starting validation process")
	if err := validator.Start(); err != nil {
		return err
	}
	log.Printf("✨ Validator node fully initialized and operational")

	return nil
}

// initP2PNetwork initializes the P2P network for the node
func initP2PNetwork(config *NodeConfig, bc *blockchain.Blockchain, wallet *blockchain.Wallet) (*blockchain.Node, error) {
	log.Printf("🔌 Initializing P2P network on %s", config.ListenAddr)

	// Create network configuration
	networkConfig := &blockchain.NetworkConfig{
		P2PPort:        extractPort(config.ListenAddr),
		BootstrapNodes: config.BootstrapNodes,
		NetworkID:      config.NetworkID,
		ChainID:        parseChainID(config.NetworkID),
		NetworkPath:    config.DataDir,
		Blockchain:     bc,
		Wallet:         wallet,
		DHTServerMode:  true,
	}

	// Create and start P2P node
	node, err := blockchain.NewNode(networkConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create P2P node: %v", err)
	}

	// Start the node
	if err := node.Start(); err != nil {
		return nil, fmt.Errorf("failed to start P2P node: %v", err)
	}

	// Connect to bootstrap nodes
	if len(config.BootstrapNodes) > 0 {
		log.Printf("🔄 Connecting to %d bootstrap nodes", len(config.BootstrapNodes))
		if err := node.ConnectToBootstrapNodes(context.Background()); err != nil {
			log.Printf("⚠️ Warning: Some bootstrap connections failed: %v", err)
		}
	}

	return node, nil
}
