package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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
	role := flag.String("role", "observer", "Node role (bootstrap, miner, validator, observer)")
	dataDir := flag.String("datadir", "./node_data", "Data directory for the node")
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

func runMinerNode(config *NodeConfig, store *blockchain.Store) {
	log.Printf("⛏️ Starting Miner Node")

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

	// Create network configuration
	networkConfig := &blockchain.NetworkConfig{
		P2PPort:     extractPort(config.ListenAddr),
		RPCPort:     extractPort(config.RPCAddr),
		NetworkPath: filepath.Join(config.DataDir, "chaindata"),
		ChainID:     parseChainID(config.NetworkID),
		NetworkID:   config.NetworkID,
		Blockchain:  bc,
	}

	// Create the node
	node, err := blockchain.NewNode(networkConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create node: %v", err)
	}
	log.Printf("✅ Node created successfully", node)

	// Start sync service
	startSyncService(config, bc, store)
}

func runValidatorNode(config *NodeConfig, store *blockchain.Store) {
	log.Printf("🔐 Starting Validator Node")

	// Initialize blockchain
	dbConfig := &blockchain.DatabaseConfig{
		Type:      "leveldb",
		Path:      filepath.Join(config.DataDir, "blockchain"),
		CacheSize: 256,
	}
	bc := blockchain.InitialiseBlockchain(dbConfig)

	// Initialize validator with configuration
	validatorConfig := &blockchain.ValidatorConfig{
		Stake:        config.ValidatorStake,
		MinStake:     00,   // Example minimum stake
		RewardRate:   0.05, // 5% annual return
		SlashingRate: 0.10, // 10% slashing for misbehavior
	}

	validator, err := blockchain.NewValidator(bc, validatorConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create validator: %v", err)
	}

	// Start validation
	if err := validator.Start(); err != nil {
		log.Fatalf("❌ Failed to start validator: %v", err)
	}

	// Initialize and start sync service
	startSyncService(config, bc, store)
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
	syncConfig := &sync.SyncConfig{
		ListenAddr:     config.ListenAddr,
		BootstrapNodes: config.BootstrapNodes,
		NetworkID:      config.NetworkID,
		EnableMetrics:  config.EnableMetrics,
	}

	syncService := sync.NewSyncService(syncConfig, bc, store)
	if err := syncService.Start(config.ListenAddr); err != nil {
		log.Fatalf("❌ Failed to start sync service: %v", err)
	}

	log.Printf("🔄 Sync Service: Running on %s", config.ListenAddr)
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
