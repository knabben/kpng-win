package winkernel

//
//func LocalCmds( run func(sink localsink.Sink) error ) (cmds []*cobra.Command) {
//	// classic backends
//	cfg := &localsink.Config{}
//	sink := fullstate.New(cfg)
//
//	for _, cmd := range BackendCmds(sink, run) {
//		cfg.BindFlags(cmd.Flags())
//		cmds = append(cmds, cmd)
//	}
//
//	// sink backends
//	ipvsBackend := ipvssink.New()
//
//	cmd := &cobra.Command{
//		Use: "to-ipvs",
//		RunE: func(_ *cobra.Command, _ []string) error {
//			return run(ipvsBackend.Sink())
//		},
//	}
//
//	ipvsBackend.BindFlags(cmd.Flags())
//
//	cmds = append(cmds, cmd)
//
//	return
//}
//
