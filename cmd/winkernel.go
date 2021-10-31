package cmd

import (
	"github.com/knabben/kpng-win/pkg/proxy/winkernel"
	"github.com/spf13/cobra"
	"sigs.k8s.io/kpng/client/localsink"
)

func WinkernelCommand(run func(sink localsink.Sink) error) *cobra.Command {
	backend := winkernel.New()
	cmd := &cobra.Command{
		Use: "winkernel",
		Short:"Windows Kernel Mode",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(backend.Sink())
		},
	}
	backend.BindFlags(cmd.Flags())
	return cmd
}
