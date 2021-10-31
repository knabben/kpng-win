package userspace

import (
	"errors"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"net"
	"sigs.k8s.io/kpng/client"
	"sync"
	"time"
)

var (
	ErrMissingServiceEntry = errors.New("missing service entry")
	ErrMissingEndpoints    = errors.New("missing endpoints")
)

// LoadBalancerRR is a round-robin load balancer.
type LoadBalancerRR struct {
	lock     sync.RWMutex
	services map[ServicePortName]*balancerState
}

// Ensure this implements LoadBalancer.
var _ LoadBalancer = &LoadBalancerRR{}

type balancerState struct {
	endpoints []string // a list of "ip:port" style strings
	index     int      // current index into endpoints
	affinity affinityPolicy
}

type affinityState struct {
	clientIP string
	//clientProtocol  api.Protocol //not yet used
	//sessionCookie   string       //not yet used
	endpoint string``
	lastUsed time.Time
}

type affinityPolicy struct {
	affinityType v1.ServiceAffinity
	affinityMap  map[string]*affinityState // map client IP -> affinity info
	ttlSeconds   int
}

// NewLoadBalancerRR returns a new LoadBalancerRR.
func NewLoadBalancerRR() *LoadBalancerRR {
	return &LoadBalancerRR{
		services: map[ServicePortName]*balancerState{},
	}
}

func (lb *LoadBalancerRR) NewService(svcPort ServicePortName, affinityType v1.ServiceAffinity, ttlSeconds int) error {
	fmt.Println("LoadBalancerRR NewService", "servicePortName", svcPort)
	lb.lock.Lock()
	defer lb.lock.Unlock()
	lb.newServiceInternal(svcPort, affinityType, ttlSeconds)
	return nil
}

func (lb *LoadBalancerRR) OnEndpointsAdd(endpoints *client.ServiceEndpoints) {
	service := endpoints.Service
	portsToEndpoints := buildPortsToEndpointsMap(endpoints)
	fmt.Println(portsToEndpoints)
	lb.lock.Lock()
	defer lb.lock.Unlock()

	for portname := range portsToEndpoints {
		svcPort := ServicePortName{
			NamespacedName: types.NamespacedName{Namespace: service.Namespace, Name: service.Name}, Port: portname,
		}
		newEndpoints := portsToEndpoints[portname]
		state, exists := lb.services[svcPort]

		if !exists || state == nil || len(newEndpoints) > 0 {
			fmt.Println("LoadBalancerRR: Setting endpoints service", "servicePortName", svcPort, "endpoints", newEndpoints)
			//lb.updateAffinityMap(svcPort, newEndpoints)
			// OnEndpointsAdd can be called without NewService being called externally.
			// To be safe we will call it here.  A new service will only be created
			// if one does not already exist.  The affinity will be updated
			// later, once NewService is called.
			state = lb.newServiceInternal(svcPort, v1.ServiceAffinity(""), 0)
			state.endpoints = ShuffleStrings(newEndpoints)
			// Reset the round-robin index.
			state.index = 0
		}
	}
}

// This assumes that lb.lock is already held.
func (lb *LoadBalancerRR) newServiceInternal(svcPort ServicePortName, affinityType v1.ServiceAffinity, ttlSeconds int) *balancerState {
	if ttlSeconds == 0 {
		ttlSeconds = int(v1.DefaultClientIPServiceAffinitySeconds) //default to 3 hours if not specified.  Should 0 be unlimited instead????
	}
	if _, exists := lb.services[svcPort]; !exists {
		lb.services[svcPort] = &balancerState{}
		fmt.Println("LoadBalancerRR service did not exist, created", "servicePortName", svcPort)
	}
	return lb.services[svcPort]
}

// ShuffleStrings copies strings from the specified slice into a copy in random
// order. It returns a new slice.
func ShuffleStrings(s []string) []string {
	if s == nil {
		return nil
	}
	shuffled := make([]string, len(s))
	perm := utilrand.Perm(len(s))
	for i, j := range perm {
		shuffled[j] = s[i]
	}
	return shuffled
}


// NextEndpoint returns a service endpoint.
// The service endpoint is chosen using the round-robin algorithm.
func (lb *LoadBalancerRR) NextEndpoint(svcPort ServicePortName, srcAddr net.Addr, sessionAffinityReset bool) (string, error) {
	// Coarse locking is simple.  We can get more fine-grained if/when we
	// can prove it matters.
	lb.lock.Lock()
	defer lb.lock.Unlock()

	state, exists := lb.services[svcPort]
	if !exists || state == nil {
		return "", ErrMissingServiceEntry
	}
	if len(state.endpoints) == 0 {
		return "", ErrMissingEndpoints
	}
	fmt.Println("NextEndpoint for service", "servicePortName", svcPort, "address", srcAddr, "endpoints", state.endpoints)
	//sessionAffinityEnabled := isSessionAffinity(&state.affinity)

	//var ipaddr string
	//if sessionAffinityEnabled {
	//	// Caution: don't shadow ipaddr
	//	var err error
	//	ipaddr, _, err = net.SplitHostPort(srcAddr.String())
	//	if err != nil {
	//		return "", fmt.Errorf("malformed source address %q: %v", srcAddr.String(), err)
	//	}
	//	if !sessionAffinityReset {
	//		sessionAffinity, exists := state.affinity.affinityMap[ipaddr]
	//		if exists && int(time.Since(sessionAffinity.lastUsed).Seconds()) < state.affinity.ttlSeconds {
	//			// Affinity wins.
	//			endpoint := sessionAffinity.endpoint
	//			sessionAffinity.lastUsed = time.Now()
	//			klog.V(4).InfoS("NextEndpoint for service from IP with sessionAffinity", "servicePortName", svcPort, "IP", ipaddr, "sessionAffinity", sessionAffinity, "endpoint", endpoint)
	//			return endpoint, nil
	//		}
	//	}
	//}
	// Take the next endpoint.
	endpoint := state.endpoints[state.index]
	state.index = (state.index + 1) % len(state.endpoints)

	//if sessionAffinityEnabled {
	//	var affinity *affinityState
	//	affinity = state.affinity.affinityMap[ipaddr]
	//	if affinity == nil {
	//		affinity = new(affinityState) //&affinityState{ipaddr, "TCP", "", endpoint, time.Now()}
	//		state.affinity.affinityMap[ipaddr] = affinity
	//	}
	//	affinity.lastUsed = time.Now()
	//	affinity.endpoint = endpoint
	//	affinity.clientIP = ipaddr
	//	klog.V(4).InfoS("Updated affinity key", "IP", ipaddr, "affinityState", state.affinity.affinityMap[ipaddr])
	//}

	return endpoint, nil
}
