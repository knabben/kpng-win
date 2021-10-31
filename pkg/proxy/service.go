package proxy

import (
	"fmt"
	utilproxy "github.com/knabben/kpng-win/pkg/proxy/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	netutils "k8s.io/utils/net"
	"net"
	"reflect"
	"strings"
	"sync"
)

// BaseServiceInfo contains base information that defines a service.
// This could be used directly by proxier while processing services,
// or can be used for constructing a more specific ServiceInfo struct
// defined by the proxier if needed.
type BaseServiceInfo struct {
	clusterIP                net.IP
	port                     int
	protocol                 v1.Protocol
	nodePort                 int
	loadBalancerStatus       v1.LoadBalancerStatus
	sessionAffinityType      v1.ServiceAffinity
	stickyMaxAgeSeconds      int
	externalIPs              []string
	loadBalancerSourceRanges []string
	healthCheckNodePort      int
	nodeLocalExternal        bool
	nodeLocalInternal        bool
	internalTrafficPolicy    *v1.ServiceInternalTrafficPolicyType
	hintsAnnotation          string
}

var _ ServicePort = &BaseServiceInfo{}

// String is part of ServicePort interface.
func (info *BaseServiceInfo) String() string {
	return fmt.Sprintf("%s:%d/%s", info.clusterIP, info.port, info.protocol)
}

// ClusterIP is part of ServicePort interface.
func (info *BaseServiceInfo) ClusterIP() net.IP {
	return info.clusterIP
}

// Port is part of ServicePort interface.
func (info *BaseServiceInfo) Port() int {
	return info.port
}

// SessionAffinityType is part of the ServicePort interface.
func (info *BaseServiceInfo) SessionAffinityType() v1.ServiceAffinity {
	return info.sessionAffinityType
}

// StickyMaxAgeSeconds is part of the ServicePort interface
func (info *BaseServiceInfo) StickyMaxAgeSeconds() int {
	return info.stickyMaxAgeSeconds
}

// Protocol is part of ServicePort interface.
func (info *BaseServiceInfo) Protocol() v1.Protocol {
	return info.protocol
}

// LoadBalancerSourceRanges is part of ServicePort interface
func (info *BaseServiceInfo) LoadBalancerSourceRanges() []string {
	return info.loadBalancerSourceRanges
}

// HealthCheckNodePort is part of ServicePort interface.
func (info *BaseServiceInfo) HealthCheckNodePort() int {
	return info.healthCheckNodePort
}

// NodePort is part of the ServicePort interface.
func (info *BaseServiceInfo) NodePort() int {
	return info.nodePort
}

// ExternalIPStrings is part of ServicePort interface.
func (info *BaseServiceInfo) ExternalIPStrings() []string {
	return info.externalIPs
}

// LoadBalancerIPStrings is part of ServicePort interface.
func (info *BaseServiceInfo) LoadBalancerIPStrings() []string {
	var ips []string
	for _, ing := range info.loadBalancerStatus.Ingress {
		ips = append(ips, ing.IP)
	}
	return ips
}

// NodeLocalExternal is part of ServicePort interface.
func (info *BaseServiceInfo) NodeLocalExternal() bool {
	return info.nodeLocalExternal
}

// NodeLocalInternal is part of ServicePort interface
func (info *BaseServiceInfo) NodeLocalInternal() bool {
	return info.nodeLocalInternal
}

// InternalTrafficPolicy is part of ServicePort interface
func (info *BaseServiceInfo) InternalTrafficPolicy() *v1.ServiceInternalTrafficPolicyType {
	return info.internalTrafficPolicy
}

// HintsAnnotation is part of ServicePort interface.
func (info *BaseServiceInfo) HintsAnnotation() string {
	return info.hintsAnnotation
}

type makeServicePortFunc func(*v1.ServicePort, *v1.Service, *BaseServiceInfo) ServicePort

// This handler is invoked by the apply function on every change. This function should not modify the
// ServiceMap's but just use the changes for any Proxier specific cleanup.
type processServiceMapChangeFunc func(previous, current ServiceMap)

// serviceChange contains all changes to services that happened since proxy rules were synced.  For a single object,
// changes are accumulated, i.e. previous is state from before applying the changes,
// current is state after applying all of the changes.
type serviceChange struct {
	previous ServiceMap
	current  ServiceMap
}

// ServiceChangeTracker carries state about uncommitted changes to an arbitrary number of
// Services, keyed by their namespace and name.
type ServiceChangeTracker struct {
	// lock protects items.
	lock sync.Mutex
	// items maps a service to its serviceChange.
	items map[types.NamespacedName]*serviceChange
	// makeServiceInfo allows proxier to inject customized information when processing service.
	makeServiceInfo         makeServicePortFunc
	processServiceMapChange processServiceMapChangeFunc
	ipFamily                v1.IPFamily

	recorder events.EventRecorder
}

// NewServiceChangeTracker initializes a ServiceChangeTracker
func NewServiceChangeTracker(makeServiceInfo makeServicePortFunc, ipFamily v1.IPFamily,processServiceMapChange processServiceMapChangeFunc) *ServiceChangeTracker {
	return &ServiceChangeTracker{
		items:                   make(map[types.NamespacedName]*serviceChange),
		makeServiceInfo:         makeServiceInfo,
		//recorder:                recorder,
		ipFamily:                ipFamily,
		processServiceMapChange: processServiceMapChange,
	}
}

// ServiceMap maps a service to its ServicePort.
type ServiceMap map[ServicePortName]ServicePort

// serviceToServiceMap translates a single Service object to a ServiceMap.
//
// NOTE: service object should NOT be modified.
func (sct *ServiceChangeTracker) serviceToServiceMap(service *v1.Service) ServiceMap {
	if service == nil {
		return nil
	}

	//if utilproxy.ShouldSkipService(service) {
	//	return nil
	//}

	clusterIP := utilproxy.GetClusterIPByFamily(sct.ipFamily, service)
	if clusterIP == "" {
		return nil
	}

	serviceMap := make(ServiceMap)
	svcName := types.NamespacedName{Namespace: service.Namespace, Name: service.Name}
	for i := range service.Spec.Ports {
		servicePort := &service.Spec.Ports[i]
		svcPortName := ServicePortName{NamespacedName: svcName, Port: servicePort.Name, Protocol: servicePort.Protocol}
		baseSvcInfo := sct.newBaseServiceInfo(servicePort, service)
		if sct.makeServiceInfo != nil {
			serviceMap[svcPortName] = sct.makeServiceInfo(servicePort, service, baseSvcInfo)
		} else {
			serviceMap[svcPortName] = baseSvcInfo
		}
	}
	return serviceMap
}

// Update updates given service's change map based on the <previous, current> service pair.  It returns true if items changed,
// otherwise return false.  Update can be used to add/update/delete items of ServiceChangeMap.  For example,
// Add item
//   - pass <nil, service> as the <previous, current> pair.
// Update item
//   - pass <oldService, service> as the <previous, current> pair.
// Delete item
//   - pass <service, nil> as the <previous, current> pair.
func (sct *ServiceChangeTracker) Update(previous, current *v1.Service) bool {
	svc := current
	if svc == nil {
		svc = previous
	}
	// previous == nil && current == nil is unexpected, we should return false directly.
	if svc == nil {
		return false
	}
	//metrics.ServiceChangesTotal.Inc()
	namespacedName := types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}

	sct.lock.Lock()
	defer sct.lock.Unlock()

	change, exists := sct.items[namespacedName]
	if !exists {
		change = &serviceChange{}
		change.previous = sct.serviceToServiceMap(previous)
		sct.items[namespacedName] = change
	}
	change.current = sct.serviceToServiceMap(current)
	// if change.previous equal to change.current, it means no change
	if reflect.DeepEqual(change.previous, change.current) {
		delete(sct.items, namespacedName)
	} else {
		klog.V(2).InfoS("Service updated ports", "service", klog.KObj(svc), "portCount", len(change.current))
	}
	//metrics.ServiceChangesPending.Set(float64(len(sct.items)))
	return len(sct.items) > 0
}

func (sct *ServiceChangeTracker) newBaseServiceInfo(port *v1.ServicePort, service *v1.Service) *BaseServiceInfo {
	nodeLocalExternal := false
	//if apiservice.RequestsOnlyLocalTraffic(service) {
	//	nodeLocalExternal = true
	//}
	nodeLocalInternal := false
	//if utilfeature.DefaultFeatureGate.Enabled(features.ServiceInternalTrafficPolicy) {
	//	nodeLocalInternal = apiservice.RequestsOnlyLocalTrafficForInternal(service)
	//}
	var stickyMaxAgeSeconds int
	if service.Spec.SessionAffinity == v1.ServiceAffinityClientIP {
		// Kube-apiserver side guarantees SessionAffinityConfig won't be nil when session affinity type is ClientIP
		stickyMaxAgeSeconds = int(*service.Spec.SessionAffinityConfig.ClientIP.TimeoutSeconds)
	}

	clusterIP := utilproxy.GetClusterIPByFamily(sct.ipFamily, service)
	info := &BaseServiceInfo{
		clusterIP:             netutils.ParseIPSloppy(clusterIP),
		port:                  int(port.Port),
		protocol:              port.Protocol,
		nodePort:              int(port.NodePort),
		sessionAffinityType:   service.Spec.SessionAffinity,
		stickyMaxAgeSeconds:   stickyMaxAgeSeconds,
		nodeLocalExternal:     nodeLocalExternal,
		nodeLocalInternal:     nodeLocalInternal,
		internalTrafficPolicy: service.Spec.InternalTrafficPolicy,
		hintsAnnotation:       service.Annotations[v1.AnnotationTopologyAwareHints],
	}

	loadBalancerSourceRanges := make([]string, len(service.Spec.LoadBalancerSourceRanges))
	for i, sourceRange := range service.Spec.LoadBalancerSourceRanges {
		loadBalancerSourceRanges[i] = strings.TrimSpace(sourceRange)
	}
	// filter external ips, source ranges and ingress ips
	// prior to dual stack services, this was considered an error, but with dual stack
	// services, this is actually expected. Hence we downgraded from reporting by events
	// to just log lines with high verbosity

	ipFamilyMap := utilproxy.MapIPsByIPFamily(service.Spec.ExternalIPs)
	info.externalIPs = ipFamilyMap[sct.ipFamily]

	// Log the IPs not matching the ipFamily
	if ips, ok := ipFamilyMap[utilproxy.OtherIPFamily(sct.ipFamily)]; ok && len(ips) > 0 {
		klog.V(4).InfoS("Service change tracker ignored the following external IPs for given service as they don't match IP Family",
			"ipFamily", sct.ipFamily, "externalIPs", strings.Join(ips, ","), "service", klog.KObj(service))
	}

	ipFamilyMap = utilproxy.MapCIDRsByIPFamily(loadBalancerSourceRanges)
	info.loadBalancerSourceRanges = ipFamilyMap[sct.ipFamily]
	// Log the CIDRs not matching the ipFamily
	if cidrs, ok := ipFamilyMap[utilproxy.OtherIPFamily(sct.ipFamily)]; ok && len(cidrs) > 0 {
		klog.V(4).InfoS("Service change tracker ignored the following load balancer source ranges for given Service as they don't match IP Family",
			"ipFamily", sct.ipFamily, "loadBalancerSourceRanges", strings.Join(cidrs, ","), "service", klog.KObj(service))
	}

	// Obtain Load Balancer Ingress IPs
	var ips []string
	for _, ing := range service.Status.LoadBalancer.Ingress {
		if ing.IP != "" {
			ips = append(ips, ing.IP)
		}
	}

	if len(ips) > 0 {
		ipFamilyMap = utilproxy.MapIPsByIPFamily(ips)

		if ipList, ok := ipFamilyMap[utilproxy.OtherIPFamily(sct.ipFamily)]; ok && len(ipList) > 0 {
			klog.V(4).InfoS("Service change tracker ignored the following load balancer ingress IPs for given Service as they don't match the IP Family",
				"ipFamily", sct.ipFamily, "loadBalancerIngressIps", strings.Join(ipList, ","), "service", klog.KObj(service))
		}
		// Create the LoadBalancerStatus with the filtered IPs
		for _, ip := range ipFamilyMap[sct.ipFamily] {
			info.loadBalancerStatus.Ingress = append(info.loadBalancerStatus.Ingress, v1.LoadBalancerIngress{IP: ip})
		}
	}

	//if apiservice.NeedsHealthCheck(service) {
	//	p := service.Spec.HealthCheckNodePort
	//	if p == 0 {
	//		klog.ErrorS(nil, "Service has no healthcheck nodeport", "service", klog.KObj(service))
	//	} else {
	//		info.healthCheckNodePort = int(p)
	//	}
	//}

	return info
}

