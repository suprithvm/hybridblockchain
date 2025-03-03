package blockchain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"os"
	"testing"
)

// TestNewWallet tests the creation of a new wallet.
func TestNewWallet(t *testing.T) {
	t.Log("Testing NewWallet...")

	wallet, err := NewWallet()
	if err != nil {
		t.Fatalf("Failed to create new wallet: %v", err)
	}

	if wallet == nil {
		t.Fatal("Expected a valid wallet, got nil")
	}

	t.Logf("Wallet created successfully. Address: %s", wallet.Address)
	t.Logf("Mnemonic: %s", wallet.Mnemonic)
	t.Logf("Private Key (bytes): %x", wallet.PrivateKeyBytes)
	t.Logf("Public Key (bytes): %x", wallet.PublicKeyBytes)
}

// TestNewWalletFromPrivateKey tests creating a wallet from an existing private key.
func TestNewWalletFromPrivateKey(t *testing.T) {
	t.Log("Testing NewWalletFromPrivateKey...")

	// Generate a new private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	wallet := NewWalletFromPrivateKey(privateKey)
	if wallet == nil {
		t.Fatal("Expected a valid wallet, got nil")
	}

	t.Logf("Wallet created from private key. Address: %s", wallet.Address)
	t.Logf("Private Key (bytes): %x", wallet.PrivateKeyBytes)
	t.Logf("Public Key (bytes): %x", wallet.PublicKeyBytes)
}

// TestRecoverWalletFromMnemonic tests wallet recovery from a mnemonic phrase.
func TestRecoverWalletFromMnemonic(t *testing.T) {
	t.Log("Testing RecoverWalletFromMnemonic...")

	// Create a new wallet to get a mnemonic
	originalWallet, err := NewWallet()
	if err != nil {
		t.Fatalf("Failed to create new wallet: %v", err)
	}

	// Recover wallet from the mnemonic
	recoveredWallet, err := RecoverWalletFromMnemonic(originalWallet.Mnemonic)
	if err != nil {
		t.Fatalf("Failed to recover wallet from mnemonic: %v", err)
	}

	// Compare addresses
	if originalWallet.Address != recoveredWallet.Address {
		t.Fatalf("Address mismatch. Original: %s, Recovered: %s", originalWallet.Address, recoveredWallet.Address)
	}

	t.Logf("Wallet recovered successfully. Address: %s", recoveredWallet.Address)
	t.Logf("Mnemonic: %s", recoveredWallet.Mnemonic)
}

// TestInvalidMnemonic tests wallet recovery with an invalid mnemonic.
func TestInvalidMnemonic(t *testing.T) {
	t.Log("Testing recovery with invalid mnemonic...")

	invalidMnemonic := "this is an invalid mnemonic phrase"
	_, err := RecoverWalletFromMnemonic(invalidMnemonic)
	if err == nil {
		t.Fatal("Expected error for invalid mnemonic, got nil")
	}

	t.Logf("Recovery failed as expected. Error: %v", err)
}

// TestAddressValidation tests the address validation logic.
func TestAddressValidation(t *testing.T) {
	t.Log("Testing address validation...")

	// Create a new wallet
	wallet, err := NewWallet()
	if err != nil {
		t.Fatalf("Failed to create new wallet: %v", err)
	}

	// Validate the address
	if !ValidateAddress(wallet.Address) {
		t.Fatalf("Address validation failed for valid address: %s", wallet.Address)
	}

	// Test with an invalid address
	invalidAddress := "invalid_address"
	if ValidateAddress(invalidAddress) {
		t.Fatalf("Address validation passed for invalid address: %s", invalidAddress)
	}

	t.Logf("Address validation passed for valid address: %s", wallet.Address)
	t.Logf("Address validation failed as expected for invalid address: %s", invalidAddress)
}

// TestSaveAndLoadWallet tests saving and loading a wallet to/from a file.
func TestSaveAndLoadWallet(t *testing.T) {
	t.Log("Testing SaveToFile and LoadWalletFromFile...")

	// Create a new wallet
	wallet, err := NewWallet()
	if err != nil {
		t.Fatalf("Failed to create new wallet: %v", err)
	}

	// Save the wallet to a file
	filename := "test_wallet.json"
	err = wallet.SaveToFile(filename)
	if err != nil {
		t.Fatalf("Failed to save wallet to file: %v", err)
	}
	t.Logf("Wallet saved to file: %s", filename)

	// Load the wallet from the file
	loadedWallet, err := LoadWalletFromFile(filename)
	if err != nil {
		t.Fatalf("Failed to load wallet from file: %v", err)
	}

	// Compare addresses
	if wallet.Address != loadedWallet.Address {
		t.Fatalf("Address mismatch. Original: %s, Loaded: %s", wallet.Address, loadedWallet.Address)
	}

	t.Logf("Wallet loaded successfully. Address: %s", loadedWallet.Address)

	// Clean up the test file
	err = os.Remove(filename)
	if err != nil {
		t.Logf("Failed to delete test file: %v", err)
	}
}

// TestInvalidFileOperations tests loading a wallet from a non-existent file.
func TestInvalidFileOperations(t *testing.T) {
	t.Log("Testing loading from a non-existent file...")

	_, err := LoadWalletFromFile("non_existent_file.json")
	if err == nil {
		t.Fatal("Expected error for non-existent file, got nil")
	}

	t.Logf("Loading failed as expected. Error: %v", err)
}

// TestSerializeDeserializeKeys tests key serialization and deserialization.
func TestSerializeDeserializeKeys(t *testing.T) {
	t.Log("Testing key serialization and deserialization...")

	// Generate a new private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	// Serialize keys
	privateKeyBytes, publicKeyBytes := serializeKeys(privateKey, &privateKey.PublicKey)

	// Deserialize keys
	deserializedPrivateKey, deserializedPublicKey, err := DeserializeKeys(privateKeyBytes, publicKeyBytes)
	if err != nil {
		t.Fatalf("Failed to deserialize keys: %v", err)
	}

	// Compare original and deserialized keys
	if privateKey.D.Cmp(deserializedPrivateKey.D) != 0 {
		t.Fatal("Private key mismatch after deserialization")
	}
	if privateKey.PublicKey.X.Cmp(deserializedPublicKey.X) != 0 || privateKey.PublicKey.Y.Cmp(deserializedPublicKey.Y) != 0 {
		t.Fatal("Public key mismatch after deserialization")
	}

	t.Logf("Key serialization and deserialization successful.")
}

// TestInvalidKeyDeserialization tests deserialization with invalid key bytes.
func TestInvalidKeyDeserialization(t *testing.T) {
	t.Log("Testing deserialization with invalid key bytes...")

	invalidPrivateKeyBytes := []byte{0x00}
	invalidPublicKeyBytes := []byte{0x00}

	_, _, err := DeserializeKeys(invalidPrivateKeyBytes, invalidPublicKeyBytes)
	if err == nil {
		t.Fatal("Expected error for invalid key bytes, got nil")
	}

	t.Logf("Deserialization failed as expected. Error: %v", err)
}

// TestGenerateAddress tests the address generation logic.
func TestGenerateAddress(t *testing.T) {
	t.Log("Testing address generation...")

	// Generate a new private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	// Generate address
	address := generateAddress(&privateKey.PublicKey)
	if address == "" {
		t.Fatal("Generated address is empty")
	}

	t.Logf("Address generated successfully: %s", address)
}
