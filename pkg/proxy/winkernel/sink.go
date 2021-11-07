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
	ip, _, err := net.ParseCIDR("192.168.0.12/32")
	proxier, err = NewProxier(
		time.Second * 2, // syncPeriod time.Duration,
		time.Second * 2,  // minSyncPeriod time.Duration,
		false, // masqueradeAll bool,
		0, // masqueradeBit int,
		"", // clusterCIDR string,
		"",  // hostname string,
		ip, // nodeIP net.IP,// 	recorder events.EventRecorder,
		proxy.KubeProxyWinkernelConfiguration{
			NetworkName: "flannel.4096",
			SourceVip: "192.168.0.12",
		},  // config proxy.KubeProxyWinkernelConfiguration,
	)
	if err != nil {
		log.Fatal(err)
	}
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

func (s *Backend) SetService(service *localnetv1.Service) {
	svcChangeTracker := proxier.GetServiceChangeTracker()
	svcName := svcChangeTracker.GetName(service.Namespace, service.Name)
	oldService, exists := svcChangeTracker.PersistentServices[svcName]
	if !exists {
		fmt.Println("Adding service", service)
		proxier.OnServiceAdd(service)
		return
	}
	fmt.Println("Updating service", service)
	proxier.OnServiceUpdate(oldService, service)
	fmt.Println(svcChangeTracker)
}

func (s *Backend) DeleteService(namespace, name string) {
	svcChangeTracker := proxier.GetServiceChangeTracker()
	svcName := svcChangeTracker.GetName(namespace, name)
	service := svcChangeTracker.PersistentServices[svcName]
	fmt.Println("Deleting service", namespace, name)
	proxier.OnServiceDelete(service)
	delete(svcChangeTracker.PersistentServices, svcName)
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
