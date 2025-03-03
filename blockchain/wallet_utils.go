package blockchain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/sha3"
)

// GenerateMnemonic creates a new mnemonic phrase
// GenerateMnemonic creates a BIP39 mnemonic phrase with the specified word count
func GenerateMnemonic(wordCount int) (string, error) {
    log.Println("Starting GenerateMnemonic function")

    if wordCount != 12 && wordCount != 24 {
        return "", errors.New("word count must be 12 or 24")
    }

    wordlist, err := LoadBIP39Wordlist()
    if err != nil {
        return "", fmt.Errorf("failed to load wordlist: %v", err)
    }

    // Create a slice to hold the selected words
    words := make([]string, wordCount)
    
    // Generate random indices and select words
    for i := 0; i < wordCount; i++ {
        index, err := rand.Int(rand.Reader, big.NewInt(int64(len(wordlist))))
        if err != nil {
            return "", fmt.Errorf("failed to generate random index: %v", err)
        }
        words[i] = strings.TrimSpace(wordlist[int(index.Int64())])
    }

    // Join words with spaces between them and ensure no control characters
    mnemonic := strings.TrimSpace(strings.Join(words, " "))
    
    log.Printf("Mnemonic generated with length: %d", len(mnemonic))

    return mnemonic, nil
}


// RecoverFromMnemonic recovers a wallet using a mnemonic phrase
func RecoverFromMnemonic(mnemonic string) (*ecdsa.PrivateKey, error) {
    log.Println("Starting RecoverFromMnemonic function")
    
    // Clean the mnemonic of control characters
    mnemonic = strings.ReplaceAll(mnemonic, "\r", "")
    mnemonic = strings.TrimSpace(mnemonic)
    
    log.Printf("Cleaned mnemonic length: %d", len(mnemonic))
    
    // Load the BIP-39 wordlist
    wordlist, err := LoadBIP39Wordlist()
    if err != nil {
        return nil, err
    }

    // Convert the wordlist to a map for faster lookups
    wordlistMap := make(map[string]bool)
    for _, word := range wordlist {
        clean := strings.TrimSpace(word)
        if clean != "" {
            wordlistMap[clean] = true
        }
    }

    // Split the mnemonic into words
    words := strings.Fields(mnemonic) // Better than Split as it handles all whitespace
    log.Printf("Split mnemonic into %d words", len(words))
    
    if len(words) != 12 && len(words) != 24 {
        return nil, errors.New("mnemonic must have 12 or 24 words")
    }

    // Verify the mnemonic words against the wordlist
    for i, word := range words {
        if !wordlistMap[word] {
            log.Printf("Word %d '%s' not found in wordlist", i, word)
            return nil, fmt.Errorf("invalid mnemonic word: %s", word)
        }
    }

    // Generate entropy from the mnemonic
    hash := sha256.Sum256([]byte(mnemonic))
    curve := elliptic.P256()
    privateKey := new(ecdsa.PrivateKey)
    privateKey.D = new(big.Int).SetBytes(hash[:])
    privateKey.PublicKey.Curve = curve
    privateKey.PublicKey.X, privateKey.PublicKey.Y = curve.ScalarBaseMult(privateKey.D.Bytes())

    return privateKey, nil
}

// DeriveChildKey derives a child key from a master key
func DeriveChildKey(masterKey *ecdsa.PrivateKey, index int) (*ecdsa.PrivateKey, error) {
	seed := fmt.Sprintf("%s-%d", masterKey.D.String(), index)
	hash := sha3.Sum256([]byte(seed))

	childKey, err := ecdsa.GenerateKey(elliptic.P256(), strings.NewReader(hex.EncodeToString(hash[:])))
	if err != nil {
		return nil, err
	}
	return childKey, nil
}

// LoadBIP39Wordlist loads the wordlist file for BIP-39
func LoadBIP39Wordlist() ([]string, error) {
	// Determine the absolute path to the wordlist
	baseDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %v", err)
	}

	// Adjust the path based on the known location of the file
	wordlistPath := filepath.Join(baseDir, "blockchain/bip39_wordlist.txt")

	// Read the file
	data, err := os.ReadFile(wordlistPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load BIP-39 wordlist: %v", err)
	}

	// Split the data into words
	words := strings.Split(strings.TrimSpace(string(data)), "\n")
	return words, nil
}

func RecoverMultiSigWallet(mnemonic string, owners []string, requiredSigs int, publicKeyMap map[string]*ecdsa.PublicKey) (*MultiSigwWallet, error) {
	// Recover master key from mnemonic
	masterKey, err := RecoverFromMnemonic(mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to recover master key: %v", err)
	}
	log.Printf("[DEBUG] Recovered Master Key: %x", masterKey.D)

	// Sort the owners to ensure deterministic order
	sort.Strings(owners)

	// Generate the address deterministically
	address := GenerateMultiSigAddress(publicKeyMap)

	return &MultiSigwWallet{
		Owners:       owners,
		RequiredSigs: requiredSigs,
		Balance:      0,
		PublicKeyMap: publicKeyMap,
		Address:      address,
	}, nil
}

// GenerateNewPublicKey generates a new public key from a mnemonic phrase
func GenerateNewPublicKey(mnemonic string) (*ecdsa.PublicKey, error) {
	privateKey, err := RecoverFromMnemonic(mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to generate new key from mnemonic: %v", err)
	}
	return &privateKey.PublicKey, nil
}
