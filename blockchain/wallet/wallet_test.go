package blockchain

import (
	"testing"
)

func TestWalletCreation(t *testing.T) {
	wallet := NewWallet()

	if wallet.PrivateKey == nil || wallet.PublicKey == nil {
		t.Errorf("Wallet should have a private and public key")
	}

	if len(wallet.Address) < 30 || len(wallet.Address) > 36 {
		t.Errorf("Wallet address length should be between 30-36, got: %d", len(wallet.Address))
	}
}

func TestSaveAndLoadWallet(t *testing.T) {
	wallet := NewWallet()
	filename := "test_wallet.json"

	// Save the wallet
	err := SaveWallet(wallet, filename)
	if err != nil {
		t.Fatalf("Failed to save wallet: %v", err)
	}

	// Load the wallet
	loadedWallet, err := LoadWallet(filename)
	if err != nil {
		t.Fatalf("Failed to load wallet: %v", err)
	}

	// Compare addresses
	if wallet.Address != loadedWallet.Address {
		t.Errorf("Loaded wallet address mismatch. Expected: %s, Got: %s", wallet.Address, loadedWallet.Address)
	}
}

func TestMnemonicRecovery(t *testing.T) {
	wallet := NewWallet()

	recoveredWallet, err := RecoverWalletFromMnemonic(wallet.Mnemonic)
	if err != nil {
		t.Fatalf("Failed to recover wallet: %v", err)
	}

	if wallet.Address != recoveredWallet.Address {
		t.Errorf("Recovered wallet address mismatch.\nExpected: %s\nGot: %s", wallet.Address, recoveredWallet.Address)
	}

	if wallet.PublicKey.X.Cmp(recoveredWallet.PublicKey.X) != 0 || wallet.PublicKey.Y.Cmp(recoveredWallet.PublicKey.Y) != 0 {
		t.Errorf("Recovered public key mismatch.\nExpected: (%x, %x)\nGot: (%x, %x)",
			wallet.PublicKey.X.Bytes(), wallet.PublicKey.Y.Bytes(),
			recoveredWallet.PublicKey.X.Bytes(), recoveredWallet.PublicKey.Y.Bytes())
	}
}
