package blockchain

import (
	"testing"
)

func TestAddStake(t *testing.T) {
	stakePool := NewStakePool()

	// Add stake for a node
	err := stakePool.AddStake("Node1", 100)
	if err != nil {
		t.Errorf("Failed to add stake: %v", err)
	}

	// Check if the stake was added correctly
	if stakePool.Stakes["Node1"] != 100 {
		t.Errorf("Expected 100 stake for Node1, got %v", stakePool.Stakes["Node1"])
	}
}

func TestRemoveStake(t *testing.T) {
	stakePool := NewStakePool()
	stakePool.AddStake("Node1", 100)

	// Remove some stake
	err := stakePool.RemoveStake("Node1", 50)
	if err != nil {
		t.Errorf("Failed to remove stake: %v", err)
	}

	// Check if the stake was removed correctly
	if stakePool.Stakes["Node1"] != 50 {
		t.Errorf("Expected 50 stake for Node1, got %v", stakePool.Stakes["Node1"])
	}

	// Try removing more stake than available
	err = stakePool.RemoveStake("Node1", 100)
	if err == nil {
		t.Errorf("Expected error when removing more stake than available")
	}
}

func TestSelectValidator(t *testing.T) {
	stakePool := NewStakePool()

	// Add stakes to multiple nodes
	stakePool.AddStake("Node1", 100)
	stakePool.AddStake("Node2", 200)

	// Select a validator and ensure it is one of the nodes with stake
	validator, err := stakePool.SelectValidator()
	if err != nil {
		t.Errorf("Validator selection failed: %v", err)
	}

	if validator != "Node1" && validator != "Node2" {
		t.Errorf("Invalid validator selected: %s", validator)
	}
}

func TestSelectValidatorNoStakes(t *testing.T) {
	stakePool := NewStakePool()

	// Try to select a validator when there are no stakes
	_, err := stakePool.SelectValidator()
	if err == nil {
		t.Errorf("Expected error when selecting validator with no stakes")
	}
}


func TestSelectValidatorWeighted(t *testing.T) {
	stakePool := NewStakePool()

	// Add stakes to multiple nodes
	stakePool.AddStake("Node1", 100)
	stakePool.AddStake("Node2", 300)

	// Run the validator selection multiple times to ensure weighted selection
	node1Count := 0
	node2Count := 0
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		validator, err := stakePool.SelectValidator()
		if err != nil {
			t.Errorf("Validator selection failed: %v", err)
			continue
		}
		if validator == "Node1" {
			node1Count++
		} else if validator == "Node2" {
			node2Count++
		}
	}

	// Ensure Node2 is selected more often than Node1 due to higher stake
	if node2Count <= node1Count {
		t.Errorf("Expected Node2 to be selected more than Node1. Node1 count: %d, Node2 count: %d", node1Count, node2Count)
	}
}



func TestNewStakePool(t *testing.T) {
	pool := NewStakePool()
	if pool == nil {
		t.Errorf("NewStakePool returned nil")
	}
}
