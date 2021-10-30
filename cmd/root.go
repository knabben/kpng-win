package cmd

import (
	"context"
	"github.com/spf13/cobra"
	"sigs.k8s.io/kpng/client/localsink"
	"sigs.k8s.io/kpng/server/jobs/api2local"
)

var (
	rootCmd = &cobra.Command{
		Use:   "kpng",
		Short: "Nothing here, choose a backend",
		Long: `Backends available are userspace and winkernel.`,
	}
)

func LocalCmds(run func(sink localsink.Sink) error) (cmds []*cobra.Command) {
	return []*cobra.Command{
		WinkernelCommand(run),
		UserspaceCommand(run),
	}
}

func Execute() error {
	ctx := context.Background()
	flags := rootCmd.PersistentFlags()

	rootCmd.AddCommand(LocalCmds(func(sink localsink.Sink) (err error) {
		job := api2local.New(sink)
		job.BindFlags(flags)
		job.Run(ctx)
		return
	})...)

 	return rootCmd.Execute()
}
