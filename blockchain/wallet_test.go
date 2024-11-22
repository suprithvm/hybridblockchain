package blockchain

import (
	"testing"
)

func TestHDWalletCreation(t *testing.T) {
	mnemonic, err := GenerateMnemonic(12)
	if err != nil {
		t.Fatalf("Failed to generate mnemonic: %v", err)
	}

	hdWallet, err := CreateHDWallet(mnemonic, 5)
	if err != nil {
		t.Fatalf("Failed to create HD wallet: %v", err)
	}

	if len(hdWallet.Addresses) != 5 {
		t.Errorf("Expected 5 addresses, got %d", len(hdWallet.Addresses))
	}
}


func TestMnemonicGeneration(t *testing.T) {

	mnemonic, err := GenerateMnemonic(12)
	if err != nil {
		t.Fatalf("Failed to generate mnemonic: %v", err)
	}

	if len(mnemonic) == 0 {
		t.Fatalf("Generated mnemonic is empty")
	}
}



func TestRecoverWallet(t *testing.T) {
	mnemonic, err := GenerateMnemonic(12)
	if err != nil {
		t.Fatalf("Failed to generate mnemonic: %v", err)
	}

	wallet, err := RecoverWallet(mnemonic)
	if err != nil {
		t.Fatalf("Failed to recover wallet: %v", err)
	}

	if wallet.Address == "" {
		t.Error("Recovered wallet address is empty")
	}
}



func TestRecoverFromInvalidMnemonic(t *testing.T) {
	invalidMnemonic := "invalid mnemonic words"
	_, err := RecoverFromMnemonic(invalidMnemonic)
	if err == nil {
		t.Fatalf("Expected error for invalid mnemonic, but got none")
	}
}

