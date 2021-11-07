package userspace

import (
	"fmt"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/types"
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

	// keep tracking of services endpoints object and convert
	// to internal backend
	servicesEndpoints map[types.NamespacedName]*ServiceEndpoint
}

type ServiceEndpoint struct {
	Service  *localnetv1.Service
	Endpoint *localnetv1.Endpoint
}

func (b *Backend) AddService(service *localnetv1.Service) {
	namespaceNamed := proxier.CreateNamespacedName(service)
	_, exists := b.servicesEndpoints[namespaceNamed]
	if !exists {
		b.servicesEndpoints[namespaceNamed] = &ServiceEndpoint{
			Service: service,
			Endpoint: nil,
		}
	} else {
		b.servicesEndpoints[namespaceNamed].Service = service
	}
}

func (b *Backend) AddEndpoint(service *localnetv1.Service, endpoint *localnetv1.Endpoint) {
	namespaceNamed := proxier.CreateNamespacedName(service)
	serviceEndpoint, exists := b.servicesEndpoints[namespaceNamed]
	if exists && serviceEndpoint.Endpoint == nil {
		b.servicesEndpoints[namespaceNamed].Endpoint = endpoint
	} else if !exists {
		b.servicesEndpoints[namespaceNamed] = &ServiceEndpoint{
			Service: service,
			Endpoint: endpoint,
		}
	}
}

func (b *Backend) GetService(name, namespace string) (*ServiceEndpoint, bool) {
	// todo - lock
	namespaceNamed := proxier.CreateNamespacedNameString(name, namespace)
	oldServiceEndpoint, exists := b.servicesEndpoints[namespaceNamed]
	return oldServiceEndpoint, exists
}

func (b *Backend) GetEndpoint(service *localnetv1.Service, endpoint *localnetv1.Endpoint) {
	namespaceNamed := proxier.CreateNamespacedName(service)
	b.servicesEndpoints[namespaceNamed].Endpoint = endpoint
}


func NewBackend() *Backend {
	return &Backend{
		servicesEndpoints: make(map[types.NamespacedName]*ServiceEndpoint),
	}
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
	fmt.Println("cleaning up stale registers for affinity impl")
}

func (s *Backend) SetService(svc *localnetv1.Service) {
	if cacheSvcEndpoint, exists := s.GetService(svc.Name, svc.Namespace); exists {
		proxier.OnServiceUpdate(cacheSvcEndpoint.Service, svc)
	} else {
		proxier.OnServiceAdd(svc)
	}
	s.AddService(svc)
}

func (s *Backend) DeleteService(namespace, name string) {
	if oldSvc, exists := s.GetService(name, namespace); exists {
		proxier.OnServiceDelete(oldSvc.Service)
		namespacedName := proxier.CreateNamespacedNameString(name, namespace)
		delete(s.servicesEndpoints, namespacedName)
	}
}

func (s *Backend) SetEndpoint(namespace, serviceName, key string, endpoint *localnetv1.Endpoint) {
	cacheSvcEndpoint, exists := s.GetService(serviceName, namespace)
	if !exists || (exists && cacheSvcEndpoint.Endpoint == nil) {
		proxier.OnEndpointsAdd(endpoint, cacheSvcEndpoint.Service) // - creating base endpoint
	} else {
		proxier.OnEndpointsUpdate(cacheSvcEndpoint.Endpoint, endpoint)
	}
	s.AddEndpoint(cacheSvcEndpoint.Service, endpoint)
}

func (s *Backend) DeleteEndpoint(namespace, serviceName, key string) {
	if cacheSvcEndpoint, exists := s.GetService(serviceName, namespace); exists {
		proxier.OnEndpointsDelete(cacheSvcEndpoint.Endpoint)
	}
}
