package blockchain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/sha3"
	"math/big"
	"strings"
	"os"
	"path/filepath"
)

// GenerateMnemonic creates a new mnemonic phrase
func GenerateMnemonic(wordCount int) (string, error) {
	if wordCount != 12 && wordCount != 24 {
		return "", errors.New("word count must be 12 or 24")
	}

	wordlist, err := LoadBIP39Wordlist()
	if err != nil {
		return "", err
	}

	entropy := make([]byte, wordCount*4/3)
	_, err = rand.Read(entropy)
	if err != nil {
		return "", err
	}

	// Generate mnemonic from entropy
	mnemonic := make([]string, wordCount)
	for i := 0; i < wordCount; i++ {
		index := int(entropy[i]) % len(wordlist)
		mnemonic[i] = wordlist[index]
	}

	return strings.Join(mnemonic, " "), nil
}



// RecoverFromMnemonic recovers a wallet using a mnemonic phrase
// RecoverFromMnemonic recovers a wallet using a mnemonic phrase
func RecoverFromMnemonic(mnemonic string) (*ecdsa.PrivateKey, error) {
	// Load the BIP-39 wordlist
	wordlist, err := LoadBIP39Wordlist()
	if err != nil {
		return nil, err
	}

	// Split the mnemonic into words
	words := strings.Fields(mnemonic)
	if len(words) != 12 && len(words) != 24 {
		return nil, errors.New("mnemonic must have 12 or 24 words")
	}

	// Verify the mnemonic words against the wordlist
	for _, word := range words {
		found := false
		for _, validWord := range wordlist {
			if word == validWord {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("invalid mnemonic word: %s", word)
		}
	}

	// Generate entropy from the mnemonic
	hash := sha256.Sum256([]byte(strings.Join(words, " ")))
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
	wordlistPath := filepath.Join(baseDir, "blockchain", "bip39_wordlist.txt")

	// Read the file
	data, err := os.ReadFile(wordlistPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load BIP-39 wordlist: %v", err)
	}

	// Split the data into words
	words := strings.Split(strings.TrimSpace(string(data)), "\n")
	return words, nil
}
