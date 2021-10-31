package winkernel

import (
	"fmt"
	"github.com/knabben/kpng-win/pkg/proxy"
	"log"
	"net"
	"sync"
	"time"

	"github.com/spf13/pflag"
	"sigs.k8s.io/kpng/api/localnetv1"

	"sigs.k8s.io/kpng/client/localsink/decoder"
	"sigs.k8s.io/kpng/client/localsink/filterreset"
	//
	//v1 "k8s.io/api/core/v1"
	//"k8s.io/utils/exec"

	//"sigs.k8s.io/kpng/backends/iptables/util"
	//localnetv1 "sigs.k8s.io/kpng/api/localnetv1"
	"sigs.k8s.io/kpng/client/localsink"
	//"sigs.k8s.io/kpng/client/localsink/decoder"
	//"sigs.k8s.io/kpng/client/localsink/filterreset"
)

var (
	wg = sync.WaitGroup{}
	hostname string
	_ decoder.Interface = &Backend{}
	proxier proxy.Provider = &Proxier{}
)

type Backend struct {
	localsink.Config
}

func New() *Backend {
	return &Backend{}
}

func (s *Backend) Sink() localsink.Sink {
	return filterreset.New(decoder.New(s))
}

func (s *Backend) BindFlags(flags *pflag.FlagSet) {
}

func (s *Backend) Setup() {
	var err error
	var ip = net.IP{}
	proxier, err = NewProxier(
		time.Second * 2, // syncPeriod time.Duration,
		time.Second * 2,  // minSyncPeriod time.Duration,
		false, // masqueradeAll bool,
		0, // masqueradeBit int,
		"", // clusterCIDR string,
		"",  // hostname string,
		ip, // nodeIP net.IP,// 	recorder events.EventRecorder,
		proxy.KubeProxyWinkernelConfiguration{},  // config proxy.KubeProxyWinkernelConfiguration,
	)
	if err != nil {
		log.Fatal(err)
	}

	// start and create service vs. endpoints struct

	//hostname = s.NodeName
	//IptablesImpl = make(map[v1.IPFamily]*iptables)
	//for _, protocol := range []v1.IPFamily{v1.IPv4Protocol, v1.IPv6Protocol} {
	//	iptable := NewIptables()
	//	iptable.iptInterface = util.NewIPTableExec(exec.New(), util.Protocol(protocol))
	//	iptable.serviceChanges = NewServiceChangeTracker(newServiceInfo, protocol, iptable.recorder)
	//	iptable.endpointsChanges = NewEndpointChangeTracker(hostname, protocol, iptable.recorder)
	//	IptablesImpl[protocol] = iptable
	//}1
}

func (s *Backend) Reset() { /* noop, we're wrapped in filterreset */ }

func (s *Backend) Sync() {
	fmt.Println("sync")
	//for _, impl := range IptablesImpl {
	//	wg.Add(1)
	//	go impl.sync()
	//}
	//wg.Wait()
}

func (s *Backend) SetService(svc *localnetv1.Service) {
	fmt.Println(svc)
	//for _, impl := range IptablesImpl {
	//	impl.serviceChanges.Update(svc)
	//}
	//proxier.OnServiceUpdate(nil, service)
}

func (s *Backend) DeleteService(namespace, name string) {
	fmt.Println(namespace, name)
	//for _, impl := range IptablesImpl {
	//	impl.serviceChanges.Delete(namespace, name)
	//}
	//proxier.OnServiceUpdate(service, nil)

}

func (s *Backend) SetEndpoint(namespace, serviceName, key string, endpoint *localnetv1.Endpoint) {
	fmt.Println(namespace, serviceName, key, endpoint)
	//for _, impl := range IptablesImpl {
	//	impl.endpointsChanges.EndpointUpdate(namespace, serviceName, key, endpoint)
	//}

}

func (s *Backend) DeleteEndpoint(namespace, serviceName, key string) {
	fmt.Println(namespace, serviceName, key)
	//for _, impl := range IptablesImpl {
	//	impl.endpointsChanges.EndpointUpdate(namespace, serviceName, key, nil)
	//}
}
