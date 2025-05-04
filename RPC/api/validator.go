package api

import (
	"blockchain-core/blockchain"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ValidatorAPI handles validator-related RPC methods
type ValidatorAPI struct {
	node       *blockchain.Node
	blockchain *blockchain.Blockchain
}

// NewValidatorAPI creates a new validator API instance
func NewValidatorAPI(node *blockchain.Node, blockchain *blockchain.Blockchain) *ValidatorAPI {
	return &ValidatorAPI{
		node:       node,
		blockchain: blockchain,
	}
}

// GetValidators lists active validators
func (api *ValidatorAPI) GetValidators(params json.RawMessage) (interface{}, error) {
	var args struct {
		Limit int `json:"limit"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// Default to 100 validators if not specified
		args.Limit = 100
	}

	if args.Limit <= 0 {
		args.Limit = 100
	}

	// Get validators from blockchain validators map
	validators := api.blockchain.Validators

	// Get stake pool from blockchain
	stakePool := api.blockchain.GetStakePool()
	if stakePool == nil {
		return nil, fmt.Errorf("stake pool not available")
	}

	// Convert validators map to a slice for the response
	validatorMap := make(map[string]map[string]interface{})
	validatorCount := 0

	// First add validators from the blockchain validators map
	for addr, val := range validators {
		validatorInfo := map[string]interface{}{
			"address":    addr,
			"status":     val.Status,
			"score":      val.Score,
			"lastActive": val.LastActive,
		}

		// Add performance data if available
		if val.Performance != nil {
			validatorInfo["performance"] = map[string]interface{}{
				"blocksProposed":    val.Performance.BlocksProposed,
				"blocksValidated":   val.Performance.BlocksValidated,
				"missedValidations": val.Performance.MissedValidations,
				"uptimePercentage":  val.Performance.UptimePercentage,
			}
		}

		// Add stake information if available in the stake pool
		if stake, exists := stakePool.Stakes[addr]; exists {
			// Convert stake amount to tokens with proper decimal precision
			stakeAmount := float64(stake.Amount) / 100000000.0

			validatorInfo["stake"] = map[string]interface{}{
				"amount":         stakeAmount,
				"startTime":      stake.StartTime,
				"lastRewardTime": stake.LastRewardTime,
				"isValidator":    stake.IsValidator,
				"lastActive":     stake.LastActive,
				"hostID":         stake.HostID,
			}

			// Add withdrawal request if present
			if stake.WithdrawalReq != nil {
				validatorInfo["stake"].(map[string]interface{})["withdrawalRequest"] = map[string]interface{}{
					"requestTime": stake.WithdrawalReq.RequestTime,
					"amount":      stake.WithdrawalReq.Amount,
					"status":      stake.WithdrawalReq.Status,
				}
			}
		} else {
			// No stake info found, set zero values
			validatorInfo["stake"] = map[string]interface{}{
				"amount":      0.0,
				"isValidator": false,
			}
		}

		validatorMap[addr] = validatorInfo
		validatorCount++
	}

	// Then add validators that are in the stake pool but not in blockchain validators map
	for addr, stake := range stakePool.Stakes {
		// Skip if this validator was already added
		if _, exists := validatorMap[addr]; exists {
			continue
		}

		// Create a new validator entry
		validatorInfo := map[string]interface{}{
			"address":    addr,
			"status":     0, // Default status for validators not in blockchain.Validators
			"score":      0, // Default score
			"lastActive": stake.LastActive,
		}

		// Add stake information
		stakeAmount := float64(stake.Amount) / 100000000.0

		validatorInfo["stake"] = map[string]interface{}{
			"amount":         stakeAmount,
			"startTime":      stake.StartTime,
			"lastRewardTime": stake.LastRewardTime,
			"isValidator":    stake.IsValidator,
			"lastActive":     stake.LastActive,
			"hostID":         stake.HostID,
		}

		// Add withdrawal request if present
		if stake.WithdrawalReq != nil {
			validatorInfo["stake"].(map[string]interface{})["withdrawalRequest"] = map[string]interface{}{
				"requestTime": stake.WithdrawalReq.RequestTime,
				"amount":      stake.WithdrawalReq.Amount,
				"status":      stake.WithdrawalReq.Status,
			}
		}

		// Add performance if available
		if stake.Performance != nil {
			validatorInfo["performance"] = map[string]interface{}{
				"blocksProposed":    stake.Performance.BlocksProposed,
				"blocksValidated":   stake.Performance.BlocksValidated,
				"missedValidations": stake.Performance.MissedValidations,
				"uptimePercentage":  stake.Performance.UptimePercentage,
			}
		}

		validatorMap[addr] = validatorInfo
		validatorCount++
	}

	// Convert map to list for response
	var validatorList []map[string]interface{}
	for _, info := range validatorMap {
		validatorList = append(validatorList, info)
	}

	// Apply limit
	if len(validatorList) > args.Limit {
		validatorList = validatorList[:args.Limit]
	}

	return map[string]interface{}{
		"validators": validatorList,
		"total":      validatorCount,
		"returned":   len(validatorList),
	}, nil
}

// GetStakeInfo retrieves validator stake information
func (api *ValidatorAPI) GetStakeInfo(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Address == "" {
		return nil, fmt.Errorf("validator address is required")
	}

	// Get stake from the stake pool
	stakePool := api.blockchain.GetStakePool()
	if stakePool == nil {
		return nil, fmt.Errorf("stake pool not available")
	}

	stake, exists := stakePool.Stakes[args.Address]
	if !exists {
		return nil, fmt.Errorf("no stake found for address %s", args.Address)
	}

	return map[string]interface{}{
		"address":        args.Address,
		"amount":         stake.Amount,
		"startTime":      stake.StartTime,
		"lastRewardTime": stake.LastRewardTime,
		"isValidator":    stake.IsValidator,
	}, nil
}

// StakeTokens stakes tokens to become validator
func (api *ValidatorAPI) StakeTokens(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address     string  `json:"address"`
		Amount      float64 `json:"amount"`
		Signature   string  `json:"signature"`
		PublicKey   string  `json:"publicKey"` // Sender's public key in hex format
		Transaction string  `json:"transaction"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Validate inputs
	if args.Address == "" {
		return nil, fmt.Errorf("address is required")
	}
	if args.Amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than 0")
	}

	// Get stake pool
	stakePool := api.blockchain.GetStakePool()
	if stakePool == nil {
		return nil, fmt.Errorf("stake pool not available")
	}

	// Check if user has enough balance
	balance := api.blockchain.GetBalance(args.Address)
	if balance < args.Amount {
		return nil, fmt.Errorf("insufficient balance: %f (required %f)", balance, args.Amount)
	}

	var stakeTransaction *blockchain.Transaction

	// Handle pre-signed transaction
	if args.Transaction != "" {
		// Deserialize the complete transaction
		txBytes, err := hex.DecodeString(args.Transaction)
		if err != nil {
			return nil, fmt.Errorf("invalid transaction encoding: %v", err)
		}

		stakeTransaction = &blockchain.Transaction{}
		if err := json.Unmarshal(txBytes, stakeTransaction); err != nil {
			return nil, fmt.Errorf("failed to deserialize transaction: %v", err)
		}

		// Verify the signature
		if !stakeTransaction.VerifySignature() {
			return nil, fmt.Errorf("transaction signature verification failed")
		}
	} else {
		// Create stake transaction
		var err error
		stakeTransaction, err = blockchain.NewTransaction(
			args.Address,
			"STAKEPOOL", // Special address for the stake pool
			args.Amount,
			0, // No gas price for staking
			0, // No gas limit for staking
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create stake transaction: %v", err)
		}

		// Require signature if no full transaction was provided
		if args.Signature == "" {
			return nil, fmt.Errorf("transaction signature is required")
		}

		// Require public key if no full transaction was provided
		if args.PublicKey == "" {
			return nil, fmt.Errorf("sender's public key is required")
		}

		// Decode and set the public key
		publicKeyBytes, err := hex.DecodeString(args.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("invalid public key format: %v", err)
		}
		stakeTransaction.SenderPubKey = publicKeyBytes

		// Apply provided signature
		stakeTransaction.Signature = args.Signature

		// Verify signature
		if !stakeTransaction.VerifySignature() {
			return nil, fmt.Errorf("transaction signature verification failed")
		}
	}

	// Broadcast transaction to network
	if err := api.node.BroadcastTransaction(stakeTransaction, nil); err != nil {
		return nil, fmt.Errorf("failed to broadcast transaction: %v", err)
	}

	// Process stake by calling AddStake with the correct parameters
	err := stakePool.AddStake(args.Address, "", args.Amount) // Empty string for hostID in this case
	if err != nil {
		return nil, fmt.Errorf("failed to add stake: %v", err)
	}

	// Return result
	return map[string]interface{}{
		"success":       true,
		"transactionId": stakeTransaction.TransactionID,
		"address":       args.Address,
		"amount":        args.Amount,
		"status":        "staked",
	}, nil
}

// UnstakeTokens unstakes tokens
func (api *ValidatorAPI) UnstakeTokens(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address     string  `json:"address"`
		Amount      float64 `json:"amount"`
		Signature   string  `json:"signature"`
		PublicKey   string  `json:"publicKey"` // Sender's public key in hex format
		Transaction string  `json:"transaction"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Validate inputs
	if args.Address == "" {
		return nil, fmt.Errorf("address is required")
	}
	if args.Amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than 0")
	}

	// Get stake pool
	stakePool := api.blockchain.GetStakePool()
	if stakePool == nil {
		return nil, fmt.Errorf("stake pool not available")
	}

	// Check if user has enough staked
	stake, exists := stakePool.Stakes[args.Address]
	if !exists {
		return nil, fmt.Errorf("no stake found for address %s", args.Address)
	}
	if float64(stake.Amount)/100000000 < args.Amount {
		return nil, fmt.Errorf("insufficient staked amount: %f (requested %f)", float64(stake.Amount)/100000000, args.Amount)
	}

	var unstakeTransaction *blockchain.Transaction

	// Handle pre-signed transaction
	if args.Transaction != "" {
		// Deserialize the complete transaction
		txBytes, err := hex.DecodeString(args.Transaction)
		if err != nil {
			return nil, fmt.Errorf("invalid transaction encoding: %v", err)
		}

		unstakeTransaction = &blockchain.Transaction{}
		if err := json.Unmarshal(txBytes, unstakeTransaction); err != nil {
			return nil, fmt.Errorf("failed to deserialize transaction: %v", err)
		}

		// Verify the signature
		if !unstakeTransaction.VerifySignature() {
			return nil, fmt.Errorf("transaction signature verification failed")
		}
	} else {
		// Create unstake transaction
		var err error
		unstakeTransaction, err = blockchain.NewTransaction(
			"STAKEPOOL", // Special address for the stake pool
			args.Address,
			args.Amount,
			0, // No gas price for unstaking
			0, // No gas limit for unstaking
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create unstake transaction: %v", err)
		}

		// Require signature if no full transaction was provided
		if args.Signature == "" {
			return nil, fmt.Errorf("transaction signature is required")
		}

		// Require public key if no full transaction was provided
		if args.PublicKey == "" {
			return nil, fmt.Errorf("sender's public key is required")
		}

		// Decode and set the public key
		publicKeyBytes, err := hex.DecodeString(args.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("invalid public key format: %v", err)
		}
		unstakeTransaction.SenderPubKey = publicKeyBytes

		// Apply provided signature
		unstakeTransaction.Signature = args.Signature

		// Verify signature
		if !unstakeTransaction.VerifySignature() {
			return nil, fmt.Errorf("transaction signature verification failed")
		}
	}

	// Broadcast transaction to network
	if err := api.node.BroadcastTransaction(unstakeTransaction, nil); err != nil {
		return nil, fmt.Errorf("failed to broadcast transaction: %v", err)
	}

	// Process unstake with proper error handling
	err := stakePool.RemoveStake(args.Address, args.Amount)
	if err != nil {
		return nil, fmt.Errorf("failed to remove stake: %v", err)
	}

	// Return result
	return map[string]interface{}{
		"success":       true,
		"transactionId": unstakeTransaction.TransactionID,
		"address":       args.Address,
		"amount":        args.Amount,
		"status":        "unstaked",
	}, nil
}

// GetValidatorRewards retrieves validator rewards
func (api *ValidatorAPI) GetValidatorRewards(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Address == "" {
		return nil, fmt.Errorf("validator address is required")
	}

	// Get UTXO pool
	utxoPool := api.node.UTXOPool
	if utxoPool == nil {
		return nil, fmt.Errorf("UTXO pool not available")
	}

	// Get all UTXOs for the address
	allUTXOs := utxoPool.GetUTXOsForAddress(args.Address)

	// Filter for validator reward transactions
	var rewardsList []map[string]interface{}
	var totalRewards float64

	for _, utxo := range allUTXOs {
		// Check for signatures or indicators that this was a validator reward
		// In real UTXO blockchains, you would typically identify validator rewards
		// by the transaction type or some metadata in the UTXO

		// Here we're looking at the transaction input (if it's "system", it's likely a reward)
		// In a real implementation, you'd have a more reliable way to identify rewards
		if utxo.TransactionID != "" {
			// Try to get the block that contains this transaction
			blockWithReward := api.blockchain.GetBlockByHeight(utxo.BlockHeight)
			if blockWithReward != nil {
				// Look for transactions in this block
				for _, tx := range blockWithReward.Body.Transactions.GetAllTransactions() {
					// Check if this is our UTXO's transaction and is of type validator reward
					if tx.TransactionID == utxo.TransactionID && tx.TxType == blockchain.TX_VALIDATOR_REWARD {
						rewardEvent := map[string]interface{}{
							"blockHeight": utxo.BlockHeight,
							"amount":      utxo.Amount,
							"timestamp":   utxo.Timestamp,
							"type":        "validator_reward",
							"txId":        utxo.TransactionID,
						}

						rewardsList = append(rewardsList, rewardEvent)
						totalRewards += utxo.Amount
						break
					}
				}
			}
		}
	}

	return map[string]interface{}{
		"address":      args.Address,
		"totalRewards": totalRewards,
		"rewards":      rewardsList,
	}, nil
}

// GetDailyValidatorRewards retrieves daily validator rewards
func (api *ValidatorAPI) GetDailyValidatorRewards(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
		Days    int    `json:"days"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Address == "" {
		return nil, fmt.Errorf("validator address is required")
	}

	// Get UTXO pool
	utxoPool := api.node.UTXOPool
	if utxoPool == nil {
		return nil, fmt.Errorf("UTXO pool not available")
	}

	// Get all UTXOs for the address
	allUTXOs := utxoPool.GetUTXOsForAddress(args.Address)

	// Filter for validator reward transactions
	var rewardsList []map[string]interface{}
	var totalRewards float64

	for _, utxo := range allUTXOs {
		// Check for signatures or indicators that this was a validator reward
		// In real UTXO blockchains, you would typically identify validator rewards
		// by the transaction type or some metadata in the UTXO

		// Here we're looking at the transaction input (if it's "system", it's likely a reward)
		// In a real implementation, you'd have a more reliable way to identify rewards
		if utxo.TransactionID != "" {
			// Try to get the block that contains this transaction
			blockWithReward := api.blockchain.GetBlockByHeight(utxo.BlockHeight)
			if blockWithReward != nil {
				// Look for transactions in this block
				for _, tx := range blockWithReward.Body.Transactions.GetAllTransactions() {
					// Check if this is our UTXO's transaction and is of type validator reward
					if tx.TransactionID == utxo.TransactionID && tx.TxType == blockchain.TX_VALIDATOR_REWARD {
						rewardEvent := map[string]interface{}{
							"blockHeight": utxo.BlockHeight,
							"amount":      utxo.Amount,
							"timestamp":   utxo.Timestamp,
							"type":        "validator_reward",
							"txId":        utxo.TransactionID,
						}

						rewardsList = append(rewardsList, rewardEvent)
						totalRewards += utxo.Amount
						break
					}
				}
			}
		}
	}

	return map[string]interface{}{
		"address":      args.Address,
		"totalRewards": totalRewards,
		"rewards":      rewardsList,
	}, nil
}

// GetTopValidators gets the top validators by score
func (api *ValidatorAPI) GetTopValidators(params json.RawMessage) (interface{}, error) {
	var args struct {
		Limit   int  `json:"limit"`
		ByStake bool `json:"byStake"` // Sort by stake amount instead of score
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// Default to 10 validators if not specified
		args.Limit = 10
	}

	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	// Get validators
	validators := api.blockchain.Validators

	// Get stake pool from blockchain
	stakePool := api.blockchain.GetStakePool()
	if stakePool == nil {
		return nil, fmt.Errorf("stake pool not available")
	}

	// Convert to slice for sorting, including stake information
	type ValidatorWithDetails struct {
		Address     string
		Score       uint64
		Status      int // Change from string to int to match val.Status type
		StakeAmount float64
		Performance *blockchain.ValidatorPerformance
	}

	var validatorList []ValidatorWithDetails

	for addr, val := range validators {
		// Get stake amount if available
		stakeAmount := 0.0
		if stake, exists := stakePool.Stakes[addr]; exists {
			stakeAmount = float64(stake.Amount) / 100000000.0
		}

		validatorList = append(validatorList, ValidatorWithDetails{
			Address:     addr,
			Score:       val.Score,
			Status:      val.Status,
			StakeAmount: stakeAmount,
			Performance: val.Performance,
		})
	}

	// Sort validators by score or stake amount
	if args.ByStake {
		// Sort by stake amount (descending)
		sort.Slice(validatorList, func(i, j int) bool {
			return validatorList[i].StakeAmount > validatorList[j].StakeAmount
		})
	} else {
		// Sort by score (descending)
		sort.Slice(validatorList, func(i, j int) bool {
			return validatorList[i].Score > validatorList[j].Score
		})
	}

	// Limit results
	if len(validatorList) > args.Limit {
		validatorList = validatorList[:args.Limit]
	}

	// Build response with additional details
	var topValidators []map[string]interface{}

	for _, valInfo := range validatorList {
		validatorDetails := map[string]interface{}{
			"address":     valInfo.Address,
			"score":       valInfo.Score,
			"status":      valInfo.Status,
			"stakeAmount": valInfo.StakeAmount,
		}

		if valInfo.Performance != nil {
			validatorDetails["performance"] = map[string]interface{}{
				"blocksProposed":    valInfo.Performance.BlocksProposed,
				"blocksValidated":   valInfo.Performance.BlocksValidated,
				"missedValidations": valInfo.Performance.MissedValidations,
				"uptimePercentage":  valInfo.Performance.UptimePercentage,
			}
		}

		// Add additional stake details if available
		if stake, exists := stakePool.Stakes[valInfo.Address]; exists {
			validatorDetails["stake"] = map[string]interface{}{
				"amount":         valInfo.StakeAmount,
				"startTime":      stake.StartTime,
				"lastRewardTime": stake.LastRewardTime,
				"isValidator":    stake.IsValidator,
				"lastActive":     stake.LastActive,
				"hostID":         stake.HostID,
			}

			// Add withdrawal request if present
			if stake.WithdrawalReq != nil {
				validatorDetails["stake"].(map[string]interface{})["withdrawalRequest"] = map[string]interface{}{
					"requestTime": stake.WithdrawalReq.RequestTime,
					"amount":      stake.WithdrawalReq.Amount,
					"status":      stake.WithdrawalReq.Status,
				}
			}
		}

		topValidators = append(topValidators, validatorDetails)
	}

	// Determine sort method string for response
	sortBy := "score"
	if args.ByStake {
		sortBy = "stake"
	}

	return map[string]interface{}{
		"validators": topValidators,
		"total":      len(validators),
		"returned":   len(topValidators),
		"sortBy":     sortBy,
	}, nil
}

// GetValidatorPerformance retrieves validator performance metrics
func (api *ValidatorAPI) GetValidatorPerformance(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Address == "" {
		return nil, fmt.Errorf("validator address is required")
	}

	// Get validator from blockchain
	validator, exists := api.blockchain.Validators[args.Address]
	if !exists {
		return nil, fmt.Errorf("validator not found: %s", args.Address)
	}

	// Get performance metrics directly from the validator object
	var performance map[string]interface{}

	if validator.Performance != nil {
		performance = map[string]interface{}{
			"blocksProposed":    validator.Performance.BlocksProposed,
			"blocksValidated":   validator.Performance.BlocksValidated,
			"missedValidations": validator.Performance.MissedValidations,
			"uptimePercentage":  validator.Performance.UptimePercentage,
			"lastUpdate":        validator.Performance.LastUpdate.Unix(),
		}
	} else {
		performance = map[string]interface{}{
			"blocksProposed":    0,
			"blocksValidated":   0,
			"missedValidations": 0,
			"uptimePercentage":  0,
			"lastUpdate":        0,
		}
	}

	// Get reward information for this validator
	rewardsData, err := api.GetValidatorRewards(params)
	if err == nil {
		if rewardsMap, ok := rewardsData.(map[string]interface{}); ok {
			if totalRewards, ok := rewardsMap["totalRewards"]; ok {
				performance["totalRewards"] = totalRewards
			}
		}
	}

	// Calculate additional metrics if possible
	// Get total blocks in the blockchain to calculate participation percentage
	latestBlock := api.blockchain.GetLatestBlock()
	if latestBlock.Header.BlockNumber > 0 {
		totalBlocks := latestBlock.Header.BlockNumber

		// Calculate participation percentage
		if validator.Performance != nil && validator.Performance.BlocksValidated > 0 {
			participationRate := float64(validator.Performance.BlocksValidated) / float64(totalBlocks) * 100
			performance["participationRate"] = participationRate
		} else {
			performance["participationRate"] = 0.0
		}
	}

	// Add general validator metrics
	result := map[string]interface{}{
		"address":     args.Address,
		"status":      validator.Status,
		"score":       validator.Score,
		"lastActive":  validator.LastActive.Unix(),
		"performance": performance,
		"violations":  validator.Violations,
		"timeouts":    validator.Timeouts,
	}

	// Add stake information if available
	stakePool := api.blockchain.GetStakePool()
	if stakePool != nil {
		if stake, exists := stakePool.Stakes[args.Address]; exists {
			stakeAmount := float64(stake.Amount) / 100000000.0

			result["stake"] = map[string]interface{}{
				"amount":         stakeAmount,
				"startTime":      stake.StartTime.Unix(),
				"lastRewardTime": stake.LastRewardTime.Unix(),
				"isValidator":    stake.IsValidator,
				"hostID":         stake.HostID,
			}
		}
	}

	return result, nil
}

// VerifyValidator checks if an address is an active validator
func (api *ValidatorAPI) VerifyValidator(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Address == "" {
		return nil, fmt.Errorf("validator address is required")
	}

	// Check if the address is in the validators map
	validator, exists := api.blockchain.Validators[args.Address]

	if !exists {
		// Check if address exists in stake pool
		stakePool := api.blockchain.GetStakePool()
		if stakePool != nil {
			if stake, stakeExists := stakePool.Stakes[args.Address]; stakeExists && stake.IsValidator {
				return map[string]interface{}{
					"isValidator": true,
					"isActive":    true,
					"status":      "staked", // Not in validators map but staked
					"stake": map[string]interface{}{
						"amount":      float64(stake.Amount) / 100000000.0,
						"startTime":   stake.StartTime.Unix(),
						"isValidator": stake.IsValidator,
						"hostID":      stake.HostID,
					},
				}, nil
			}
		}

		return map[string]interface{}{
			"isValidator": false,
		}, nil
	}

	// Check if the validator is active
	isActive := validator.Status == blockchain.ValidatorStatusActive

	result := map[string]interface{}{
		"isValidator": true,
		"isActive":    isActive,
		"status":      validator.Status,
		"score":       validator.Score,
	}

	// Add stake information if available
	stakePool := api.blockchain.GetStakePool()
	if stakePool != nil {
		if stake, stakeExists := stakePool.Stakes[args.Address]; stakeExists {
			result["stake"] = map[string]interface{}{
				"amount":      float64(stake.Amount) / 100000000.0,
				"startTime":   stake.StartTime.Unix(),
				"isValidator": stake.IsValidator,
				"hostID":      stake.HostID,
			}
		}
	}

	return result, nil
}

// GetTotalStaked gets the total amount staked in the network
func (api *ValidatorAPI) GetTotalStaked(params json.RawMessage) (interface{}, error) {
	// Get stake pool
	stakePool := api.blockchain.GetStakePool()
	if stakePool == nil {
		return nil, fmt.Errorf("stake pool not available")
	}

	// Manual calculation to avoid using GetTotalStake
	var totalTokens float64

	// Just count the total stakes directly
	for _, stake := range stakePool.Stakes {
		// Convert each stake's Amount from uint64 to float64
		totalTokens += float64(stake.Amount) / 100000000.0
	}

	return map[string]interface{}{
		"totalStaked": totalTokens,
		"stakers":     len(stakePool.Stakes),
		"timestamp":   time.Now().Unix(),
	}, nil
}

// GetValidatorStats gets comprehensive validator statistics
func (api *ValidatorAPI) GetValidatorStats(params json.RawMessage) (interface{}, error) {
	// Get validators from blockchain directly
	validators := api.blockchain.Validators

	// Count validators by status
	activeValidators := 0
	slashedValidators := 0
	probationValidators := 0
	totalValidators := len(validators)

	// Track total and average statistics
	totalScore := uint64(0)
	totalBlocksValidated := uint64(0)
	totalBlocksProposed := uint64(0)
	totalUptime := float64(0)
	validatorsWithPerformance := 0

	for _, val := range validators {
		switch val.Status {
		case blockchain.ValidatorStatusActive:
			activeValidators++
		case blockchain.ValidatorStatusSlashed:
			slashedValidators++
		case blockchain.ValidatorStatusProbation:
			probationValidators++
		}

		// Accumulate validator metrics for averages
		totalScore += val.Score

		if val.Performance != nil {
			totalBlocksValidated += val.Performance.BlocksValidated
			totalBlocksProposed += val.Performance.BlocksProposed
			totalUptime += val.Performance.UptimePercentage
			validatorsWithPerformance++
		}
	}

	// Calculate averages
	averageScore := uint64(0)
	averageUptime := float64(0)

	if totalValidators > 0 {
		averageScore = totalScore / uint64(totalValidators)
	}

	if validatorsWithPerformance > 0 {
		averageUptime = totalUptime / float64(validatorsWithPerformance)
	}

	// Get stake pool data
	stakePool := api.blockchain.GetStakePool()
	var totalStakeTokens float64 = 0

	if stakePool != nil {
		// Calculate total stake
		for _, stake := range stakePool.Stakes {
			totalStakeTokens += float64(stake.Amount) / 100000000.0
		}
	}

	// Get latest block for additional metrics
	latestBlock := api.blockchain.GetLatestBlock()
	blockchainHeight := latestBlock.Header.BlockNumber

	// Create response with comprehensive stats
	stats := map[string]interface{}{
		"totalValidators":      totalValidators,
		"activeValidators":     activeValidators,
		"slashedValidators":    slashedValidators,
		"probationValidators":  probationValidators,
		"totalStaked":          totalStakeTokens,
		"averageScore":         averageScore,
		"averageUptime":        averageUptime,
		"totalBlocksValidated": totalBlocksValidated,
		"totalBlocksProposed":  totalBlocksProposed,
		"blockchainHeight":     blockchainHeight,
		"timestamp":            time.Now().Unix(),
	}

	return stats, nil
}
