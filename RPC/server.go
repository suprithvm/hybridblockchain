package RPC

import (
	"blockchain-core/RPC/api"
	"blockchain-core/blockchain"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Config holds RPC server configuration
type Config struct {
	ListenAddr          string
	EnableCORS          bool
	EnableMetrics       bool
	EnableSubscriptions bool
	WebSocketAddr       string
}

// RPCMethodHandler is a function type for RPC method handlers
type RPCMethodHandler func(json.RawMessage) (interface{}, error)

// Subscription represents a client subscription to blockchain events
type Subscription struct {
	ID           string
	Type         string
	Filters      map[string]interface{}
	WebSocket    *websocket.Conn
	CreatedAt    time.Time
	LastActivity time.Time
	ClientIP     string
}

// SubscriptionManager manages active subscriptions
type SubscriptionManager struct {
	subscriptions       map[string]*Subscription
	clientSubscriptions map[string]map[string]bool // clientID -> map of subscription IDs
	maxPerClient        int
	mu                  sync.RWMutex
}

// NewSubscriptionManager creates a new subscription manager
func NewSubscriptionManager(maxPerClient int) *SubscriptionManager {
	return &SubscriptionManager{
		subscriptions:       make(map[string]*Subscription),
		clientSubscriptions: make(map[string]map[string]bool),
		maxPerClient:        maxPerClient,
	}
}

// AddSubscription adds a new subscription
func (sm *SubscriptionManager) AddSubscription(clientIP string, subType string, filters map[string]interface{}, ws *websocket.Conn) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if client has reached subscription limit
	if _, exists := sm.clientSubscriptions[clientIP]; !exists {
		sm.clientSubscriptions[clientIP] = make(map[string]bool)
	}

	if len(sm.clientSubscriptions[clientIP]) >= sm.maxPerClient {
		return "", fmt.Errorf("subscription limit reached for client")
	}

	// Generate a new subscription ID
	subID := uuid.New().String()

	// Create new subscription
	sub := &Subscription{
		ID:           subID,
		Type:         subType,
		Filters:      filters,
		WebSocket:    ws,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		ClientIP:     clientIP,
	}

	// Add to subscription maps
	sm.subscriptions[subID] = sub
	sm.clientSubscriptions[clientIP][subID] = true

	return subID, nil
}

// RemoveSubscription removes a subscription
func (sm *SubscriptionManager) RemoveSubscription(subID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sub, exists := sm.subscriptions[subID]
	if !exists {
		return false
	}

	// Remove from client subscriptions map
	if subs, ok := sm.clientSubscriptions[sub.ClientIP]; ok {
		delete(subs, subID)
		if len(subs) == 0 {
			delete(sm.clientSubscriptions, sub.ClientIP)
		}
	}

	// Remove from subscriptions map
	delete(sm.subscriptions, subID)
	return true
}

// GetSubscriptions returns all subscriptions for a client
func (sm *SubscriptionManager) GetSubscriptions(clientIP string) []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	subs, exists := sm.clientSubscriptions[clientIP]
	if !exists {
		return []string{}
	}

	result := make([]string, 0, len(subs))
	for subID := range subs {
		result = append(result, subID)
	}

	return result
}

// NotifySubscribers notifies subscribers of an event
func (sm *SubscriptionManager) NotifySubscribers(eventType string, data interface{}) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, sub := range sm.subscriptions {
		if sub.Type == eventType && sm.matchesFilters(sub, eventType, data) {
			// Create notification payload
			notification := map[string]interface{}{
				"method": "sup_subscription",
				"params": map[string]interface{}{
					"subscription_id": sub.ID,
					"result":          data,
				},
			}

			// Send notification to client
			if sub.WebSocket != nil && sub.WebSocket.WriteJSON(notification) == nil {
				sub.LastActivity = time.Now()
			}
		}
	}
}

// matchesFilters checks if an event matches subscription filters
func (sm *SubscriptionManager) matchesFilters(sub *Subscription, eventType string, data interface{}) bool {
	// If no filters, match all events of the requested type
	if sub.Filters == nil || len(sub.Filters) == 0 {
		return true
	}

	switch eventType {
	case "logs":
		// Match log events against address and topic filters
		if log, ok := data.(map[string]interface{}); ok {
			// Check address filter
			if address, hasAddress := sub.Filters["address"]; hasAddress {
				if logAddr, ok := log["address"].(string); ok && logAddr != address {
					return false
				}
			}

			// Check topics filter
			if topics, hasTopics := sub.Filters["topics"].([]interface{}); hasTopics {
				if logTopics, ok := log["topics"].([]interface{}); ok {
					// Simple topic matching - each filter topic must match corresponding log topic
					for i, filterTopic := range topics {
						if i >= len(logTopics) || filterTopic != logTopics[i] {
							return false
						}
					}
				}
			}
			return true
		}
	case "pending_transactions":
		// Match transaction events against address filter
		if tx, ok := data.(map[string]interface{}); ok {
			if address, hasAddress := sub.Filters["address"]; hasAddress {
				from, hasFrom := tx["sender"].(string)
				to, hasTo := tx["receiver"].(string)
				return (hasFrom && from == address) || (hasTo && to == address)
			}
			return true
		}
	}

	// Default: no filtering for other event types
	return true
}

// RPCServer represents a JSON-RPC server
type RPCServer struct {
	node            *blockchain.Node
	blockchain      *blockchain.Blockchain
	router          *mux.Router
	server          *http.Server
	methods         map[string]RPCMethodHandler
	config          *Config
	mu              sync.RWMutex
	logger          *log.Logger
	subscriptionMgr *SubscriptionManager
	upgrader        websocket.Upgrader
	wsConnections   map[*websocket.Conn]string // WebSocket -> client IP
	wsConnectionsMu sync.RWMutex
}

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      interface{}     `json:"id"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// RPCError represents a JSON-RPC 2.0 error object
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
	ErrServerError    = -32000
)

// Start starts the RPC server
func (s *RPCServer) Start() error {
	s.logger.Printf("Starting RPC server on %s", s.config.ListenAddr)

	// Start subscription cleanup routine if subscriptions are enabled
	if s.config.EnableSubscriptions {
		go s.cleanupStaleSubscriptions()
	}

	return s.server.ListenAndServe()
}

// cleanupStaleSubscriptions periodically removes stale subscriptions
func (s *RPCServer) cleanupStaleSubscriptions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.subscriptionMgr.mu.Lock()

			now := time.Now()
			staleThreshold := 30 * time.Minute

			// Find stale subscriptions
			for id, sub := range s.subscriptionMgr.subscriptions {
				if now.Sub(sub.LastActivity) > staleThreshold {
					// Remove from client subscriptions map
					if subs, ok := s.subscriptionMgr.clientSubscriptions[sub.ClientIP]; ok {
						delete(subs, id)
						if len(subs) == 0 {
							delete(s.subscriptionMgr.clientSubscriptions, sub.ClientIP)
						}
					}

					// Remove from subscriptions map
					delete(s.subscriptionMgr.subscriptions, id)

					s.logger.Printf("Removed stale subscription %s for client %s", id, sub.ClientIP)
				}
			}

			s.subscriptionMgr.mu.Unlock()
		}
	}
}

// Stop stops the RPC server
func (s *RPCServer) Stop() error {
	s.logger.Printf("Stopping RPC server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// registerMethod registers an RPC method handler
func (s *RPCServer) registerMethod(name string, handler RPCMethodHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[name] = handler
	s.logger.Printf("Registered RPC method: %s", name)
}

// handleRPCRequest handles JSON-RPC requests
func (s *RPCServer) handleRPCRequest(w http.ResponseWriter, r *http.Request) {
	// Set JSON content type
	w.Header().Set("Content-Type", "application/json")

	// Parse request
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, nil, -32700, "Parse error", err)
		return
	}

	// Validate JSON-RPC version
	if req.JSONRPC != "2.0" {
		s.writeError(w, req.ID, -32600, "Invalid Request", "Expected JSON-RPC 2.0")
		return
	}

	// Find method handler
	s.mu.RLock()
	handler, exists := s.methods[req.Method]
	s.mu.RUnlock()

	if !exists {
		s.writeError(w, req.ID, -32601, "Method not found", fmt.Sprintf("Method '%s' not found", req.Method))
		return
	}

	// Check if this is a subscription method that requires context
	subscriptionMethods := map[string]bool{
		"sup_subscribe":        true,
		"sup_unsubscribe":      true,
		"sup_getSubscriptions": true,
	}

	var params interface{} = req.Params

	// Add client context for subscription methods
	if subscriptionMethods[req.Method] && s.config.EnableSubscriptions {
		// Create extended context with client information
		extendedContext := &jsonExtendedContext{
			RawMessage: req.Params,
			ClientIP:   r.RemoteAddr,
			// WebSocket will be nil for HTTP requests,
			// subscription methods will check and return appropriate error
		}
		params = extendedContext
	}

	// Execute method
	result, err := handler(params.(json.RawMessage))
	if err != nil {
		s.writeError(w, req.ID, -32603, "Internal error", err)
		return
	}

	// Send successful response
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Printf("Error encoding response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// writeError writes a JSON-RPC error response
func (s *RPCServer) writeError(w http.ResponseWriter, id interface{}, code int, message string, data interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Printf("Error encoding error response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleHealthCheck handles health check requests
func (s *RPCServer) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Basic health check
	health := map[string]interface{}{
		"status":      "up",
		"timestamp":   time.Now().Unix(),
		"connections": len(s.node.Host.Network().Peers()),
		"blockHeight": s.blockchain.GetHeight(),
	}

	json.NewEncoder(w).Encode(health)
}

// handleMetrics handles metrics requests
func (s *RPCServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Only enable metrics if configured
	if !s.config.EnableMetrics {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Metrics are disabled",
		})
		return
	}

	metrics := map[string]interface{}{
		"uptime":       time.Now().Unix(), // Just show current time, we don't have access to start time
		"peers":        len(s.node.Host.Network().Peers()),
		"blocks":       s.blockchain.GetHeight(),
		"transactions": len(s.node.Mempool.GetTransactions()),
	}

	json.NewEncoder(w).Encode(metrics)
}

// corsMiddleware adds CORS headers to responses
func (s *RPCServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Call the next handler
		next.ServeHTTP(w, r)
	})
}

// NewRPCServer creates a new RPC server instance
func NewRPCServer(node *blockchain.Node, bc *blockchain.Blockchain, config *Config) *RPCServer {
	server := &RPCServer{
		node:            node,
		blockchain:      bc,
		config:          config,
		logger:          log.New(os.Stdout, "[RPC] ", log.LstdFlags),
		methods:         make(map[string]RPCMethodHandler),
		subscriptionMgr: NewSubscriptionManager(10), // Max 10 subscriptions per client
		wsConnections:   make(map[*websocket.Conn]string),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				// Allow all origins in this example
				// In production, this should be more restrictive
				return true
			},
		},
	}

	// Register all API methods
	server.registerAllHandlers()

	// Create router
	router := mux.NewRouter()
	router.HandleFunc("/", server.handleRPCRequest).Methods("POST")
	router.HandleFunc("/health", server.handleHealthCheck).Methods("GET")

	// Add WebSocket endpoint if subscriptions are enabled
	if config.EnableSubscriptions {
		router.HandleFunc("/ws", server.handleWebSocket)

		// Setup blockchain event subscriptions
		server.setupBlockchainSubscriptions()
	}

	// Add metrics endpoint if enabled
	if config.EnableMetrics {
		router.HandleFunc("/metrics", server.handleMetrics).Methods("GET")
	}

	// Add CORS middleware if enabled
	if config.EnableCORS {
		router.Use(server.corsMiddleware)
	}

	server.router = router
	server.server = &http.Server{
		Addr:         config.ListenAddr,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server
}

// ServeHTTP implements the http.Handler interface
func (s *RPCServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// registerAllHandlers registers all RPC API handlers
func (s *RPCServer) registerAllHandlers() {
	// Initialize API modules
	walletAPI := api.NewWalletAPI(s.node, s.blockchain)
	blockchainAPI := api.NewBlockchainAPI(s.node, s.blockchain)
	transactionAPI := api.NewTransactionAPI(s.node, s.blockchain)
	networkAPI := api.NewNetworkAPI(s.node, s.blockchain)
	validatorAPI := api.NewValidatorAPI(s.node, s.blockchain)

	// Register Wallet API methods
	s.registerMethod("createWallet", walletAPI.CreateWallet)
	s.registerMethod("importWallet", walletAPI.ImportWallet)
	s.registerMethod("getWalletInfo", walletAPI.GetWalletInfo)
	s.registerMethod("createHDWallet", walletAPI.CreateHDWallet)
	s.registerMethod("getAddresses", walletAPI.GetAddresses)
	s.registerMethod("createMultiSigWallet", walletAPI.CreateMultiSigWallet)
	s.registerMethod("signMessage", walletAPI.SignMessage)
	s.registerMethod("verifySignature", walletAPI.VerifySignature)

	// Register Blockchain API methods
	s.registerMethod("getBlockByHash", blockchainAPI.GetBlockByHash)
	s.registerMethod("getBlockByHeight", blockchainAPI.GetBlockByHeight)
	s.registerMethod("getBlockCount", blockchainAPI.GetBlockCount)
	s.registerMethod("getChainInfo", blockchainAPI.GetChainInfo)
	s.registerMethod("getValidationInfo", blockchainAPI.GetValidationInfo)
	s.registerMethod("getBlockRange", blockchainAPI.GetBlockRange)
	s.registerMethod("getHashRate", blockchainAPI.GetHashRate)
	s.registerMethod("getNetworkDifficulty", blockchainAPI.GetNetworkDifficulty)
	s.registerMethod("getCirculatingSupply", blockchainAPI.GetCirculatingSupply)
	s.registerMethod("getBlockTransactions", blockchainAPI.GetBlockTransactions)
	s.registerMethod("getRichList", blockchainAPI.GetRichList)
	s.registerMethod("getBlockchainStats", blockchainAPI.GetBlockchainStats)
	s.registerMethod("getBlockTime", blockchainAPI.GetBlockTime)
	s.registerMethod("getStateRoot", blockchainAPI.GetStateRoot)
	s.registerMethod("getBlockHeaders", blockchainAPI.GetBlockHeaders)
	s.registerMethod("validateAddress", blockchainAPI.ValidateAddress)
	s.registerMethod("exportState", blockchainAPI.ExportState)
	s.registerMethod("getStateProof", blockchainAPI.GetStateProof)

	// Register Transaction API methods
	s.registerMethod("getBalance", transactionAPI.GetBalance)
	s.registerMethod("getUTXOs", transactionAPI.GetUTXOs)
	s.registerMethod("getAccountState", transactionAPI.GetAccountState)
	s.registerMethod("createUnsignedTransaction", transactionAPI.CreateUnsignedTransaction)
	s.registerMethod("createTransaction", transactionAPI.CreateUnsignedTransaction) // Alias for backward compatibility
	s.registerMethod("sendTransaction", transactionAPI.SendTransaction)
	s.registerMethod("getTransaction", transactionAPI.GetTransaction)
	s.registerMethod("getPendingTransactions", transactionAPI.GetPendingTransactions)
	s.registerMethod("estimateFee", transactionAPI.EstimateFee)
	s.registerMethod("getTransactionHistory", transactionAPI.GetTransactionHistory)
	s.registerMethod("getTransactionProof", transactionAPI.GetTransactionProof)
	s.registerMethod("decodeTransaction", transactionAPI.DecodeTransaction)
	s.registerMethod("debugTransaction", transactionAPI.DebugTransaction)
	s.registerMethod("callReadOnly", transactionAPI.CallReadOnly)
	s.registerMethod("getRecentTransactions", transactionAPI.GetRecentTransactions)
	s.registerMethod("getMempoolInfo", transactionAPI.GetMempoolInfo)
	s.registerMethod("getAverageFees", transactionAPI.GetAverageFees)
	s.registerMethod("traceBlock", transactionAPI.TraceBlock)
	s.registerMethod("searchByAddress", transactionAPI.SearchByAddress)

	// Register Network API methods
	s.registerMethod("getPeerInfo", networkAPI.GetPeerInfo)
	s.registerMethod("getNetworkStats", networkAPI.GetNetworkStats)
	s.registerMethod("addPeer", networkAPI.AddPeer)
	s.registerMethod("getNodeStatus", networkAPI.GetNodeStatus)
	s.registerMethod("syncStatus", networkAPI.GetSyncStatus)
	s.registerMethod("getProtocolVersion", networkAPI.GetProtocolVersion)
	s.registerMethod("getNodePerformance", networkAPI.GetNodePerformance)
	s.registerMethod("getBootnodeInfo", networkAPI.GetBootnodeInfo)
	s.registerMethod("getPeerLatency", networkAPI.GetPeerLatency)
	s.registerMethod("getBandwidthUsage", networkAPI.GetBandwidthUsage)
	s.registerMethod("getNetworkGrowth", networkAPI.GetNetworkGrowth)
	s.registerMethod("getActiveAddresses", networkAPI.GetActiveAddresses)
	s.registerMethod("getPeerCount", networkAPI.GetPeerCount)
	s.registerMethod("getSlashingEvents", networkAPI.GetSlashingEvents)
	s.registerMethod("getValidatorUptime", networkAPI.GetValidatorUptime)
	s.registerMethod("getDailyTransactionVolume", networkAPI.GetDailyTransactionVolume)

	// Register Validator API methods
	s.registerMethod("getValidators", validatorAPI.GetValidators)
	s.registerMethod("getStakeInfo", validatorAPI.GetStakeInfo)
	s.registerMethod("stakeTokens", validatorAPI.StakeTokens)
	s.registerMethod("unstakeTokens", validatorAPI.UnstakeTokens)
	s.registerMethod("getValidatorRewards", validatorAPI.GetValidatorRewards)
	s.registerMethod("getValidatorPerformance", validatorAPI.GetValidatorPerformance)
	s.registerMethod("verifyValidator", validatorAPI.VerifyValidator)
	s.registerMethod("getTopValidators", validatorAPI.GetTopValidators)
	s.registerMethod("getTotalStaked", validatorAPI.GetTotalStaked)
	s.registerMethod("getValidatorStats", validatorAPI.GetValidatorStats)
	s.registerMethod("getDailyValidatorRewards", validatorAPI.GetDailyValidatorRewards)

	// Register Subscription API methods
	if s.config.EnableSubscriptions {
		s.registerMethod("sup_subscribe", s.handleSupSubscribeWrapper)
		s.registerMethod("sup_unsubscribe", s.handleSupUnsubscribeWrapper)
		s.registerMethod("sup_getSubscriptions", s.handleSupGetSubscriptionsWrapper)
	}

	// Register internal handlers as alternatives/fallbacks
	s.registerMethod("_internal_getBlockCount", s.internalGetBlockCount)
	s.registerMethod("_internal_getBlockByHash", s.internalGetBlockByHash)
	s.registerMethod("_internal_getBlockByHeight", s.internalGetBlockByHeight)
	s.registerMethod("_internal_getChainInfo", s.internalGetChainInfo)
	s.registerMethod("_internal_getBalance", s.internalGetBalance)
	s.registerMethod("_internal_getUTXOs", s.internalGetUTXOs)
	s.registerMethod("_internal_sendTransaction", s.internalSendTransaction)
	s.registerMethod("_internal_getTransaction", s.internalGetTransaction)
	s.registerMethod("_internal_getPendingTransactions", s.internalGetPendingTransactions)
	s.registerMethod("_internal_estimateFee", s.internalEstimateFee)
	s.registerMethod("_internal_getTransactionHistory", s.internalGetTransactionHistory)
	s.registerMethod("_internal_getPeerInfo", s.internalGetPeerInfo)
	s.registerMethod("_internal_getNetworkStats", s.internalGetNetworkStats)
	s.registerMethod("_internal_getNodeStatus", s.internalGetNodeStatus)
	s.registerMethod("_internal_getAccountState", s.internalGetAccountState)
	s.registerMethod("_internal_createWallet", s.internalCreateWallet)
	s.registerMethod("_internal_importWallet", s.internalImportWallet)
	s.registerMethod("_internal_createHDWallet", s.internalCreateHDWallet)
	s.registerMethod("_internal_getAddresses", s.internalGetAddresses)
	s.registerMethod("_internal_createMultiSigWallet", s.internalCreateMultiSigWallet)
	s.registerMethod("_internal_getValidators", s.internalGetValidators)
	s.registerMethod("_internal_getStakeInfo", s.internalGetStakeInfo)
}

// Wrappers for subscription handlers to match RPCMethodHandler type
func (s *RPCServer) handleSupSubscribeWrapper(params json.RawMessage) (interface{}, error) {
	// This will be properly handled in handleRPCRequest where we set the extended context
	return s.handleSupSubscribe(params)
}

func (s *RPCServer) handleSupUnsubscribeWrapper(params json.RawMessage) (interface{}, error) {
	// This will be properly handled in handleRPCRequest where we set the extended context
	return s.handleSupUnsubscribe(params)
}

func (s *RPCServer) handleSupGetSubscriptionsWrapper(params json.RawMessage) (interface{}, error) {
	// This will be properly handled in handleRPCRequest where we set the extended context
	return s.handleSupGetSubscriptions(params)
}

// Example handler implementations

// handleGetBlockCount returns the current block height
func (s *RPCServer) handleGetBlockCount(params json.RawMessage) (interface{}, *RPCError) {
	return s.blockchain.GetHeight(), nil
}

// handleGetBlockByHash returns a block by its hash
func (s *RPCServer) handleGetBlockByHash(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Hash string `json:"hash"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	block, err := s.blockchain.GetBlock(args.Hash)
	if err != nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Block not found", Data: err.Error()}
	}

	return block, nil
}

// handleGetBlockByHeight returns a block by its height
func (s *RPCServer) handleGetBlockByHeight(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Height uint64 `json:"height"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	block := s.blockchain.GetBlockByHeight(args.Height)
	if block == nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Block not found", Data: nil}
	}

	return block, nil
}

// handleGetChainInfo returns information about the blockchain
func (s *RPCServer) handleGetChainInfo(params json.RawMessage) (interface{}, *RPCError) {
	latestBlock := s.blockchain.GetLatestBlock()

	return map[string]interface{}{
		"blocks":        s.blockchain.GetHeight(),
		"bestblockhash": latestBlock.Hash(),
		"difficulty":    latestBlock.Header.Difficulty,
		"mediantime":    latestBlock.Header.Timestamp,
		"chainwork":     latestBlock.CumulativeDifficulty,
	}, nil
}

// handleGetBalance returns the balance for an address
func (s *RPCServer) handleGetBalance(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	// Use UTXOPool directly to get balance
	balance := s.node.UTXOPool.GetBalance(args.Address)
	return balance, nil
}

// handleGetUTXOs returns UTXOs for an address
func (s *RPCServer) handleGetUTXOs(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	utxos := s.node.UTXOPool.GetUTXOsForAddress(args.Address)
	return utxos, nil
}

// The rest of the handler implementations will need to be added...
// For now, implement stubs that return errors for unimplemented methods

// handleSendTransaction sends a new transaction
func (s *RPCServer) handleSendTransaction(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		From           string  `json:"from"`
		To             string  `json:"to"`
		Amount         float64 `json:"amount"`
		GasPrice       uint64  `json:"gasPrice,omitempty"`
		GasLimit       uint64  `json:"gasLimit,omitempty"`
		Signature      string  `json:"signature"`
		RawTransaction string  `json:"rawTransaction"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	var tx *blockchain.Transaction

	// Handle pre-signed transaction
	if args.RawTransaction != "" {
		// Deserialize the complete transaction
		txBytes, err := hex.DecodeString(args.RawTransaction)
		if err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid transaction encoding", Data: err.Error()}
		}

		tx = &blockchain.Transaction{}
		if err := json.Unmarshal(txBytes, tx); err != nil {
			return nil, &RPCError{Code: ErrServerError, Message: "Failed to deserialize transaction", Data: err.Error()}
		}

		// Verify the signature
		if !tx.VerifySignature() {
			return nil, &RPCError{Code: ErrServerError, Message: "Transaction signature verification failed", Data: nil}
		}
	} else {
		// We need signature for non-raw transactions
		if args.Signature == "" {
			return nil, &RPCError{Code: ErrInvalidParams, Message: "Transaction signature is required", Data: nil}
		}

		// Create unsigned transaction
		var err error
		tx, err = blockchain.NewTransaction(args.From, args.To, args.Amount, args.GasPrice, args.GasLimit)
		if err != nil {
			return nil, &RPCError{Code: ErrServerError, Message: "Transaction creation failed", Data: err.Error()}
		}

		// Apply the provided signature
		tx.Signature = args.Signature

		// Verify the signature
		if !tx.VerifySignature() {
			return nil, &RPCError{Code: ErrServerError, Message: "Transaction signature verification failed", Data: nil}
		}
	}

	// Broadcast transaction
	err := s.node.BroadcastTransaction(tx, nil)
	if err != nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Broadcasting transaction failed", Data: err.Error()}
	}

	// Return comprehensive transaction data
	return map[string]interface{}{
		"txid":          tx.TransactionID,
		"status":        "success",
		"from":          tx.Sender,
		"to":            tx.Receiver,
		"amount":        tx.Amount,
		"timestamp":     tx.Timestamp,
		"gasPrice":      tx.GasPrice,
		"gasLimit":      tx.GasLimit,
		"inMempool":     true,
		"confirmations": 0,
	}, nil
}

// Implement proper transaction retrieval
func (s *RPCServer) handleGetTransaction(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		TxID string `json:"txid"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	if args.TxID == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Transaction ID is required", Data: nil}
	}

	// First, check the mempool for pending transactions
	mempoolTxs := s.node.Mempool.GetTransactions()
	for _, tx := range mempoolTxs {
		if tx.TransactionID == args.TxID {
			return map[string]interface{}{
				"txid":      tx.TransactionID,
				"sender":    tx.Sender,
				"receiver":  tx.Receiver,
				"amount":    tx.Amount,
				"timestamp": tx.Timestamp,
				"gasFee":    tx.GasFee,
				"gasLimit":  tx.GasLimit,
				"gasPrice":  tx.GasPrice,
				"gasUsed":   tx.GasUsed,
				"status":    "pending",
				"confirmed": false,
			}, nil
		}
	}

	// If not in mempool, search in the blockchain
	// Start with the latest block and go backwards
	height := s.blockchain.GetHeight()

	// Limit search to prevent excessive load
	maxSearchHeight := uint64(1000)
	if height > maxSearchHeight {
		height = maxSearchHeight
	}

	for i := height; i > 0; i-- {
		block := s.blockchain.GetBlockByHeight(i)
		if block == nil || block.Body == nil || block.Body.Transactions == nil {
			continue
		}

		txs := block.Body.Transactions.GetAllTransactions()
		for _, tx := range txs {
			if tx.TransactionID == args.TxID {
				return map[string]interface{}{
					"txid":        tx.TransactionID,
					"sender":      tx.Sender,
					"receiver":    tx.Receiver,
					"amount":      tx.Amount,
					"timestamp":   tx.Timestamp,
					"gasFee":      tx.GasFee,
					"gasLimit":    tx.GasLimit,
					"gasPrice":    tx.GasPrice,
					"gasUsed":     tx.GasUsed,
					"status":      "confirmed",
					"confirmed":   true,
					"blockHeight": block.Header.BlockNumber,
					"blockHash":   block.Hash(),
					"blockTime":   block.Header.Timestamp,
				}, nil
			}
		}
	}

	return nil, &RPCError{Code: ErrServerError, Message: "Transaction not found", Data: nil}
}

func (s *RPCServer) handleGetPendingTransactions(params json.RawMessage) (interface{}, *RPCError) {
	return s.node.Mempool.GetTransactions(), nil
}

func (s *RPCServer) handleEstimateFee(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		From     string  `json:"from"`
		To       string  `json:"to"`
		Amount   float64 `json:"amount"`
		GasPrice uint64  `json:"gasPrice,omitempty"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	if args.From == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Sender address is required", Data: nil}
	}
	if args.To == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Receiver address is required", Data: nil}
	}
	if args.Amount <= 0 {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Amount must be greater than 0", Data: nil}
	}

	// Set default gas price if not provided
	gasPrice := args.GasPrice
	if gasPrice == 0 {
		gasPrice = blockchain.DefaultGasPrice
	}

	// Default gas limit for a simple transfer
	gasLimit := uint64(blockchain.DefaultGasLimit)

	// Get gas model if available for more accurate estimates
	gasUsed := gasLimit
	if s.node.GetGasModel() != nil {
		// For more complex transactions (with data), we'd estimate higher
		if len(args.To) > 40 || len(args.From) > 40 { // Longer addresses might indicate contracts
			gasLimit = gasLimit * 2 // Double the gas for more complex operations
		}

		// Estimate gas usage based on network conditions
		if len(s.node.Host.Network().Peers()) > 10 {
			// Slightly higher gas when network is busy
			gasLimit += gasLimit / 10
		}

		gasUsed = gasLimit
	}

	// Calculate fee using gas price and limit
	fee := blockchain.ConvertGasToTokens(gasUsed * gasPrice)

	// Get recent transactions to provide fee suggestions
	transactions := s.node.Mempool.GetTransactions()

	// Calculate average, low, and high fees from recent transactions
	var totalFee, totalSize, lowFee, highFee float64
	var count int

	if len(transactions) > 0 {
		lowFee = transactions[0].GasFee
		highFee = transactions[0].GasFee

		for _, tx := range transactions {
			totalFee += tx.GasFee
			// Estimated size: approximately 250 bytes per transaction
			totalSize += 250
			count++

			if tx.GasFee < lowFee {
				lowFee = tx.GasFee
			}
			if tx.GasFee > highFee {
				highFee = tx.GasFee
			}
		}
	} else {
		// Default values if no transactions in mempool
		lowFee = fee * 0.8
		highFee = fee * 1.2
	}

	// Calculate average fee if we have transactions
	avgFee := fee
	if count > 0 {
		avgFee = totalFee / float64(count)
	}

	result := map[string]interface{}{
		"gasLimit": gasLimit,
		"gasPrice": gasPrice,
		"fee":      fee,
		"feeRecommendations": map[string]interface{}{
			"low":     lowFee,
			"average": avgFee,
			"high":    highFee,
		},
		"unit": "tokens",
	}

	return result, nil
}

func (s *RPCServer) handleGetTransactionHistory(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Address  string `json:"address"`
		Limit    int    `json:"limit,omitempty"`
		Offset   int    `json:"offset,omitempty"`
		SortDesc bool   `json:"sortDesc,omitempty"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	if args.Address == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Address is required", Data: nil}
	}

	// Set default values for pagination
	if args.Limit <= 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100 // Cap at 100 to prevent excessive load
	}

	// Collect transactions for the address
	var transactions []map[string]interface{}

	// First, check pending transactions in the mempool
	mempoolTxs := s.node.Mempool.GetTransactions()
	for _, tx := range mempoolTxs {
		if tx.Sender == args.Address || tx.Receiver == args.Address {
			txInfo := map[string]interface{}{
				"txid":      tx.TransactionID,
				"sender":    tx.Sender,
				"receiver":  tx.Receiver,
				"amount":    tx.Amount,
				"timestamp": tx.Timestamp,
				"gasFee":    tx.GasFee,
				"status":    "pending",
				"confirmed": false,
			}

			// Set transaction type based on address match
			if args.Address == tx.Sender {
				txInfo["type"] = "send"
			} else {
				txInfo["type"] = "receive"
			}

			transactions = append(transactions, txInfo)
		}
	}

	// Then, search in blockchain blocks
	height := s.blockchain.GetHeight()

	// Limit search to prevent excessive load
	maxSearchHeight := uint64(1000)
	if height > maxSearchHeight {
		height = maxSearchHeight
	}

	for i := height; i > 0; i-- {
		block := s.blockchain.GetBlockByHeight(i)
		if block == nil || block.Body == nil || block.Body.Transactions == nil {
			continue
		}

		txs := block.Body.Transactions.GetAllTransactions()
		for _, tx := range txs {
			if tx.Sender == args.Address || tx.Receiver == args.Address {
				txInfo := map[string]interface{}{
					"txid":        tx.TransactionID,
					"sender":      tx.Sender,
					"receiver":    tx.Receiver,
					"amount":      tx.Amount,
					"timestamp":   tx.Timestamp,
					"gasFee":      tx.GasFee,
					"status":      "confirmed",
					"confirmed":   true,
					"blockHeight": block.Header.BlockNumber,
					"blockHash":   block.Hash(),
				}

				// Set transaction type based on address match
				if args.Address == tx.Sender {
					txInfo["type"] = "send"
				} else {
					txInfo["type"] = "receive"
				}

				transactions = append(transactions, txInfo)
			}
		}

		// If we've collected enough transactions, stop searching
		if len(transactions) >= args.Limit+args.Offset {
			break
		}
	}

	// Sort transactions by timestamp
	sort.Slice(transactions, func(i, j int) bool {
		timeI, _ := transactions[i]["timestamp"].(int64)
		timeJ, _ := transactions[j]["timestamp"].(int64)
		if args.SortDesc {
			return timeI > timeJ
		}
		return timeI < timeJ
	})

	// Apply pagination
	totalCount := len(transactions)
	startIdx := args.Offset
	endIdx := args.Offset + args.Limit

	if startIdx >= totalCount {
		transactions = []map[string]interface{}{}
	} else {
		if endIdx > totalCount {
			endIdx = totalCount
		}
		transactions = transactions[startIdx:endIdx]
	}

	return map[string]interface{}{
		"address":      args.Address,
		"transactions": transactions,
		"total":        totalCount,
	}, nil
}

func (s *RPCServer) handleGetPeerInfo(params json.RawMessage) (interface{}, *RPCError) {
	peers := s.node.Host.Network().Peers()
	peerInfo := make([]map[string]interface{}, 0, len(peers))

	for _, p := range peers {
		info := map[string]interface{}{
			"id":          p.String(),
			"address":     s.node.Host.Peerstore().Addrs(p),
			"isBootstrap": s.node.IsPeerBootstrapNode(p),
		}
		peerInfo = append(peerInfo, info)
	}

	return peerInfo, nil
}

func (s *RPCServer) handleGetNetworkStats(params json.RawMessage) (interface{}, *RPCError) {
	peers := s.node.Host.Network().Peers()

	// Collect basic network stats
	stats := map[string]interface{}{
		"peerCount":   len(peers),
		"nodeID":      s.node.Host.ID().String(),
		"listenAddrs": s.node.Host.Addrs(),
		"protocols":   s.node.Host.Mux().Protocols(),
		"connections": len(s.node.Host.Network().Conns()),
		"isSyncing":   s.node.IsSyncing(),
	}

	// Calculate connections per protocol if available
	if s.node.Host.Peerstore() != nil {
		protocolCounts := make(map[string]int)
		for _, p := range peers {
			protocols, err := s.node.Host.Peerstore().GetProtocols(p)
			if err == nil {
				for _, proto := range protocols {
					protocolCounts[string(proto)]++
				}
			}
		}
		stats["protocolUsage"] = protocolCounts
	}

	// Get network latency statistics
	if len(peers) > 0 {
		// We'll calculate average latency as an example
		// In a real implementation, we would use more accurate ping measurements
		totalLatency := int64(0)
		peerLatencies := make(map[string]int64)

		for _, p := range peers {
			// This is a placeholder - in a real implementation you would have
			// actual ping measurements
			latency := int64(100) // Default 100ms latency
			peerLatencies[p.String()] = latency
			totalLatency += latency
		}

		stats["averageLatency"] = totalLatency / int64(len(peers))
		stats["peerLatencies"] = peerLatencies
	}

	return stats, nil
}

func (s *RPCServer) handleGetNodeStatus(params json.RawMessage) (interface{}, *RPCError) {
	return map[string]interface{}{
		"synced":      !s.node.IsSyncing(),
		"peerCount":   len(s.node.Host.Network().Peers()),
		"currentTime": time.Now().Unix(),
		"nodeID":      s.node.Host.ID().String(),
	}, nil
}

func (s *RPCServer) handleGetAccountState(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	if args.Address == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Address is required", Data: nil}
	}

	// Validate address format
	if !blockchain.ValidateAddress(args.Address) {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid address format", Data: nil}
	}

	// Get UTXOs directly from UTXOPool
	utxos := s.node.UTXOPool.GetUTXOsForAddress(args.Address)

	// Calculate balance from UTXOs
	balance := s.node.UTXOPool.GetBalance(args.Address)

	// In UTXO model, nonce can be derived from transaction count or UTXO count
	// Using the length of UTXOs as an approximation for nonce
	nonce := uint64(len(utxos))

	// Check if address is a validator
	_, isValidator := s.blockchain.Validators[args.Address]

	// Get staking info if the address is a validator
	var stakeInfo map[string]interface{}
	if isValidator {
		validators, err := s.node.StakePool.GetValidators(0) // Get all validators
		if err == nil {
			// Find the validator with the matching address
			for _, v := range validators {
				if v.Address == args.Address {
					stakeInfo = map[string]interface{}{
						"stake": v.Stake,
					}

					// Try to get the host ID if available
					if hostID, ok := v.HostID(); ok {
						stakeInfo["hostID"] = hostID
					}

					// Check if we can get more info from the stake pool directly
					if stakeInfoFromPool, ok := s.node.StakePool.Stakes[args.Address]; ok && stakeInfoFromPool != nil {
						stakeInfo["isActive"] = stakeInfoFromPool.IsValidator
						stakeInfo["since"] = stakeInfoFromPool.StartTime
						stakeInfo["lastActive"] = stakeInfoFromPool.LastActive
						stakeInfo["violations"] = stakeInfoFromPool.Violations
					}

					break
				}
			}
		}
	}

	// Check for pending transactions in mempool
	pendingTxCount := 0
	pendingAmount := 0.0

	for _, tx := range s.node.Mempool.GetTransactions() {
		if tx.Sender == args.Address {
			pendingTxCount++
			pendingAmount += tx.Amount + tx.GasFee
		}
		if tx.Receiver == args.Address {
			pendingTxCount++
			pendingAmount += tx.Amount
		}
	}

	// Build and return the account state
	accountState := map[string]interface{}{
		"address":     args.Address,
		"balance":     balance,
		"nonce":       nonce,
		"utxoCount":   len(utxos),
		"isValidator": isValidator,
		"pending": map[string]interface{}{
			"transactions": pendingTxCount,
			"amount":       pendingAmount,
		},
	}

	// Add staking info if available
	if stakeInfo != nil {
		accountState["staking"] = stakeInfo
	}

	return accountState, nil
}

func (s *RPCServer) handleCreateWallet(params json.RawMessage) (interface{}, *RPCError) {
	wallet, err := blockchain.NewWallet()
	if err != nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Failed to create wallet", Data: err.Error()}
	}

	return map[string]interface{}{
		"address":  wallet.Address,
		"mnemonic": wallet.Mnemonic,
	}, nil
}

func (s *RPCServer) handleImportWallet(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Mnemonic string `json:"mnemonic"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	wallet, err := blockchain.RecoverWalletFromMnemonic(args.Mnemonic)
	if err != nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Failed to recover wallet", Data: err.Error()}
	}

	return map[string]interface{}{
		"address": wallet.Address,
	}, nil
}

func (s *RPCServer) handleCreateHDWallet(params json.RawMessage) (interface{}, *RPCError) {
	// Parse parameters
	var args struct {
		InitialAddresses int `json:"initialAddresses"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// If parsing fails, set default value
		args.InitialAddresses = 1
	}

	// Ensure reasonable defaults
	if args.InitialAddresses <= 0 {
		args.InitialAddresses = 1
	}
	if args.InitialAddresses > 100 {
		args.InitialAddresses = 100 // Limit to avoid excessive generation
	}

	// Generate mnemonic
	mnemonic, err := blockchain.GenerateMnemonic(12)
	if err != nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Failed to generate mnemonic", Data: err.Error()}
	}

	// Create HD wallet
	hdWallet, err := blockchain.CreateHDWallet(mnemonic, args.InitialAddresses)
	if err != nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Failed to create HD wallet", Data: err.Error()}
	}

	// Create response
	result := map[string]interface{}{
		"mnemonic":  hdWallet.Mnemonic,
		"addresses": hdWallet.Addresses,
	}

	return result, nil
}

func (s *RPCServer) handleGetAddresses(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Mnemonic string `json:"mnemonic"`
		Start    int    `json:"start"`
		Count    int    `json:"count"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	if args.Mnemonic == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Mnemonic is required", Data: nil}
	}

	// Set reasonable defaults
	if args.Start < 0 {
		args.Start = 0
	}
	if args.Count <= 0 {
		args.Count = 10
	}
	if args.Count > 100 {
		args.Count = 100 // Limit to avoid excessive generation
	}

	// Create HD wallet from mnemonic
	hdWallet, err := blockchain.CreateHDWallet(args.Mnemonic, args.Start+args.Count)
	if err != nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Failed to recover HD wallet", Data: err.Error()}
	}

	// Get the addresses
	addresses := hdWallet.ListAddresses()

	// Only return the requested range
	if args.Start >= len(addresses) {
		return []map[string]interface{}{}, nil
	}

	end := args.Start + args.Count
	if end > len(addresses) {
		end = len(addresses)
	}

	addresses = addresses[args.Start:end]

	// Derive addresses with balances
	var addressInfos []map[string]interface{}
	for i, address := range addresses {
		// Get balance directly from UTXOPool
		balance := s.node.UTXOPool.GetBalance(address)

		addressInfos = append(addressInfos, map[string]interface{}{
			"index":   args.Start + i,
			"address": address,
			"balance": balance,
		})
	}

	return addressInfos, nil
}

func (s *RPCServer) handleCreateMultiSigWallet(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Addresses    []string `json:"addresses"`
		RequiredSigs int      `json:"requiredSigs"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	// Validate parameters
	if len(args.Addresses) < 2 {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Multisig wallet requires at least 2 addresses", Data: nil}
	}

	if args.RequiredSigs < 1 || args.RequiredSigs > len(args.Addresses) {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Required signatures must be between 1 and the number of addresses", Data: nil}
	}

	// Validate each address
	for _, addr := range args.Addresses {
		if !blockchain.ValidateAddress(addr) {
			return nil, &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("Invalid address format: %s", addr), Data: nil}
		}
	}

	// Create a multisig address
	// Create an empty map for public keys since we don't have the actual public keys
	publicKeyMap := make(map[string]*ecdsa.PublicKey)

	// Use the GenerateMultiSigAddress function from the multi_sig_wallet.go file
	multiSigAddr := blockchain.GenerateMultiSigAddress(publicKeyMap)

	return map[string]interface{}{
		"address":      multiSigAddr,
		"requiredSigs": args.RequiredSigs,
		"totalSigs":    len(args.Addresses),
		"participants": args.Addresses,
	}, nil
}

func (s *RPCServer) handleGetValidators(params json.RawMessage) (interface{}, *RPCError) {
	validators, err := s.node.StakePool.GetValidators(10)
	if err != nil {
		return nil, &RPCError{Code: ErrServerError, Message: "Failed to get validators", Data: err.Error()}
	}

	return validators, nil
}

func (s *RPCServer) handleGetStakeInfo(params json.RawMessage) (interface{}, *RPCError) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "Invalid parameters", Data: err.Error()}
	}

	if args.Address == "" {
		// If no address provided, return overall staking information
		totalStake := s.node.StakePool.GetTotalStake()
		validators, err := s.node.StakePool.GetValidators(0) // Get all validators
		if err != nil {
			return nil, &RPCError{Code: ErrServerError, Message: "Failed to get validators", Data: err.Error()}
		}

		result := map[string]interface{}{
			"totalStake":     totalStake,
			"validatorCount": len(validators),
		}

		// Only add these if we have validators
		if len(validators) > 0 {
			result["avgStakeAmount"] = totalStake / float64(len(validators))
		}

		return result, nil
	}

	// Get stake info for the specific address
	stakeInfo, exists := s.node.StakePool.Stakes[args.Address]
	if !exists {
		return nil, &RPCError{Code: ErrServerError, Message: "No stake found for address", Data: nil}
	}

	// Build response
	result := map[string]interface{}{
		"address":     args.Address,
		"isValidator": stakeInfo.IsValidator,
		"stake":       float64(stakeInfo.Amount),
		"startTime":   stakeInfo.StartTime,
		"lastActive":  stakeInfo.LastActive,
		"violations":  stakeInfo.Violations,
	}

	// Add withdrawal request info if present
	if stakeInfo.WithdrawalReq != nil {
		result["withdrawalRequest"] = map[string]interface{}{
			"requestTime": stakeInfo.WithdrawalReq.RequestTime,
			"amount":      stakeInfo.WithdrawalReq.Amount,
			"status":      stakeInfo.WithdrawalReq.Status,
		}
	}

	return result, nil
}

// Create wrapper functions for internal handlers to match RPCMethodHandler type
func (s *RPCServer) internalGetBlockCount(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetBlockCount(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetBlockByHash(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetBlockByHash(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetBlockByHeight(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetBlockByHeight(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetChainInfo(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetChainInfo(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetBalance(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetBalance(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetUTXOs(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetUTXOs(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalSendTransaction(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleSendTransaction(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetTransaction(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetTransaction(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetPendingTransactions(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetPendingTransactions(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalEstimateFee(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleEstimateFee(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetTransactionHistory(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetTransactionHistory(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetPeerInfo(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetPeerInfo(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetNetworkStats(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetNetworkStats(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetNodeStatus(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetNodeStatus(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetAccountState(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetAccountState(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalCreateWallet(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleCreateWallet(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalImportWallet(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleImportWallet(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalCreateHDWallet(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleCreateHDWallet(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetAddresses(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetAddresses(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalCreateMultiSigWallet(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleCreateMultiSigWallet(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetValidators(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetValidators(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

func (s *RPCServer) internalGetStakeInfo(params json.RawMessage) (interface{}, error) {
	result, rpcErr := s.handleGetStakeInfo(params)
	if rpcErr != nil {
		return nil, fmt.Errorf(rpcErr.Message)
	}
	return result, nil
}

// handleWebSocket handles WebSocket connections
func (s *RPCServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Printf("Error upgrading to WebSocket: %v", err)
		return
	}

	// Store client connection
	clientIP := r.RemoteAddr
	s.wsConnectionsMu.Lock()
	s.wsConnections[conn] = clientIP
	s.wsConnectionsMu.Unlock()

	s.logger.Printf("New WebSocket connection from %s", clientIP)

	// Handle WebSocket messages in a loop
	go func() {
		defer func() {
			// Clean up connection when done
			conn.Close()
			s.wsConnectionsMu.Lock()
			delete(s.wsConnections, conn)
			s.wsConnectionsMu.Unlock()
			s.logger.Printf("WebSocket connection closed for %s", clientIP)
		}()

		for {
			// Read message
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					s.logger.Printf("WebSocket error: %v", err)
				}
				break
			}

			// Parse as JSON-RPC request
			var req JSONRPCRequest
			if err := json.Unmarshal(msg, &req); err != nil {
				s.sendWebSocketError(conn, nil, ErrParseError, "Parse error", err)
				continue
			}

			// Validate JSON-RPC version
			if req.JSONRPC != "2.0" {
				s.sendWebSocketError(conn, req.ID, ErrInvalidRequest, "Invalid Request", "Expected JSON-RPC 2.0")
				continue
			}

			// Find method handler
			s.mu.RLock()
			handler, exists := s.methods[req.Method]
			s.mu.RUnlock()

			if !exists {
				s.sendWebSocketError(conn, req.ID, ErrMethodNotFound, "Method not found", fmt.Sprintf("Method '%s' not found", req.Method))
				continue
			}

			// Execute method
			result, err := handler(req.Params)
			if err != nil {
				s.sendWebSocketError(conn, req.ID, ErrInternalError, "Internal error", err)
				continue
			}

			// Send successful response
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				Result:  result,
				ID:      req.ID,
			}

			if err := conn.WriteJSON(resp); err != nil {
				s.logger.Printf("Error writing response: %v", err)
				break
			}
		}
	}()
}

// sendWebSocketError sends a JSON-RPC error over WebSocket
func (s *RPCServer) sendWebSocketError(conn *websocket.Conn, id interface{}, code int, message string, data interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}

	if err := conn.WriteJSON(resp); err != nil {
		s.logger.Printf("Error sending error response: %v", err)
	}
}

// handleSupSubscribe handles sup_subscribe RPC method
func (s *RPCServer) handleSupSubscribe(params interface{}) (interface{}, error) {
	// Get client context
	ctx, ok := params.(*jsonExtendedContext)
	if !ok {
		return nil, fmt.Errorf("missing context for subscription")
	}

	// Parse parameters from the RawMessage
	var args []interface{}
	if err := json.Unmarshal(ctx.RawMessage, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if len(args) < 1 {
		return nil, fmt.Errorf("missing event type parameter")
	}

	// Get event type parameter
	eventType, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("event_type must be a string")
	}

	// Validate event type
	validEventTypes := map[string]bool{
		"new_blocks":           true,
		"pending_transactions": true,
		"logs":                 true,
	}

	if !validEventTypes[eventType] {
		return nil, fmt.Errorf("unsupported event type: %s", eventType)
	}

	// Extract filters if provided
	var filters map[string]interface{}
	if len(args) > 1 {
		if filtersArg, ok := args[1].(map[string]interface{}); ok {
			filters = filtersArg
		}
	}

	// WebSocket connection is required for subscriptions
	if ctx.WebSocket == nil {
		return nil, fmt.Errorf("WebSocket connection required for subscriptions")
	}

	// Create subscription
	subID, err := s.subscriptionMgr.AddSubscription(ctx.ClientIP, eventType, filters, ctx.WebSocket)
	if err != nil {
		return nil, err
	}

	return subID, nil
}

// handleSupUnsubscribe handles sup_unsubscribe RPC method
func (s *RPCServer) handleSupUnsubscribe(params interface{}) (interface{}, error) {
	var rawMessage json.RawMessage

	// Check if we received a jsonExtendedContext or a raw message
	if ctx, ok := params.(*jsonExtendedContext); ok {
		rawMessage = ctx.RawMessage
	} else if rm, ok := params.(json.RawMessage); ok {
		rawMessage = rm
	} else {
		return nil, fmt.Errorf("invalid parameters type")
	}

	// Parse parameters
	var args []string
	if err := json.Unmarshal(rawMessage, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if len(args) < 1 {
		return nil, fmt.Errorf("missing subscription_id parameter")
	}

	// Get subscription ID
	subscriptionID := args[0]

	// Remove subscription
	success := s.subscriptionMgr.RemoveSubscription(subscriptionID)

	return success, nil
}

// handleSupGetSubscriptions handles sup_getSubscriptions RPC method
func (s *RPCServer) handleSupGetSubscriptions(params interface{}) (interface{}, error) {
	// Get client context
	ctx, ok := params.(*jsonExtendedContext)
	if !ok {
		return nil, fmt.Errorf("missing context for subscription")
	}

	// Get subscriptions for client
	subscriptions := s.subscriptionMgr.GetSubscriptions(ctx.ClientIP)

	return subscriptions, nil
}

// jsonExtendedContext is used to pass additional context to RPC methods
type jsonExtendedContext struct {
	json.RawMessage
	ClientIP  string
	WebSocket *websocket.Conn
}

// UnmarshalJSON implements json.Unmarshaler for jsonExtendedContext
func (c *jsonExtendedContext) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &c.RawMessage)
}

// MarshalJSON implements json.Marshaler for jsonExtendedContext
func (c jsonExtendedContext) MarshalJSON() ([]byte, error) {
	return c.RawMessage.MarshalJSON()
}

// setupBlockchainSubscriptions sets up notification triggers for blockchain events
func (s *RPCServer) setupBlockchainSubscriptions() {
	// Listen for new blocks
	s.node.OnNewBlock(func(block *blockchain.Block) {
		// Create block notification data
		blockData := map[string]interface{}{
			"block_hash":   block.Hash(),
			"block_number": block.Header.BlockNumber,
			"timestamp":    block.Header.Timestamp,
		}

		// Notify subscribers
		s.subscriptionMgr.NotifySubscribers("new_blocks", blockData)
	})

	// Listen for new transactions in mempool
	s.node.OnNewTransaction(func(tx *blockchain.Transaction) {
		// Create transaction notification data
		txData := map[string]interface{}{
			"tx_hash":   tx.TransactionID,
			"sender":    tx.Sender,
			"receiver":  tx.Receiver,
			"amount":    tx.Amount,
			"timestamp": tx.Timestamp,
		}

		// Notify subscribers
		s.subscriptionMgr.NotifySubscribers("pending_transactions", txData)
	})

	// Listen for log events (if implemented)
	s.node.OnLogEvent(func(log *blockchain.LogEvent) {
		if log == nil {
			return
		}

		// Create log notification data
		logData := map[string]interface{}{
			"tx_hash":      log.TransactionHash,
			"address":      log.Address,
			"topics":       log.Topics,
			"data":         log.Data,
			"block_number": log.BlockNumber,
			"log_index":    log.LogIndex,
		}

		// Notify subscribers
		s.subscriptionMgr.NotifySubscribers("logs", logData)
	})
}
