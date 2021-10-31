package userspace

import (
	"net"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/kpng/client"
)

// LoadBalancer is an interface for distributing incoming requests to service endpoints.
type LoadBalancer interface {
	//// NextEndpoint returns the endpoint to handle a request for the given
	//// service-port and source address.
	NextEndpoint(service ServicePortName, srcAddr net.Addr, sessionAffinityReset bool) (string, error)
	NewService(service ServicePortName, sessionAffinityType v1.ServiceAffinity, stickyMaxAgeMinutes int) error
	OnEndpointsAdd(endpoints *client.ServiceEndpoints)
	//DeleteService(service ServicePortName)
	//CleanupStaleStickySessions(service ServicePortName)

	//proxyconfig.EndpointsHandler
}
