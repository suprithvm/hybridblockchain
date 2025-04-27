package main

import (
	"bufio"
	"context"
	"crypto/elliptic"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"blockchain-core/blockchain"
	"blockchain-core/blockchain/db"
	"blockchain-core/blockchain/sync"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Constant for TXNS node ID to skip during block propagation
const TXNSNodeID = "12D3KooWFz4MY4XYWW49fZP3WpzSe3eoEUzrg5k92zPvkD7VkW31"

type NodeRole int

const (
	RoleObserver NodeRole = iota
	RoleMiner
	RoleValidator
	RoleBootstrap
	RoleTXNS //Test node to test transactions
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
	case RoleTXNS:
		if err := runTXNS(config, store); err != nil {
			log.Fatalf("❌ Failed to run TXNS node: %v", err)
		}
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

	// Use the node variable
	log.Printf("✅ Bootstrap node with ID %p is running on %s", node, config.ListenAddr)
}

func runMinerNode(config *NodeConfig, store *blockchain.Store) error {
	log.Printf("⛏️ Starting Miner Node")

	// Load or create wallet
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

	// Set the node in the blockchain BEFORE starting the node
	bc.Node = node
	log.Printf("🔗 Blockchain node connection established with ID: %s", node.Host.ID())

	// Additional debug logs
	log.Printf("🔍 Verifying blockchain components:")
	log.Printf("   • Blockchain UTXOPool: %v", bc.GetUTXOPool() != nil)
	log.Printf("   • Node UTXOPool: %v", node.UTXOPool != nil)
	log.Printf("   • Node AccountManager: %v", node.GetAccountManager() != nil)

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

	// Try peer discovery a few times to find validator
	maxPeerDiscoveryAttempts := 4
	validatorFound := false
	var validatorPeer peer.ID

	for i := 0; i < maxPeerDiscoveryAttempts; i++ {
		log.Printf("👥 Peer discovery attempt %d/%d", i+1, maxPeerDiscoveryAttempts)
		if err := node.DiscoverPeers(); err != nil {
			log.Printf("⚠️ Peer discovery attempt failed: %v", err)
		}

		// Check for validator peer, skipping TXNS nodes
		for _, p := range node.Host.Network().Peers() {
			// Skip if it's a bootstrap node
			if node.IsPeerBootstrapNode(p) {
				continue
			}

			// Skip TXNS node - here we can use node ID to identify TXNS nodes
			// The TXNSNodeID constant is defined at the top of the file
			if p.String() == TXNSNodeID {
				log.Printf("⏭️ Skipping TXNS node during validator search: %s", p)
				continue
			}

			// This is potentially a validator
			validatorPeer = p
			validatorFound = true
			log.Printf("✅ Found validator peer: %s", p)
			break
		}

		if validatorFound {
			break
		}

		if i < maxPeerDiscoveryAttempts-1 {
			log.Printf("⏳ No validator found, waiting before next attempt...")
			time.Sleep(5 * time.Second)
		}
	}

	if !validatorFound {
		return fmt.Errorf("no validator peer found after %d attempts", maxPeerDiscoveryAttempts)
	}

	// Perform syncs with the found validator peer
	log.Printf("🔄 Starting sync process with validator peer %s", validatorPeer)

	// Sync blockchain
	log.Printf("🔄 Syncing blockchain with validator peer %s", validatorPeer)
	if err := node.SyncWithPeer(validatorPeer); err != nil {
		log.Printf("⚠️ Failed to sync blockchain with validator peer %s: %v", validatorPeer, err)
		return fmt.Errorf("blockchain sync failed: %v", err)
	}

	// Initialize stake pool if not already initialized
	if node.StakePool == nil {
		log.Printf("🔐 Initializing stake pool for miner node")
		node.StakePool = blockchain.NewStakePool(bc)
	}

	// Sync stake pool
	log.Printf("🔄 Syncing stake pool with validator peer %s", validatorPeer)
	if err := node.StakePool.SyncWithPeer(validatorPeer); err != nil {
		log.Printf("⚠️ Failed to sync stake pool with validator peer %s: %v", validatorPeer, err)
		return fmt.Errorf("stake pool sync failed: %v", err)
	}

	// Sync mempool
	log.Printf("🔄 Syncing mempool with validator peer %s", validatorPeer)
	if err := node.Mempool.SyncWithPeer(node, validatorPeer); err != nil {
		log.Printf("⚠️ Failed to sync mempool with validator peer %s: %v", validatorPeer, err)
		return fmt.Errorf("mempool sync failed: %v", err)
	}

	log.Printf("✅ All syncs completed successfully with validator peer %s", validatorPeer)

	// Start mining
	log.Printf("⛏️ Starting mining process...")
	go startMining(bc, wallet.Address)

	// Keep node running
	select {}
}

func runValidatorNode(config *NodeConfig, store *blockchain.Store) error {
	log.Printf("🚀 Starting validator node...")

	if config.ListenAddr == ":50505" {
		config.ListenAddr = ":50506" // Use different P2P port
		log.Printf("📡 Using alternate P2P port: %s to avoid conflicts", config.ListenAddr)
	}
	if config.RPCAddr == ":8545" {
		config.RPCAddr = ":8546" // Use different RPC port
		log.Printf("🌐 Using alternate RPC port: %s to avoid conflicts", config.RPCAddr)
	}

	// 1. First, setup wallet
	wallet, err := setupWallet(config.DataDir)
	if err != nil {
		return fmt.Errorf("failed to setup wallet: %v", err)
	}
	log.Printf("📝 Wallet address: %s", wallet.Address)

	// 2. Initialize blockchain database
	dbConfig := &blockchain.DatabaseConfig{
		Type:         "leveldb",
		Path:         filepath.Join(config.DataDir, "chaindata"),
		CacheSize:    256,
		MaxOpenFiles: 64,
		Compression:  true,
	}
	bc := blockchain.InitialiseBlockchain(dbConfig)

	// 3. Read bootnode address
	if len(config.BootstrapNodes) == 0 {
		bootnodeAddr, err := os.ReadFile("bootnode.addr")
		if err != nil {
			return fmt.Errorf("failed to read bootnode address: %v", err)
		}
		config.BootstrapNodes = []string{strings.TrimSpace(string(bootnodeAddr))}
		log.Printf("📡 Using bootnode address from file: %s", config.BootstrapNodes[0])
	}

	// 4. Initialize P2P network with validator-specific configuration
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
		ValidatorMode:  true, // Enable validator-specific features
	}

	// Create and start the node
	node, err := blockchain.NewNode(networkConfig)
	if err != nil {
		return fmt.Errorf("failed to create node: %v", err)
	}

	// Set the node in the blockchain BEFORE starting the node
	bc.Node = node
	log.Printf("🔗 Connected node to blockchain with ID: %s", node.Host.ID())

	// Additional debug logs
	log.Printf("🔍 Verifying blockchain components for validator node:")
	log.Printf("   • Blockchain UTXOPool: %v", bc.GetUTXOPool() != nil)
	log.Printf("   • Node UTXOPool: %v", node.UTXOPool != nil)
	log.Printf("   • Node AccountManager: %v", node.GetAccountManager() != nil)
	log.Printf("   • StakePool: %v", bc.StakePool != nil)

	// Start the node
	if err := node.Start(); err != nil {
		return fmt.Errorf("failed to start node: %v", err)
	}

	// Log node information
	log.Printf("🌐 P2P node initialized with ID: %s", node.Host.ID())
	log.Printf("📡 Listening on: %s", config.ListenAddr)

	// 5. Connect to bootstrap nodes with retries
	maxRetries := 5
	retryDelay := time.Second * 5
	connected := false

	for i := 0; i < maxRetries; i++ {
		log.Printf("📡 Attempt %d/%d: Connecting to bootstrap nodes", i+1, maxRetries)
		if err := node.ConnectToBootstrapNodes(context.Background()); err != nil {
			log.Printf("⚠️ Attempt %d/%d failed: %v", i+1, maxRetries, err)
			if i < maxRetries-1 {
				log.Printf("⏳ Waiting %v before next attempt...", retryDelay)
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
		return fmt.Errorf("failed to connect to bootstrap nodes after %d attempts", maxRetries)
	}

	// 6. Create validator instance
	validator, err := blockchain.NewValidator(bc, &blockchain.ValidatorConfig{
		Stake:        config.ValidatorStake,
		MinStake:     0.0,  // Set minimum stake requirement
		RewardRate:   0.01, // 1% reward rate
		SlashingRate: 0.5,  // 50% slashing for violations
		BlockTimeout: 30 * time.Second,
		MaxMissed:    10,
	}, wallet.Address)
	if err != nil {
		return fmt.Errorf("failed to create validator: %v", err)
	}
	log.Printf("✅ Validator instance created with stake: %.2f", config.ValidatorStake)

	// Ensure the validator is properly registered with the blockchain
	if bc.GetHeight() == 0 {
		log.Printf("🔐 Ensuring validator is properly registered with blockchain")
		// Force re-registration of the validator with the correct node ID
		if bc.Node != nil && bc.Node.Host != nil {
			nodeID := bc.Node.Host.ID().String()
			log.Printf("📡 Re-registering validator with node ID: %s", nodeID)
			if err := bc.StakePool.AddValidator(wallet.Address, config.ValidatorStake, nodeID); err != nil {
				log.Printf("⚠️ Failed to re-register validator: %v", err)
			} else {
				log.Printf("✅ Validator re-registered successfully with node ID: %s", nodeID)
			}
		}
	}

	// 7. Check if this is a new blockchain
	if bc.GetHeight() == 0 {
		log.Printf("🌟 No existing blockchain found. Initializing as first validator...")
		log.Printf("🌟 Creating genesis block...")

		if err := bc.InitializeChain(); err != nil {
			return fmt.Errorf("failed to initialize chain: %v", err)
		}

		displayGenesisBlock(bc, wallet.Address)
		node.SetInitializedValidator(true)
		log.Printf("🔐 Validator node activated and waiting for miner connections")
	} else {
		log.Printf("📊 Existing blockchain found at height: %d", bc.GetHeight())
		log.Printf("🔍 Validator will start validating from the current state")
	}

	// 8. Start validator process
	log.Printf("🚀 Starting validator process...")
	if err := validator.Start(); err != nil {
		return fmt.Errorf("failed to start validator: %v", err)
	}
	log.Printf("✅ Validator process started successfully")
	log.Printf("📡 Validator is now active and monitoring the network")
	log.Printf("💰 Current stake: %.2f", config.ValidatorStake)

	// 9. Keep the node running
	select {}
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
	case "txns":
		return RoleTXNS
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
	case RoleTXNS:
		return "txns Node"
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

		// Add 5 second delay between mining cycles
		log.Printf("⏳ Waiting 5 seconds before next mining cycle...")
		time.Sleep(5 * time.Second)
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
		RPCPort:        extractPort(config.RPCAddr),
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

// Update displayGenesisBlock to include validator address
func displayGenesisBlock(bc *blockchain.Blockchain, validatorAddr string) {
	genesis := bc.GetLatestBlock()
	log.Printf("📖 Genesis Block Details:")
	log.Printf("• Block Number: %d", genesis.Header.BlockNumber)
	log.Printf("• Hash: %s", genesis.Hash())
	log.Printf("• Previous Hash: %s", genesis.Header.PreviousHash)
	log.Printf("• Validator: %s", validatorAddr)
	log.Printf("• Timestamp: %s (%d)", time.Unix(genesis.Header.Timestamp, 0).Format(time.RFC3339), genesis.Header.Timestamp)
	log.Printf("• State Root: %s", genesis.Header.StateRoot)
	log.Printf("• Merkle Root: %s", genesis.Header.MerkleRoot)
	log.Printf("• Receipts Root: %s", genesis.Header.ReceiptsRoot)
	log.Printf("• Difficulty: %d", genesis.Header.Difficulty)
	log.Printf("• Gas Limit: %d", genesis.Header.GasLimit)
	log.Printf("• Gas Used: %d", genesis.Header.GasUsed)
	log.Printf("• Version: %d", genesis.Header.Version)
	log.Printf("• Transaction Count: %d", genesis.TransactionCount())
	log.Printf("• Block Size: %d bytes", genesis.Size())
}

func connectToBootstrapNodes(node *blockchain.Node, bootstrapNodes []string) error {
	log.Printf("🔄 Connecting to %d bootstrap nodes", len(bootstrapNodes))
	for _, addr := range bootstrapNodes {
		log.Printf(" ↳ Attempting connection to %s", addr)

		// Check if the address is already a valid multiaddr
		if !strings.HasPrefix(addr, "/") {
			// If it's just a peer ID, we need to construct a proper multiaddr
			// This is a fallback in case the full multiaddr wasn't provided
			log.Printf("⚠️ Invalid multiaddr format, attempting to fix: %s", addr)
			continue // Skip invalid addresses
		}

		if err := node.ConnectToPeer(addr); err != nil {
			log.Printf("⚠️ Failed to connect to bootstrap node %s: %v", addr, err)
		}
	}
	return nil
}

func runTXNS(config *NodeConfig, store *blockchain.Store) error {
	log.Printf("🌐 Starting TXNS Node(RPC Observer)")

	// Use different ports by default for TXNS when running on same machine as other nodes
	// Only override if not explicitly set through command-line
	if config.ListenAddr == ":50505" {
		config.ListenAddr = ":50506" // Use different P2P port
		log.Printf("📡 Using alternate P2P port: %s to avoid conflicts", config.ListenAddr)
	}
	if config.RPCAddr == ":8545" {
		config.RPCAddr = ":8546" // Use different RPC port
		log.Printf("🌐 Using alternate RPC port: %s to avoid conflicts", config.RPCAddr)
	}

	// Load or create persistent node key
	txnsKeyFile := filepath.Join(config.DataDir, "txnsnode.key")
	privKey, err := loadOrCreateTXNSKey(txnsKeyFile)
	if err != nil {
		log.Fatalf("❌ Failed to load or create TXNS node key: %v", err)
	}
	log.Printf("🔑 Loaded TXNS node private key from %s", txnsKeyFile)

	//Initialise DB
	dbConfig := &blockchain.DatabaseConfig{
		Type:         "leveldb",
		Path:         filepath.Join(config.DataDir, "chaindata"),
		CacheSize:    256,
		MaxOpenFiles: 64,
		Compression:  true,
	}

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
		DHTServerMode:  true,
		ObserverMode:   true,
	}

	// Create node with the private key
	node, err := blockchain.NewNodeWithPrivKey(networkConfig, privKey)
	if err != nil {
		log.Fatalf("❌ Failed to create node with private key: %v", err)
	}

	// Log the TXNS node ID and save it in the constant for skipping
	log.Printf("📝 TXNS Node created with ID: %s using persistent private key", node.Host.ID())
	log.Printf("🔄 Other nodes can skip this ID during peer discovery and block propagation")

	bc.Node = node
	log.Printf("🔗 Blockchain node connection established with ID: %s", node.Host.ID())

	// Additional debug logs
	log.Printf("🔍 Verifying blockchain components:")
	log.Printf("   • Blockchain UTXOPool: %v", bc.GetUTXOPool() != nil)
	log.Printf("   • Node UTXOPool: %v", node.UTXOPool != nil)
	log.Printf("   • Node AccountManager: %v", node.GetAccountManager() != nil)

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

	//Try peer discovery to find other nodes in the network connected to bootnode
	maxPeerDiscoveryAttempts := 4

	for i := 0; i < maxPeerDiscoveryAttempts; i++ {
		log.Printf("👥 Peer discovery attempt %d/%d", i+1, maxPeerDiscoveryAttempts)
		if err := node.DiscoverPeers(); err != nil {
			log.Printf("⚠️ Peer discovery attempt failed: %v", err)
		}

		log.Printf("Found %d peers", len(node.Host.Network().Peers()))
		for _, peer := range node.Host.Network().Peers() {
			log.Printf("Peer: %s", peer)
		}

		if i < maxPeerDiscoveryAttempts-1 {
			log.Printf("⏳ Waiting before next attempt...")
			time.Sleep(5 * time.Second)
		}

		if len(node.Host.Network().Peers()) > 1 {
			break
		}
	}

	//select random peer from available peers to sync with it
	peers := node.Host.Network().Peers()

	var candidates []peer.ID
	for _, p := range peers {
		if !node.IsPeerBootstrapNode(p) {
			candidates = append(candidates, p)
		}
	}

	// Check if we have any candidate peers for syncing
	if len(candidates) > 0 {
		selectedPeer := candidates[rand.Intn(len(candidates))]

		// Perform syncs with the found validator peer
		log.Printf("🔄 Starting sync process with validator peer %s", selectedPeer)

		// Sync blockchain
		log.Printf("🔄 Syncing blockchain with validator peer %s", selectedPeer)
		if err := node.SyncWithPeer(selectedPeer); err != nil {
			log.Printf("⚠️ Failed to sync blockchain with validator peer %s: %v", selectedPeer, err)
			return fmt.Errorf("blockchain sync failed: %v", err)
		}

		// Initialize stake pool if not already initialized
		if node.StakePool == nil {
			log.Printf("🔐 Initializing stake pool for miner node")
			node.StakePool = blockchain.NewStakePool(bc)
		}

		// Sync stake pool
		log.Printf("🔄 Syncing stake pool with validator peer %s", selectedPeer)
		if err := node.StakePool.SyncWithPeer(selectedPeer); err != nil {
			log.Printf("⚠️ Failed to sync stake pool with validator peer %s: %v", selectedPeer, err)
			return fmt.Errorf("stake pool sync failed: %v", err)
		}

		// Sync mempool
		log.Printf("🔄 Syncing mempool with validator peer %s", selectedPeer)
		if err := node.Mempool.SyncWithPeer(node, selectedPeer); err != nil {
			log.Printf("⚠️ Failed to sync mempool with validator peer %s: %v", selectedPeer, err)
			return fmt.Errorf("mempool sync failed: %v", err)
		}

		log.Printf("✅ All syncs completed with validator peer %s", selectedPeer)
	} else {
		log.Printf("⚠️ No suitable peers found for syncing. Starting shell anyway...")
	}

	// Start the TXNS shell
	log.Printf("🖥️ Starting TXNS shell interface...")
	startTXNSShell(node, bc)

	return nil
}

func startTXNSShell(node *blockchain.Node, bc *blockchain.Blockchain) {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("\nTXNS> ")
		scanner.Scan()
		input := strings.TrimSpace(scanner.Text())

		switch {
		case strings.HasPrefix(input, "create-wallet"):
			handleCreateWallet() //uses wallet.go file which has create wallet features and adds proper logs like setupWallet() function

		case strings.HasPrefix(input, "import-wallet"):
			handleImportWallet() //user inputs phrases and same uses recover function in wallet.go and recovers wallet with proper logs

		case strings.HasPrefix(input, "send-tx"):
			handleSendTransaction(input, bc, node) //user enter to address and amount to be sent and this functions uses transaction.go file and first validates the transaction like realblockchain and then it broadcasts the transaction and uses helper function to properly show the status of transaction incomplete or declined if transaction successfull it shows proper successfull log like in real blockchain (has user wallet is already created or restored for further when sender or any address is required it takes from there)

		case strings.HasPrefix(input, "balance"):
			handleGetBalance(input, bc) //this function returns the available using the utxo available locally or something

		case input == "exit":
			os.Exit(0)

		default:
			fmt.Println("Commands:")
			fmt.Println("  create-wallet - Create new wallet")
			fmt.Println("  import-wallet [12 phrase memonic] - Import wallet from mnemonic")
			fmt.Println("  send-tx [from] [to] [amount] - Send transaction")
			fmt.Println("  balance [address] - Check balance") //implement if any further commands are required [i guees to check gas also we need to add some command because we have implemented gas package too(like a gas market)]
			fmt.Println("  exit - Exit the node")
		}
	}
}

func handleCreateWallet() {
	// Create wallet directory if it doesn't exist
	walletDir := filepath.Join("data", "wallets")
	if err := os.MkdirAll(walletDir, 0755); err != nil {
		fmt.Printf("Error creating wallet directory: %v\n", err)
		return
	}

	// Create a new wallet
	wallet, err := blockchain.NewWallet()
	if err != nil {
		fmt.Printf("Error creating wallet: %v\n", err)
		return
	}

	// Save wallet to file
	walletFile := filepath.Join(walletDir, fmt.Sprintf("%s.wallet", wallet.Address))
	if err := wallet.SaveToFile(walletFile); err != nil {
		fmt.Printf("Error saving wallet: %v\n", err)
		return
	}

	fmt.Println("✅ New wallet created successfully!")
	fmt.Printf("🔑 Address: %s\n", wallet.Address)
	fmt.Printf("🔐 Mnemonic: %s\n", wallet.Mnemonic)
	fmt.Println("⚠️ IMPORTANT: Save your mnemonic phrase to recover your wallet!")
}

func handleImportWallet() {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Print("Enter your 12-word mnemonic phrase: ")
	scanner.Scan()
	mnemonic := strings.TrimSpace(scanner.Text())

	if mnemonic == "" {
		fmt.Println("❌ Mnemonic phrase cannot be empty")
		return
	}

	// Validate mnemonic has appropriate number of words
	words := strings.Fields(mnemonic)
	if len(words) != 12 && len(words) != 24 {
		fmt.Printf("❌ Invalid mnemonic: must have 12 or 24 words, got %d\n", len(words))
		return
	}

	// Create wallet directory if it doesn't exist
	walletDir := filepath.Join("data", "wallets")
	if err := os.MkdirAll(walletDir, 0755); err != nil {
		fmt.Printf("❌ Error creating wallet directory: %v\n", err)
		return
	}

	// Recover wallet from mnemonic
	wallet, err := blockchain.RecoverWalletFromMnemonic(mnemonic)
	if err != nil {
		fmt.Printf("❌ Error recovering wallet: %v\n", err)
		return
	}

	// Save wallet to file
	walletFile := filepath.Join(walletDir, fmt.Sprintf("%s.wallet", wallet.Address))
	if err := wallet.SaveToFile(walletFile); err != nil {
		fmt.Printf("❌ Error saving wallet: %v\n", err)
		return
	}

	fmt.Println("✅ Wallet recovered successfully!")
	fmt.Printf("🔑 Address: %s\n", wallet.Address)
}

func handleSendTransaction(input string, bc *blockchain.Blockchain, node *blockchain.Node) {
	parts := strings.Fields(input)
	if len(parts) < 4 {
		fmt.Println("❌ Usage: send-tx [from] [to] [amount]")
		return
	}

	fromAddress := parts[1]
	toAddress := parts[2]

	amount, err := strconv.ParseFloat(parts[3], 64)
	if err != nil {
		fmt.Printf("❌ Invalid amount: %v\n", err)
		return
	}

	if amount <= 0 {
		fmt.Println("❌ Amount must be greater than 0")
		return
	}

	// Verify the addresses are valid
	if !blockchain.ValidateAddress(fromAddress) {
		fmt.Println("❌ Invalid sender address")
		return
	}

	if !blockchain.ValidateAddress(toAddress) {
		fmt.Println("❌ Invalid receiver address")
		return
	}

	// Check if wallet file exists
	walletDir := filepath.Join("data", "wallets")
	walletFile := filepath.Join(walletDir, fmt.Sprintf("%s.wallet", fromAddress))

	wallet, err := blockchain.LoadWalletFromFile(walletFile)
	if err != nil {
		fmt.Printf("❌ Error loading wallet: %v\n", err)
		return
	}

	// Get UTXO pool
	utxoPool := bc.GetUTXOPool()
	if utxoPool == nil {
		fmt.Println("❌ UTXO pool not initialized")
		return
	}

	// Check balance
	balance := utxoPool.GetBalance(fromAddress)
	if balance < amount {
		fmt.Printf("❌ Insufficient balance. Available: %.8f, Required: %.8f\n", balance, amount)
		return
	}

	// Create transaction with default gas values
	fmt.Println("⏳ Creating transaction...")
	tx, err := blockchain.NewTransaction(fromAddress, toAddress, amount, 1000000000, 21000)
	if err != nil {
		fmt.Printf("❌ Error creating transaction: %v\n", err)
		return
	}

	// Select UTXOs for the transaction
	fmt.Println("🔍 Selecting UTXOs...")
	if err := tx.SelectUTXOs(utxoPool); err != nil {
		fmt.Printf("❌ Error selecting UTXOs: %v\n", err)
		return
	}

	// Sign transaction with sender's wallet
	fmt.Println("🔏 Signing transaction...")
	if err := wallet.SignTransaction(tx); err != nil {
		fmt.Printf("❌ Error signing transaction: %v\n", err)
		return
	}

	// Validate transaction
	fmt.Println("🔍 Validating transaction...")
	mempool := node.Mempool
	if mempool == nil {
		fmt.Println("❌ Mempool not initialized")
		return
	}

	// Get the UTXO map from the UTXO pool
	utxos := utxoPool.GetUTXOs()
	if !mempool.ValidateTransaction(*tx, utxos) {
		fmt.Println("❌ Transaction validation failed")
		return
	}

	// Broadcast transaction
	fmt.Println("📡 Broadcasting transaction to network...")
	if err := node.BroadcastTransaction(tx, nil); err != nil {
		fmt.Printf("❌ Error broadcasting transaction: %v\n", err)
		return
	}

	fmt.Println("✅ Transaction created and broadcast successfully!")
	fmt.Printf("📝 Transaction ID: %s\n", tx.TransactionID)
	fmt.Printf("🔹 From: %s\n", tx.Sender)
	fmt.Printf("🔹 To: %s\n", tx.Receiver)
	fmt.Printf("🔹 Amount: %.8f tokens\n", tx.Amount)
	fmt.Printf("🔹 Gas Fee: %.8f tokens\n", tx.GasFee)
	if len(tx.Outputs) > 1 {
		fmt.Printf("🔹 Change Amount: %.8f tokens\n", tx.Outputs[1].Amount)
	}
	fmt.Println("⏳ Status: Pending - Waiting for miners to include in a block")

	// Start a goroutine to track the transaction status
	go monitorTransaction(tx.TransactionID, fromAddress, bc, node)
}

// monitorTransaction checks periodically if a transaction has been included in a block
func monitorTransaction(txID, fromAddress string, bc *blockchain.Blockchain, node *blockchain.Node) {
	// Set up a ticker to check every 10 seconds
	ticker := time.NewTicker(10 * time.Second)
	timeout := time.After(5 * time.Minute) // Set a timeout of 5 minutes

	defer ticker.Stop()

	initialHeight := bc.GetHeight()
	fmt.Printf("\n⏳ Monitoring transaction %s for confirmation (current block height: %d)...\n", txID, initialHeight)
	fmt.Print("TXNS> ") // Re-print the prompt

	for {
		select {
		case <-ticker.C:
			// Get the UTXO pool (this should be updated when new blocks are added)
			utxoPool := bc.GetUTXOPool()
			if utxoPool == nil {
				continue
			}

			// Check current blockchain height
			currentHeight := bc.GetHeight()

			// If block height has increased, check for our transaction in confirmed blocks
			if currentHeight > initialHeight {
				// Get latest block
				latestBlock := bc.GetLatestBlock()

				// Check if our transaction is in the latest block
				for _, blockTx := range latestBlock.Body.Transactions.GetAllTransactions() {
					if blockTx.TransactionID == txID {
						// Transaction confirmed!
						fmt.Printf("\n🎉 Transaction confirmed!\n")
						fmt.Printf("📝 Transaction ID: %s\n", txID)
						fmt.Printf("🔹 Included in block #%d\n", latestBlock.Header.BlockNumber)
						fmt.Printf("🔹 Block hash: %s\n", latestBlock.Hash())
						fmt.Println("✅ Status: Confirmed")

						// Check new balance after confirmation
						newBalance := utxoPool.GetBalance(fromAddress)
						fmt.Printf("💰 New balance for %s: %.8f tokens\n", fromAddress, newBalance)
						fmt.Print("\nTXNS> ") // Re-print the prompt
						return
					}
				}

				// Update initial height for next check
				initialHeight = currentHeight
				fmt.Printf("\n⏳ New block #%d detected, but transaction not yet included. Continuing to monitor...\n", currentHeight)
				fmt.Print("TXNS> ") // Re-print the prompt
			}
		case <-timeout:
			fmt.Printf("\n⚠️ Monitoring timed out after 5 minutes\n")
			fmt.Println("The transaction might still be confirmed later")
			fmt.Print("TXNS> ") // Re-print the prompt
			return
		}
	}
}

func handleGetBalance(input string, bc *blockchain.Blockchain) {
	parts := strings.Fields(input)
	if len(parts) < 2 {
		fmt.Println("❌ Usage: balance [address]")
		return
	}

	address := parts[1]

	// Verify the address is valid
	if !blockchain.ValidateAddress(address) {
		fmt.Println("❌ Invalid address")
		return
	}

	// Get balance from UTXO pool
	utxoPool := bc.GetUTXOPool()
	if utxoPool == nil {
		fmt.Println("❌ UTXO pool not initialized")
		return
	}

	balance := utxoPool.GetBalance(address)
	fmt.Printf("💰 Balance for %s: %.8f tokens\n", address, balance)

	// Get UTXOs for this address
	utxos := utxoPool.GetUTXOsForAddress(address)
	fmt.Printf("📊 Found %d UTXO(s) for this address\n", len(utxos))

	if len(utxos) > 0 {
		fmt.Println("📝 UTXO Details:")
		for i, utxo := range utxos {
			fmt.Printf("  %d. Amount: %.8f, TxID: %s, Index: %d\n",
				i+1, utxo.Amount, utxo.TransactionID, utxo.OutputIndex)
		}
	}
}

// loadOrCreateTXNSKey loads a private key from the specified file
// or creates a new one if the file doesn't exist
func loadOrCreateTXNSKey(keyFile string) (crypto.PrivKey, error) {
	// Check if the key file exists
	_, err := os.Stat(keyFile)
	if err == nil {
		// Key file exists, load it
		keyBytes, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read key file: %v", err)
		}

		// Decode the private key
		privKey, err := crypto.UnmarshalPrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to decode private key: %v", err)
		}

		return privKey, nil
	}

	// Key file doesn't exist, create a new key
	privKey, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %v", err)
	}

	// Encode and save the private key
	keyBytes, err := crypto.MarshalPrivateKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encode private key: %v", err)
	}

	// Save key to file
	err = os.WriteFile(keyFile, keyBytes, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to write key file: %v", err)
	}

	return privKey, nil
}
