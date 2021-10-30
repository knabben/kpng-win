package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"strings"
)

func main() {
	var cmdEcho = &cobra.Command{
		Use:   "echo [string to echo]",
		Short: "Echo anything to the screen",
		Long: `echo is for echoing anything back.
Echo works a lot like print, except it has a child command.`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Echo: " + strings.Join(args, " "))
		},
	}

	var rootCmd = &cobra.Command{Use: "winkernel"}
	rootCmd.AddCommand(cmdEcho)
	rootCmd.Execute()
}
