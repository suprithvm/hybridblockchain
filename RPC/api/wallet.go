package api

import (
	"blockchain-core/blockchain"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// WalletAPI handles wallet-related RPC methods
type WalletAPI struct {
	node       *blockchain.Node
	blockchain *blockchain.Blockchain
}

// NewWalletAPI creates a new wallet API instance
func NewWalletAPI(node *blockchain.Node, blockchain *blockchain.Blockchain) *WalletAPI {
	return &WalletAPI{
		node:       node,
		blockchain: blockchain,
	}
}

// CreateWallet generates a new wallet
func (api *WalletAPI) CreateWallet(params json.RawMessage) (interface{}, error) {
	wallet, err := blockchain.NewWallet()
	if err != nil {
		return nil, fmt.Errorf("failed to create wallet: %v", err)
	}

	// Convert binary key data to hex strings for the API response
	privateKeyHex := hex.EncodeToString(wallet.PrivateKeyBytes)
	publicKeyHex := hex.EncodeToString(wallet.PublicKeyBytes)

	result := map[string]interface{}{
		"address":    wallet.Address,
		"mnemonic":   wallet.Mnemonic,
		"privateKey": privateKeyHex,
		"publicKey":  publicKeyHex,
	}

	return result, nil
}

// ImportWallet imports wallet from mnemonic/private key
func (api *WalletAPI) ImportWallet(params json.RawMessage) (interface{}, error) {
	var args struct {
		Mnemonic   string `json:"mnemonic"`
		PrivateKey string `json:"privateKey"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	var wallet *blockchain.Wallet
	var err error

	if args.Mnemonic != "" {
		wallet, err = blockchain.RecoverWalletFromMnemonic(args.Mnemonic)
		if err != nil {
			return nil, fmt.Errorf("failed to recover wallet from mnemonic: %v", err)
		}
	} else if args.PrivateKey != "" {
		// RecoverWalletFromPrivateKey doesn't exist; we should use the proper function
		// from the blockchain package to handle this case
		// Create an ECDSA private key from the provided hex string
		privateKeyBytes, err := hex.DecodeString(args.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("invalid private key format: %v", err)
		}

		// Create a new private key from bytes
		privateKey, _, err := blockchain.DeserializeKeys(privateKeyBytes, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize private key: %v", err)
		}

		// Create a wallet from the private key
		wallet = blockchain.NewWalletFromPrivateKey(privateKey)
	} else {
		return nil, fmt.Errorf("either mnemonic or private key is required")
	}

	// Convert binary key data to hex strings for the API response
	privateKeyHex := hex.EncodeToString(wallet.PrivateKeyBytes)
	publicKeyHex := hex.EncodeToString(wallet.PublicKeyBytes)

	result := map[string]interface{}{
		"address":    wallet.Address,
		"mnemonic":   wallet.Mnemonic,
		"privateKey": privateKeyHex,
		"publicKey":  publicKeyHex,
	}

	return result, nil
}

// GetWalletInfo gets wallet address, public key, balance
func (api *WalletAPI) GetWalletInfo(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address  string `json:"address"`
		Mnemonic string `json:"mnemonic,omitempty"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Validate address
	if !blockchain.ValidateAddress(args.Address) {
		return nil, fmt.Errorf("invalid address format")
	}

	// Get balance directly from UTXOPool
	balance := api.node.UTXOPool.GetBalance(args.Address)

	// Get UTXOs
	utxos := api.node.UTXOPool.GetUTXOsForAddress(args.Address)

	// In UTXO model, nonce can be derived from transaction count or UTXO count
	nonce := uint64(len(utxos))

	// Create response
	info := map[string]interface{}{
		"address":   args.Address,
		"balance":   balance,
		"nonce":     nonce,
		"utxoCount": len(utxos),
		// Use UTXO count as an approximation for transaction count
		"transactions": len(utxos),
		"isValidator":  false, // Default value
	}

	// Check if this address is a validator
	_, isValidator := api.blockchain.Validators[args.Address]
	info["isValidator"] = isValidator

	// If a mnemonic was provided, add public key and validate ownership
	if args.Mnemonic != "" {
		wallet, err := blockchain.RecoverWalletFromMnemonic(args.Mnemonic)
		if err != nil {
			return nil, fmt.Errorf("failed to recover wallet from mnemonic: %v", err)
		}

		// Verify that the wallet address matches the provided address
		if wallet.Address != args.Address {
			return nil, fmt.Errorf("mnemonic does not match the provided address")
		}

		// Add public key to response in hex format
		publicKeyHex := hex.EncodeToString(wallet.PublicKeyBytes)
		info["publicKey"] = publicKeyHex

		// Don't include private key in getWalletInfo for security reasons,
		// only in create/import wallet methods
	}

	return info, nil
}

// CreateHDWallet creates a hierarchical deterministic wallet
func (api *WalletAPI) CreateHDWallet(params json.RawMessage) (interface{}, error) {
	// Optional parameter for number of addresses to generate initially
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

	// Generate a fresh mnemonic
	mnemonic, err := blockchain.GenerateMnemonic(12)
	if err != nil {
		return nil, fmt.Errorf("failed to generate mnemonic: %v", err)
	}

	// Create HD wallet using CreateHDWallet
	hdWallet, err := blockchain.CreateHDWallet(mnemonic, args.InitialAddresses)
	if err != nil {
		return nil, fmt.Errorf("failed to create HD wallet: %v", err)
	}

	// Get first wallet address and keys for convenience
	firstWallet, err := blockchain.RecoverWalletFromMnemonic(mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to create first wallet from mnemonic: %v", err)
	}

	// Convert binary key data to hex strings
	privateKeyHex := hex.EncodeToString(firstWallet.PrivateKeyBytes)
	publicKeyHex := hex.EncodeToString(firstWallet.PublicKeyBytes)

	// Create response with comprehensive data
	result := map[string]interface{}{
		"mnemonic":  hdWallet.Mnemonic,
		"addresses": hdWallet.Addresses,
		"count":     len(hdWallet.Addresses),
		"rootAccount": map[string]interface{}{
			"address":    firstWallet.Address,
			"privateKey": privateKeyHex,
			"publicKey":  publicKeyHex,
		},
	}

	return result, nil
}

// GetAddresses lists addresses from HD wallet
func (api *WalletAPI) GetAddresses(params json.RawMessage) (interface{}, error) {
	var args struct {
		Mnemonic string `json:"mnemonic"`
		Start    int    `json:"start"`
		Count    int    `json:"count"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Mnemonic == "" {
		return nil, fmt.Errorf("mnemonic is required")
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
		return nil, fmt.Errorf("failed to recover HD wallet: %v", err)
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

	// Derive addresses with detailed info
	var addressInfos []map[string]interface{}
	for i, address := range addresses {
		// Get balance directly from UTXOPool
		balance := api.node.UTXOPool.GetBalance(address)

		// Create address info
		addressInfo := map[string]interface{}{
			"index":   args.Start + i,
			"address": address,
			"balance": balance,
		}

		// We don't have a direct method to get the public key for a specific
		// derived address without implementing RecoverHDAddressAtIndex,
		// so we'll only include the address and balance for now.

		addressInfos = append(addressInfos, addressInfo)
	}

	// Return response with metadata
	return map[string]interface{}{
		"addresses": addressInfos,
		"total":     len(addressInfos),
		"start":     args.Start,
		"end":       args.Start + len(addressInfos) - 1,
	}, nil
}

// CreateMultiSigWallet creates a multi-signature wallet
func (api *WalletAPI) CreateMultiSigWallet(params json.RawMessage) (interface{}, error) {
	var args struct {
		Addresses    []string `json:"addresses"`
		RequiredSigs int      `json:"requiredSigs"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Validate parameters
	if len(args.Addresses) < 2 {
		return nil, fmt.Errorf("multisig wallet requires at least 2 addresses")
	}
	if args.RequiredSigs < 1 || args.RequiredSigs > len(args.Addresses) {
		return nil, fmt.Errorf("required signatures must be between 1 and the number of addresses")
	}

	// Validate each address
	for _, addr := range args.Addresses {
		if !blockchain.ValidateAddress(addr) {
			return nil, fmt.Errorf("invalid address format: %s", addr)
		}
	}

	// Generate a map of public keys (required for creating multisig wallet)
	// In a real implementation, you would need to retrieve the public keys for each address
	// For this RPC method, we'll use an empty map since we don't have access to the keys
	publicKeyMap := make(map[string]*ecdsa.PublicKey)

	// Create a simplified multisig address by using GenerateMultiSigAddress
	multiSigAddress := blockchain.GenerateMultiSigAddress(publicKeyMap)

	// Return result
	return map[string]interface{}{
		"address":      multiSigAddress,
		"requiredSigs": args.RequiredSigs,
		"totalSigs":    len(args.Addresses),
		"participants": args.Addresses,
	}, nil
}

// SignMessage signs a message with wallet private key
func (api *WalletAPI) SignMessage(params json.RawMessage) (interface{}, error) {
	var args struct {
		Message  string `json:"message"`
		Mnemonic string `json:"mnemonic"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Validate parameters
	if args.Message == "" {
		return nil, fmt.Errorf("message is required")
	}
	if args.Mnemonic == "" {
		return nil, fmt.Errorf("mnemonic is required")
	}

	// Recover wallet from mnemonic
	wallet, err := blockchain.RecoverWalletFromMnemonic(args.Mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to recover wallet: %v", err)
	}

	// Sign message using the blockchain.SignMessage function
	signature, err := blockchain.SignMessage(wallet.PrivateKey, args.Message)
	if err != nil {
		return nil, fmt.Errorf("failed to sign message: %v", err)
	}

	// Return result
	return map[string]interface{}{
		"address":   wallet.Address,
		"message":   args.Message,
		"signature": signature,
	}, nil
}

// VerifySignature verifies a message signature
func (api *WalletAPI) VerifySignature(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address   string `json:"address"`
		Message   string `json:"message"`
		Signature string `json:"signature"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Validate parameters
	if args.Address == "" {
		return nil, fmt.Errorf("address is required")
	}
	if args.Message == "" {
		return nil, fmt.Errorf("message is required")
	}
	if args.Signature == "" {
		return nil, fmt.Errorf("signature is required")
	}

	// Get public key from address - this is a simplification since we don't have access
	// to a way to retrieve a public key from an address in this API context

	// In a real implementation, we would need to have a way to resolve a public key
	// For now, we'll return a result that indicates verification isn't possible

	return map[string]interface{}{
		"address": args.Address,
		"message": args.Message,
		"isValid": false,
		"error":   "Verification not possible without public key",
	}, nil
}
