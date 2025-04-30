package api

import (
	"blockchain-core/blockchain"
	"encoding/json"
	"fmt"
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
	// In a real implementation, active validators would be retrieved from the blockchain
	// For this example, we'll return a list of validators from the blockchain

	validators := api.blockchain.Validators

	// Convert validators map to a slice for the response
	var validatorList []map[string]interface{}

	for addr, val := range validators {
		validatorInfo := map[string]interface{}{
			"address":    addr,
			"status":     val.Status,
			"score":      val.Score,
			"lastActive": val.LastActive,
		}

		if val.Performance != nil {
			validatorInfo["performance"] = map[string]interface{}{
				"blocksProposed":    val.Performance.BlocksProposed,
				"blocksValidated":   val.Performance.BlocksValidated,
				"missedValidations": val.Performance.MissedValidations,
				"uptimePercentage":  val.Performance.UptimePercentage,
			}
		}

		validatorList = append(validatorList, validatorInfo)
	}

	return validatorList, nil
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
		Address  string  `json:"address"`
		Amount   float64 `json:"amount"`
		Mnemonic string  `json:"mnemonic"`
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
	if args.Mnemonic == "" {
		return nil, fmt.Errorf("mnemonic is required")
	}

	// Recover wallet from mnemonic
	wallet, err := blockchain.RecoverWalletFromMnemonic(args.Mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to recover wallet: %v", err)
	}

	// Verify that the wallet address matches the staking address
	if wallet.Address != args.Address {
		return nil, fmt.Errorf("wallet address does not match staking address")
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

	// Create stake transaction (in a real implementation, this would be a specific transaction type)
	stakeTransaction, err := blockchain.NewTransaction(
		args.Address,
		"STAKEPOOL", // Special address for the stake pool
		args.Amount,
		0, // No gas price for staking
		0, // No gas limit for staking
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create stake transaction: %v", err)
	}

	// Sign transaction
	if err := wallet.SignTransaction(stakeTransaction); err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Process stake by calling AddStake with the correct parameters
	// Notice AddStake takes 3 parameters: address, hostID, amount
	err = stakePool.AddStake(args.Address, "", args.Amount) // Empty string for hostID in this case
	if err != nil {
		return nil, fmt.Errorf("failed to add stake: %v", err)
	}

	// Return result
	return map[string]interface{}{
		"success":       true,
		"transactionId": stakeTransaction.TransactionID,
		"address":       args.Address,
		"amount":        args.Amount,
	}, nil
}

// UnstakeTokens unstakes tokens
func (api *ValidatorAPI) UnstakeTokens(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address  string  `json:"address"`
		Amount   float64 `json:"amount"`
		Mnemonic string  `json:"mnemonic"`
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
	if args.Mnemonic == "" {
		return nil, fmt.Errorf("mnemonic is required")
	}

	// Recover wallet from mnemonic
	wallet, err := blockchain.RecoverWalletFromMnemonic(args.Mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to recover wallet: %v", err)
	}

	// Verify that the wallet address matches the staking address
	if wallet.Address != args.Address {
		return nil, fmt.Errorf("wallet address does not match staking address")
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

	// Create unstake transaction (in a real implementation, this would be a specific transaction type)
	unstakeTransaction, err := blockchain.NewTransaction(
		"STAKEPOOL", // Special address for the stake pool
		args.Address,
		args.Amount,
		0, // No gas price for unstaking
		0, // No gas limit for unstaking
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create unstake transaction: %v", err)
	}

	// Process unstake with proper error handling
	// The RemoveStake function takes float64 as the second parameter, not uint64
	err = stakePool.RemoveStake(args.Address, args.Amount)
	if err != nil {
		return nil, fmt.Errorf("failed to remove stake: %v", err)
	}

	// Return result
	return map[string]interface{}{
		"success":       true,
		"transactionId": unstakeTransaction.TransactionID,
		"address":       args.Address,
		"amount":        args.Amount,
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

	// In a real implementation, validator rewards would be tracked and retrieved from the database
	// For this example, we'll return a simulated response
	rewardsList := []map[string]interface{}{
		{
			"blockHeight": 100,
			"amount":      10.0,
			"timestamp":   1625097600, // Example timestamp
			"type":        "validator_reward",
		},
		{
			"blockHeight": 200,
			"amount":      15.0,
			"timestamp":   1625184000, // Example timestamp
			"type":        "validator_reward",
		},
	}

	totalRewards := 25.0 // Sum of all rewards

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

	if args.Days <= 0 {
		args.Days = 30 // Default to 30 days
	}
	if args.Days > 365 {
		args.Days = 365 // Cap at 365 days
	}

	// In a real implementation, validator rewards would be tracked daily
	// For this example, we'll return a simulated response

	// Generate sample data
	dailyRewards := make([]map[string]interface{}, args.Days)

	for i := 0; i < args.Days; i++ {
		// Simulated daily reward, increasing slightly each day
		rewardAmount := 10.0 + float64(i)*0.1

		dailyRewards[i] = map[string]interface{}{
			"day":       i + 1,
			"timestamp": 1625097600 + int64(i*86400), // Example timestamp, increasing by 1 day each time
			"reward":    rewardAmount,
			"blocks":    24, // Simulated number of blocks validated that day
		}
	}

	// Calculate total rewards
	totalRewards := 0.0
	for _, day := range dailyRewards {
		totalRewards += day["reward"].(float64)
	}

	return map[string]interface{}{
		"address":      args.Address,
		"days":         args.Days,
		"totalRewards": totalRewards,
		"dailyRewards": dailyRewards,
	}, nil
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
		return map[string]interface{}{
			"isValidator": false,
		}, nil
	}

	// Check if the validator is active
	isActive := validator.Status == blockchain.ValidatorStatusActive

	return map[string]interface{}{
		"isValidator": true,
		"isActive":    isActive,
		"status":      validator.Status,
		"score":       validator.Score,
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

	// Get performance metrics
	var performance map[string]interface{}

	if validator.Performance != nil {
		performance = map[string]interface{}{
			"blocksProposed":    validator.Performance.BlocksProposed,
			"blocksValidated":   validator.Performance.BlocksValidated,
			"missedValidations": validator.Performance.MissedValidations,
			"uptimePercentage":  validator.Performance.UptimePercentage,
			"lastUpdate":        validator.Performance.LastUpdate,
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

	// Add general validator metrics
	result := map[string]interface{}{
		"address":     args.Address,
		"status":      validator.Status,
		"score":       validator.Score,
		"lastActive":  validator.LastActive,
		"performance": performance,
		"violations":  validator.Violations,
		"timeouts":    validator.Timeouts,
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
	}, nil
}

// GetTopValidators gets the top validators by score
func (api *ValidatorAPI) GetTopValidators(params json.RawMessage) (interface{}, error) {
	var args struct {
		Limit int `json:"limit"`
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

	// Convert to slice for sorting
	type ValidatorWithScore struct {
		Address string
		Score   uint64
	}

	var validatorList []ValidatorWithScore

	for addr, val := range validators {
		validatorList = append(validatorList, ValidatorWithScore{
			Address: addr,
			Score:   val.Score,
		})
	}

	// Sort by score (descending)
	// Note: In production, you'd implement a proper sorting algorithm here
	// This is just a simplified version for the example
	for i := 0; i < len(validatorList); i++ {
		for j := i + 1; j < len(validatorList); j++ {
			if validatorList[j].Score > validatorList[i].Score {
				validatorList[i], validatorList[j] = validatorList[j], validatorList[i]
			}
		}
	}

	// Limit results
	if len(validatorList) > args.Limit {
		validatorList = validatorList[:args.Limit]
	}

	// Build response with additional details
	var topValidators []map[string]interface{}

	for _, valInfo := range validatorList {
		validator := validators[valInfo.Address]

		validatorDetails := map[string]interface{}{
			"address": valInfo.Address,
			"score":   valInfo.Score,
			"status":  validator.Status,
		}

		if validator.Performance != nil {
			validatorDetails["performance"] = map[string]interface{}{
				"blocksValidated":  validator.Performance.BlocksValidated,
				"uptimePercentage": validator.Performance.UptimePercentage,
			}
		}

		topValidators = append(topValidators, validatorDetails)
	}

	return topValidators, nil
}

// GetValidatorStats gets comprehensive validator statistics
func (api *ValidatorAPI) GetValidatorStats(params json.RawMessage) (interface{}, error) {
	// Get validators
	validators := api.blockchain.Validators

	// Count validators by status
	activeValidators := 0
	slashedValidators := 0
	probationValidators := 0
	totalValidators := len(validators)

	for _, val := range validators {
		switch val.Status {
		case blockchain.ValidatorStatusActive:
			activeValidators++
		case blockchain.ValidatorStatusSlashed:
			slashedValidators++
		case blockchain.ValidatorStatusProbation:
			probationValidators++
		}
	}

	// Get stake pool
	stakePool := api.blockchain.GetStakePool()
	var totalStakeTokens float64 = 0

	if stakePool != nil {
		// Manual calculation to avoid using GetTotalStake
		for _, stake := range stakePool.Stakes {
			totalStakeTokens += float64(stake.Amount) / 100000000.0
		}
	}

	// Calculate average score
	totalScore := uint64(0)
	for _, val := range validators {
		totalScore += val.Score
	}

	averageScore := uint64(0)
	if totalValidators > 0 {
		averageScore = totalScore / uint64(totalValidators)
	}

	// Create response
	stats := map[string]interface{}{
		"totalValidators":     totalValidators,
		"activeValidators":    activeValidators,
		"slashedValidators":   slashedValidators,
		"probationValidators": probationValidators,
		"totalStaked":         totalStakeTokens,
		"averageScore":        averageScore,
	}

	return stats, nil
}
