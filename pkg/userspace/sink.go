package userspace

import (
	"fmt"
	"github.com/spf13/pflag"
	"log"
	"sigs.k8s.io/kpng/api/localnetv1"

	netutils "k8s.io/utils/net"

	"sigs.k8s.io/kpng/client/localsink"
	"sigs.k8s.io/kpng/client/localsink/decoder"
	"sigs.k8s.io/kpng/client/localsink/filterreset"
	"sync"
	"time"
)

var wg = sync.WaitGroup{}
var hostname string
var _ decoder.Interface = &Backend{}

// assert Proxier is a proxy.Provider
var proxier = &Proxier{}

type Backend struct {
	localsink.Config
}

func NewBackend() *Backend {
	return &Backend{}
}

func (s *Backend) Sink() localsink.Sink {
	return filterreset.New(decoder.New(s))
}

func (s *Backend) BindFlags(flags *pflag.FlagSet) {}

func (s *Backend) Setup() {
	var err error
	loadBalancer := NewLoadBalancerRR()
	proxier, err = NewProxier(loadBalancer, netutils.ParseIPSloppy("0.0.0.0"), 20*time.Second, 20*time.Second)
	if err != nil {
		log.Fatal(1)
	}
	fmt.Println("setup all other shit ")
}

func (s *Backend) Reset() { /* noop, we're wrapped in filterreset */ }

func (s *Backend) Sync() {
	fmt.Println("sync")
	fmt.Println("cleaning up stale registers")
}

func (s *Backend) SetService(svc *localnetv1.Service) {
	proxier.OnServiceAdd(svc)
}

func (s *Backend) DeleteService(namespace, name string) {
	fmt.Println(namespace, name)
	//for _, impl := range IptablesImpl {
	//	impl.serviceChanges.Delete(namespace, name)
	//}
}

func (s *Backend) SetEndpoint(namespace, serviceName, key string, endpoint *localnetv1.Endpoint) {
	fmt.Println(namespace, serviceName, key, endpoint)
	//proxier.OnEndpointsAdd(endpoint) - creating base endpoint
}

func (s *Backend) DeleteEndpoint(namespace, serviceName, key string) {
	fmt.Println(namespace, serviceName, key)
	//for _, impl := range IptablesImpl {
	//	impl.endpointsChanges.EndpointUpdate(namespace, serviceName, key, nil)
	//}
}
