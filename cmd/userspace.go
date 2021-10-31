package cmd

import (
	"github.com/knabben/kpng-win/pkg/userspace"
	"github.com/spf13/cobra"
	"sigs.k8s.io/kpng/client/localsink"
)

func UserspaceCommand(run func(sink localsink.Sink) error) *cobra.Command {
	return &cobra.Command{
		Use: "userspace",
		Short:"Windows Userspace Mode",
		RunE: func(cmd *cobra.Command, args []string) error {
			backend := userspace.NewBackend()
			return run(backend.Sink())
		},
	}
}
