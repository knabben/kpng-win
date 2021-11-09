package userspace

import (
	v1 "k8s.io/api/core/v1"
	"net"
	"sigs.k8s.io/kpng/api/localnetv1"
)

// LoadBalancer is an interface for distributing incoming requests to service endpoints.
type LoadBalancer interface {
	//// NextEndpoint returns the endpoint to handle a request for the given
	//// service-port and source address.

	NextEndpoint(service ServicePortName, srcAddr net.Addr, sessionAffinityReset bool) (string, error)
	NewService(service ServicePortName, sessionAffinityType v1.ServiceAffinity, stickyMaxAgeMinutes int) error
	DeleteService(service ServicePortName)

	// EndpointsHandler
	OnEndpointsAdd(endpoint *localnetv1.Endpoint, service *localnetv1.Service)
	OnEndpointsDelete(endpoint *localnetv1.Endpoint, service *localnetv1.Service)
	OnEndpointsUpdate(oldEndpoints, endpoints *localnetv1.Endpoint, service *localnetv1.Service)
	OnEndpointsSynced()

	// todo - sticky sessions
	CleanupStaleStickySessions(service ServicePortName)
}
