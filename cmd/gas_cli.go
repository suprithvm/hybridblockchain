package cmd

import (
	"fmt"
	"strconv"
	"blockchain-core/blockchain/gas"
	"github.com/spf13/cobra"
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "blockchain",
	Short: "Blockchain core application",
}

func init() {
	rootCmd.AddCommand(gasCmd)
	gasCmd.AddCommand(getCurrentPriceCmd)
	gasCmd.AddCommand(estimateFeesCmd)
	gasCmd.AddCommand(networkStatusCmd)
}

var gasCmd = &cobra.Command{
	Use:   "gas",
	Short: "Gas price and fee management commands",
	Long:  `Commands for viewing gas prices, estimating fees, and checking network congestion.`,
}

var getCurrentPriceCmd = &cobra.Command{
	Use:   "price",
	Short: "Get current gas prices",
	Run: func(cmd *cobra.Command, args []string) {
		model := gas.NewGasModel(gas.MinGasPrice, 15_000_000) // Fixed block gas limit
		estimator := gas.NewGasEstimator(model)
		fmt.Println(estimator.GetCurrentGasInfo())
	},
}

var estimateFeesCmd = &cobra.Command{
	Use:   "estimate [size]",
	Short: "Estimate transaction fees",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		txSize, _ := strconv.Atoi(args[0])
		
		model := gas.NewGasModel(gas.MinGasPrice, 15_000_000) // Fixed block gas limit
		estimator := gas.NewGasEstimator(model)
		preview := gas.NewFeePreview(estimator)
		
		fees := preview.GetTransactionFeePreview(txSize)
		recommended := preview.GetRecommendedFee(txSize)
		
		fmt.Println("\n📊 Available Fee Options:")
		for priority, details := range fees {
			fmt.Printf("\n%s Option:\n", priority)
			fmt.Printf("   • Total Fee: %d\n", details.TotalFee)
			fmt.Printf("   • Max Fee: %d\n", details.MaxFee)
			fmt.Printf("   • Processing Time: %s\n", details.EstimatedTime)
		}
		
		fmt.Printf("\n💡 Recommended Configuration:\n")
		fmt.Printf("   • Priority: %s\n", recommended.PriorityLevel)
		fmt.Printf("   • Fee: %d\n", recommended.TotalFee)
		fmt.Printf("   • Time: %s\n", recommended.EstimatedTime)
		fmt.Printf("   • Network: %s\n", recommended.NetworkCongestion)
	},
}

var networkStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "View network congestion status",
	Run: func(cmd *cobra.Command, args []string) {
		model := gas.NewGasModel(gas.MinGasPrice, 15_000_000) // Fixed block gas limit
		estimator := gas.NewGasEstimator(model)
		preview := gas.NewFeePreview(estimator)
		
		congestion := preview.GetNetworkCongestion()
		fmt.Printf("\n🌐 Network Status: %s\n", congestion)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately
func Execute() error {
	return rootCmd.Execute()
}