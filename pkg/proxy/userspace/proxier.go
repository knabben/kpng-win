package userspace

import (
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	"k8s.io/utils/exec"
	netutils "k8s.io/utils/net"
	"sigs.k8s.io/kpng/api/localnetv1"
	"strconv"
	"sync/atomic"

	"net"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	utilnet "k8s.io/apimachinery/pkg/util/net"
)

type portal struct {
	ip         string
	port       int32
	isExternal bool
}

// ServicePortName carries a namespace + name + portname.  This is the unique
// identifier for a load-balanced service.
type ServicePortName struct {
	types.NamespacedName
	Port     string
	Protocol v1.Protocol
}

// ServicePortPortalName carries a namespace + name + portname + portalip.  This is the unique
// identifier for a windows service port portal.
type ServicePortPortalName struct {
	types.NamespacedName
	Port         string
	PortalIPName string
}

func (spn ServicePortPortalName) String() string {
	return fmt.Sprintf("%s:%s:%s", spn.NamespacedName.String(), spn.Port, spn.PortalIPName)
}

type serviceInfo struct {
	isAliveAtomic       int32 // Only access this with atomic ops
	portal              portal
	protocol            localnetv1.Protocol
	socket              proxySocket
	timeout             time.Duration
	activeClients       *clientCache
	sessionAffinityType v1.ServiceAffinity
}

func (info *serviceInfo) isAlive() bool {
	return atomic.LoadInt32(&info.isAliveAtomic) != 0
}

func (info *serviceInfo) setAlive(b bool) {
	var i int32
	if b {
		i = 1
	}
	atomic.StoreInt32(&info.isAliveAtomic, i)
}

// Proxier is a simple proxy for TCP connections between a localhost:lport
// and services that provide the actual implementations.
type Proxier struct {
	//// EndpointSlice support has not been added for this proxier yet.
	//config.NoopEndpointSliceHandler
	//// TODO(imroc): implement node handler for winuserspace proxier.
	//config.NoopNodeHandler

	loadBalancer   LoadBalancer
	mu             sync.Mutex // protects serviceMap
	serviceMap     map[ServicePortPortalName]*serviceInfo
	syncPeriod     time.Duration
	udpIdleTimeout time.Duration
	numProxyLoops  int32     // use atomic ops to access this; mostly for testing
	netsh          Interface // todo(knabben) need this to windows compilation
	hostIP         net.IP
}

// NewProxier returns a new Proxier given a LoadBalancer and an address on
// which to listen. It is assumed that there is only a single Proxier active
// on a machine. An error will be returned if the proxier cannot be started
// due to an invalid ListenIP (loopback)
func NewProxier(loadBalancer LoadBalancer, listenIP net.IP, syncPeriod, udpIdleTimeout time.Duration) (*Proxier, error) {
	//if listenIP.Equal(localhostIPv4) || listenIP.Equal(localhostIPv6) {
	//	return nil, ErrProxyOnLocalhost
	//}

	hostIP, err := utilnet.ChooseHostInterface()
	if err != nil {
		return nil, fmt.Errorf("failed to select a host interface: %v", err)
	}

	fmt.Println("Setting proxy", "ip", hostIP)
	return createProxier(loadBalancer, listenIP, hostIP, syncPeriod, udpIdleTimeout)
}

func createProxier(loadBalancer LoadBalancer, listenIP net.IP, hostIP net.IP, syncPeriod, udpIdleTimeout time.Duration) (*Proxier, error) {
	return &Proxier{
		loadBalancer:      loadBalancer,
		serviceMap:        make(map[ServicePortPortalName]*serviceInfo),
		syncPeriod:        syncPeriod,
		netsh:             New(exec.New()),
		udpIdleTimeout:    udpIdleTimeout,
		hostIP:            hostIP,
	}, nil
}

// cleanupStaleStickySessions cleans up any stale sticky session records in the hash map.
func (proxier *Proxier) cleanupStaleStickySessions() {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()
	servicePortNameMap := make(map[ServicePortName]bool)
	for name := range proxier.serviceMap {
		servicePortName := ServicePortName{
			NamespacedName: types.NamespacedName{
				Namespace: name.Namespace,
				Name:      name.Name,
			},
			Port: name.Port,
		}
		if !servicePortNameMap[servicePortName] {
			// ensure cleanup sticky sessions only gets called once per serviceportname
			servicePortNameMap[servicePortName] = true
			proxier.loadBalancer.CleanupStaleStickySessions(servicePortName)
		}
	}
}

// OnServiceAdd is called whenever creation of new service object
// is observed.
func (proxier *Proxier) OnServiceAdd(service *localnetv1.Service) {
	fmt.Println("ADD SERVICE", service)
	_ = proxier.mergeService(service)
}

// OnServiceUpdate is called whenever modification of an existing
// service object is observed.
func (proxier *Proxier) OnServiceUpdate(oldService, service *localnetv1.Service) {
	fmt.Println("UPDATE SERVICE", oldService, service)
	existingPortPortals := proxier.mergeService(service)
	proxier.unmergeService(oldService, existingPortPortals)
}

// OnServiceDelete is called whenever deletion of an existing service
// object is observed.
func (proxier *Proxier) OnServiceDelete(service *localnetv1.Service) {
	fmt.Println("DELETE SERVICE", service)
	proxier.unmergeService(service, map[ServicePortPortalName]bool{})
}

// OnEndpointsAdd is called whenever creation of new endpoints object
// is observed.
func (proxier *Proxier) OnEndpointsAdd(endpoint *localnetv1.Endpoint, service *localnetv1.Service) {
	fmt.Println("ADD ENDPOINT", endpoint)
	proxier.loadBalancer.OnEndpointsAdd(endpoint, service)
}

// OnEndpointsUpdate is called whenever modification of an existing
// endpoints object is observed.
func (proxier *Proxier) OnEndpointsUpdate(oldEndpoints, endpoints *localnetv1.Endpoint, service *localnetv1.Service) {
	fmt.Println("UPDATE ENDPOINT", oldEndpoints, endpoints)
	proxier.loadBalancer.OnEndpointsUpdate(oldEndpoints, endpoints, service)
}

// OnEndpointsDelete is called whenever deletion of an existing endpoints
// object is observed.
func (proxier *Proxier) OnEndpointsDelete(endpoint *localnetv1.Endpoint, service *localnetv1.Service) {
	fmt.Println("DELETE ENDPOINT", endpoint)
	proxier.loadBalancer.OnEndpointsDelete(endpoint, service)
}

// OnEndpointsSynced is called once all the initial event handlers were
// called and the state is fully propagated to local cache.
func (proxier *Proxier) OnEndpointsSynced() {
	proxier.loadBalancer.OnEndpointsSynced()
}

func (proxier *Proxier) CreateNamespacedName(service *localnetv1.Service) types.NamespacedName {
	return types.NamespacedName{Namespace: service.Namespace, Name: service.Name}
}

func (proxier *Proxier) CreateNamespacedNameString(serviceName, namespaceName string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespaceName, Name: serviceName}
}

func (proxier *Proxier) mergeService(service *localnetv1.Service) map[ServicePortPortalName]bool {
	if service == nil {
		return nil
	}
	svcName := proxier.CreateNamespacedName(service)
	//if !helper.IsServiceIPSet(service) { // todo: refactor fix
	//	klog.V(3).InfoS("Skipping service due to clusterIP", "svcName", svcName, "ip", service.Spec.ClusterIP)
	//	return nil
	//}

	existingPortPortals := make(map[ServicePortPortalName]bool)
	for i := range service.Ports {
		servicePort := *service.Ports[i]
		// create a slice of all the source IPs to use for service port portals
		listenIPPortMap := getListenIPPortMap(service, servicePort.Port, servicePort.NodePort)
		protocol := servicePort.Protocol

		for listenIP, listenPort := range listenIPPortMap {
			servicePortPortalName := ServicePortPortalName{
				NamespacedName: svcName,
				Port:           servicePort.Name,
				PortalIPName:   listenIP,
			}
			existingPortPortals[servicePortPortalName] = true
			//fmt.Println(proxier.serviceMap, "serviceMap")
			info, exists := proxier.GetServiceInfo(servicePortPortalName)
			if exists && sameConfig(info, listenPort) { // todo: upgrade later on update
				// Nothing changed.
				continue
			}
			//if exists { // todo: fix on update
			//	klog.V(4).InfoS("Something changed for service: stopping it", "servicePortPortalName", servicePortPortalName.String())
			//	if err := proxier.closeServicePortPortal(servicePortPortalName, info); err != nil {
			//		klog.ErrorS(err, "Failed to close service port portal", "servicePortPortalName", servicePortPortalName.String())
			//	}
			//}
			fmt.Println("Adding new service", "servicePortPortalName", servicePortPortalName.String(), "addr", net.JoinHostPort(listenIP, strconv.Itoa(int(listenPort))), "protocol", protocol)
			info, err := proxier.addServicePortPortal(
				servicePortPortalName,
				protocol,
				listenIP,
				listenPort,
				proxier.udpIdleTimeout,
			)
			if err != nil {
				klog.ErrorS(err, "Failed to start proxy", "servicePortPortalName", servicePortPortalName.String())
				continue
			}
			//info.sessionAffinityType = service.Spec.SessionAffinity
			fmt.Println("record serviceInfo", "info", info)
		}
		if len(listenIPPortMap) > 0 {
			//only one loadbalancer per service port portal
			servicePortName := ServicePortName{
				NamespacedName: types.NamespacedName{
					Namespace: service.Namespace,
					Name:      service.Name,
				},
				Port: servicePort.Name,
			}
			timeoutSeconds := 0
			//
			//if service.Spec.SessionAffinity == v1.ServiceAffinityClientIP {
			//	timeoutSeconds = int(*service.Spec.SessionAffinityConfig.ClientIP.TimeoutSeconds)
			//}
			proxier.loadBalancer.NewService(servicePortName, v1.ServiceAffinityNone, timeoutSeconds)
		}
	}

	return existingPortPortals
}

func (proxier *Proxier) unmergeService(service *localnetv1.Service, existingPortPortals map[ServicePortPortalName]bool) {
	if service == nil {
		return
	}
	svcName := proxier.CreateNamespacedName(service)
	fmt.Println(svcName, existingPortPortals)
	//if !helper.IsServiceIPSet(service) {
	//	klog.V(3).InfoS("Skipping service due to clusterIP", "svcName", svcName, "ip", service.Spec.ClusterIP)
	//	return
	//}

	servicePortNameMap := make(map[ServicePortName]bool)
	for name := range existingPortPortals {
		servicePortName := ServicePortName{
			NamespacedName: proxier.CreateNamespacedName(service),
			Port:           name.Port,
		}
		servicePortNameMap[servicePortName] = true
	}
	//
	for i := range service.Ports {
		servicePort := *service.Ports[i]
		serviceName := ServicePortName{NamespacedName: svcName, Port: servicePort.Name}
		// create a slice of all the source IPs to use for service port portals
		listenIPPortMap := getListenIPPortMap(service, servicePort.Port, servicePort.NodePort)

		for listenIP := range listenIPPortMap {
			servicePortPortalName := ServicePortPortalName{
				NamespacedName: svcName,
				Port:           servicePort.Name,
				PortalIPName:   listenIP,
			}
			if existingPortPortals[servicePortPortalName] {
				continue
			}

			klog.V(1).InfoS("Stopping service", "servicePortPortalName", servicePortPortalName.String())
			info, exists := proxier.GetServiceInfo(servicePortPortalName)
			if !exists {
				klog.ErrorS(nil, "Service is being removed but doesn't exist", "servicePortPortalName", servicePortPortalName.String())
				continue
			}

			if err := proxier.closeServicePortPortal(servicePortPortalName, info); err != nil {
				klog.ErrorS(err, "Failed to close service port portal", "servicePortPortalName", servicePortPortalName)
			}
		}

		// Only delete load balancer if all listen ips per name/port show inactive.
		if !servicePortNameMap[serviceName] {
			proxier.loadBalancer.DeleteService(serviceName)
		}
	}
}

func sameConfig(info *serviceInfo, listenPort int32) bool {
	return info.portal.port == listenPort
}

// addServicePortPortal starts listening for a new service, returning the serviceInfo.
// The timeout only applies to UDP connections, for now.
func (proxier *Proxier) addServicePortPortal(servicePortPortalName ServicePortPortalName, protocol localnetv1.Protocol, listenIP string, port int32, timeout time.Duration) (*serviceInfo, error) {
	var serviceIP net.IP
	if listenIP != allAvailableInterfaces {
		if serviceIP = netutils.ParseIPSloppy(listenIP); serviceIP == nil {
			return nil, fmt.Errorf("could not parse ip '%q'", listenIP)
		}

		fmt.Println(listenIP, serviceIP, servicePortPortalName, "netsh address")
		// add the IP address.  Node port binds to all interfaces. // todo: on windows
		args := proxier.netshIPv4AddressAddArgs(serviceIP)
		if existed, err := proxier.netsh.EnsureIPAddress(args, serviceIP); err != nil {
			return nil, err
		} else if !existed {
			fmt.Println("Added ip address to fowarder interface for service", "servicePortPortalName", servicePortPortalName.String(), "addr", net.JoinHostPort(listenIP, strconv.Itoa(int(port))), "protocol", protocol)
		}
	}

	//// add the listener, proxy
	sock, err := newProxySocket(protocol, serviceIP, int(port))
	if err != nil {
		return nil, err
	}
	si := &serviceInfo{
		isAliveAtomic: 1,
		portal: portal{
			ip:         listenIP,
			port:       port,
			isExternal: false,
		},
		protocol: protocol,
		socket:   sock,
		timeout:  timeout,
		//activeClients:       newClientCache(),
		sessionAffinityType: v1.ServiceAffinityNone, // default
	}
	proxier.setServiceInfo(servicePortPortalName, si)
	fmt.Println("Proxying for service", "servicePortPortalName", servicePortPortalName.String(), "addr", net.JoinHostPort(listenIP, strconv.Itoa(int(port))), "protocol", protocol)
	go func(service ServicePortPortalName, proxier *Proxier) {
		defer runtime.HandleCrash()
		atomic.AddInt32(&proxier.numProxyLoops, 1)
		sock.ProxyLoop(service, si, proxier)
		atomic.AddInt32(&proxier.numProxyLoops, -1)
	}(servicePortPortalName, proxier)

	return si, nil
}

func (proxier *Proxier) closeServicePortPortal(servicePortPortalName ServicePortPortalName, info *serviceInfo) error {
	// turn off the proxy
	if err := proxier.stopProxy(servicePortPortalName, info); err != nil {
		return err
	}

	// close the PortalProxy by deleting the service IP address
	if info.portal.ip != allAvailableInterfaces {
		serviceIP := netutils.ParseIPSloppy(info.portal.ip)
		args := proxier.netshIPv4AddressDeleteArgs(serviceIP)
		if err := proxier.netsh.DeleteIPAddress(args); err != nil {
			return err
		}
	}
	return nil
}

// This assumes proxier.mu is not locked.
func (proxier *Proxier) stopProxy(service ServicePortPortalName, info *serviceInfo) error {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()
	return proxier.stopProxyInternal(service, info)
}

// This assumes proxier.mu is locked.
func (proxier *Proxier) stopProxyInternal(service ServicePortPortalName, info *serviceInfo) error {
	delete(proxier.serviceMap, service)
	info.setAlive(false)
	err := info.socket.Close()
	return err
}

func (proxier *Proxier) GetServiceInfo(service ServicePortPortalName) (*serviceInfo, bool) {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()
	info, ok := proxier.serviceMap[service]
	return info, ok
}

func (proxier *Proxier) setServiceInfo(service ServicePortPortalName, info *serviceInfo) {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()
	proxier.serviceMap[service] = info
}

func (proxier *Proxier) netshIPv4AddressAddArgs(destIP net.IP) []string {
	intName := proxier.netsh.GetInterfaceToAddIP()
	args := []string{
		"interface", "ipv4", "add", "address",
		"name=" + intName,
		"address=" + destIP.String(),
	}

	return args
}

func (proxier *Proxier) netshIPv4AddressDeleteArgs(destIP net.IP) []string {
	intName := proxier.netsh.GetInterfaceToAddIP()
	args := []string{
		"interface", "ipv4", "delete", "address",
		"name=" + intName,
		"address=" + destIP.String(),
	}

	return args
}