package main

import (
	"blockchain-core/blockchain"
	"bufio"
	"fmt"
	"os"
	"strings"
)

func main() {
	reader := bufio.NewReader(os.Stdin)

	// Step 1: Test Wallet Creation
	fmt.Println("=== Wallet Creation ===")

	mnemonic, err := blockchain.GenerateMnemonic(12)
	if err != nil {
		fmt.Printf("Failed to generate mnemonic: %v\n", err)
		return
	}

	wallet,err := blockchain.RecoverWalletFromMnemonic(mnemonic)
	fmt.Printf("\nNew Wallet Created Successfully!\n")
	fmt.Printf("Wallet Address: %s\n", wallet.Address)
	fmt.Printf("Mnemonic: %s\n", wallet.Mnemonic)
	fmt.Printf("Private Key (hex): %x\n", wallet.PrivateKey.D)
	fmt.Printf("Public Key (hex): %x%x\n", wallet.PublicKey.X, wallet.PublicKey.Y)

	err = blockchain.SaveWallet(wallet, "my_wallet.json")
	if err != nil {
		fmt.Println("Failed to save wallet:", err)
		return
	}
	fmt.Println("\nWallet saved successfully to file: my_wallet.json")

	// Step 2: Test Wallet Recovery
	fmt.Println("\n=== Wallet Recovery ===")
	fmt.Print("Enter your 12-word mnemonic to recover the wallet: ")
	userMnemonic, _ := reader.ReadString('\n')
	userMnemonic = strings.TrimSpace(userMnemonic)

	recoveredWallet, err := blockchain.RecoverWalletFromMnemonic(userMnemonic)
	if err != nil {
		fmt.Printf("Failed to recover wallet: %v\n", err)
	} else {
		fmt.Printf("\nWallet Recovered Successfully!\n")
		fmt.Printf("Recovered Wallet Address: %s\n", recoveredWallet.Address)
		fmt.Printf("Mnemonic: %s\n", recoveredWallet.Mnemonic)
		fmt.Printf("Private Key (hex): %x\n", recoveredWallet.PrivateKey.D)
		fmt.Printf("Public Key (hex): %x%x\n", recoveredWallet.PublicKey.X, recoveredWallet.PublicKey.Y)

		if recoveredWallet.Address == wallet.Address {
			fmt.Println("\nSuccess: The recovered wallet matches the originally created wallet!")
		} else {
			fmt.Println("\nWarning: The recovered wallet does not match the original wallet.")
		}
	}

	// Step 3: Test HD Wallet
	fmt.Println("\n=== HD Wallet Testing ===")
	fmt.Printf("Generating child wallets from the master key...\n")
	numChildWallets := 5

	hdWallet, err := blockchain.CreateHDWallet(wallet.Mnemonic, numChildWallets)
	if err != nil {
		fmt.Printf("Failed to create HD wallet: %v\n", err)
		return
	}

	fmt.Printf("\nHD Wallet Created Successfully! Derived %d child wallets:\n", numChildWallets)
	for i, childAddress := range hdWallet.ListAddresses() {
		fmt.Printf("Child Wallet %d Address: %s\n", i+1, childAddress)
	}

	// All tests completed
	fmt.Println("\nAll tests completed successfully. Thank you for using the Wallet System!")
}
