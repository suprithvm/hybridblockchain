package main

import (
	"blockchain-core/blockchain/wallet"
	"fmt"
)

func main() {
	// Create a new wallet
	w := blockchain.NewWallet()
	fmt.Printf("Wallet Address: %s\n", w.Address)
	fmt.Printf("Mnemonic: %s\n", w.Mnemonic)

	// Save the wallet
	err := blockchain.SaveWallet(w, "my_wallet.json")
	if err != nil {
		fmt.Printf("Failed to save wallet: %v\n", err)
		return
	}
	fmt.Println("Wallet saved successfully")

	// Load the wallet
	loadedWallet, err := blockchain.LoadWallet("my_wallet.json")
	if err != nil {
		fmt.Printf("Failed to load wallet: %v\n", err)
		return
	}
	fmt.Printf("Loaded Wallet Address: %s\n", loadedWallet.Address)

	// Recover wallet using mnemonic
	recoveredWallet, err := blockchain.RecoverWalletFromMnemonic(loadedWallet.Mnemonic)
	if err != nil {
		fmt.Printf("Failed to recover wallet: %v\n", err)
		return
	}
	fmt.Printf("Recovered Wallet Address: %s\n", recoveredWallet.Address)
}
