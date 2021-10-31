package userspace

import (
	"fmt"
	"io"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	"net"
	"sigs.k8s.io/kpng/api/localnetv1"
	"sigs.k8s.io/kpng/client"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tcpProxySocket implements proxySocket.  Close() is implemented by net.Listener.  When Close() is called,
// no new connections are allowed but existing connections are left untouched.
type tcpProxySocket struct {
	net.Listener
	port int
}

// udpProxySocket implements proxySocket.  Close() is implemented by net.UDPConn.  When Close() is called,
// no new connections are allowed and existing connections are broken.
// TODO: We could lame-duck this ourselves, if it becomes important.
type udpProxySocket struct {
	*net.UDPConn
	port int
}

func logTimeout(err error) bool {
	if e, ok := err.(net.Error); ok {
		if e.Timeout() {
			klog.V(3).InfoS("connection to endpoint closed due to inactivity")
			return true
		}
	}
	return false
}

func (udp *udpProxySocket) ListenPort() int {
	return udp.port
}

func (udp *udpProxySocket) Addr() net.Addr {
	return udp.LocalAddr()
}

func (udp *udpProxySocket) ProxyLoop(service ServicePortPortalName, myInfo *serviceInfo, proxier *Proxier) {
	var buffer [4096]byte // 4KiB should be enough for most whole-packets

	for {
		if !myInfo.isAlive() {
			// The service port was closed or replaced.
			break
		}

		// Block until data arrives.
		// TODO: Accumulate a histogram of n or something, to fine tune the buffer size.
		n, cliAddr, err := udp.ReadFrom(buffer[0:])
		if err != nil {
			if e, ok := err.(net.Error); ok {
				if e.Temporary() {
					klog.V(1).ErrorS(err, "ReadFrom had a temporary failure")
					continue
				}
			}
			klog.ErrorS(err, "ReadFrom failed, exiting ProxyLoop")
			break
		}

		// If this is a client we know already, reuse the connection and goroutine.
		svrConn, err := udp.getBackendConn(myInfo.activeClients, cliAddr, proxier, service, myInfo.timeout)
		if err != nil {
			continue
		}
		// TODO: It would be nice to let the goroutine handle this write, but we don't
		// really want to copy the buffer.  We could do a pool of buffers or something.
		_, err = svrConn.Write(buffer[0:n])
		if err != nil {
			if !logTimeout(err) {
				klog.ErrorS(err, "Write failed")
				// TODO: Maybe tear down the goroutine for this client/server pair?
			}
			continue
		}
		err = svrConn.SetDeadline(time.Now().Add(myInfo.timeout))
		if err != nil {
			klog.ErrorS(err, "SetDeadline failed")
			continue
		}
	}
}

func (udp *udpProxySocket) getBackendConn(activeClients *clientCache, cliAddr net.Addr, proxier *Proxier, service ServicePortPortalName, timeout time.Duration) (net.Conn, error) {
	activeClients.mu.Lock()
	defer activeClients.mu.Unlock()

	svrConn, found := activeClients.clients[cliAddr.String()]
	if !found {
		// TODO: This could spin up a new goroutine to make the outbound connection,
		// and keep accepting inbound traffic.
		klog.V(3).InfoS("New UDP connection from client", "address", cliAddr)
		var err error
		svrConn, err = tryConnect(service, cliAddr, "udp", proxier)
		if err != nil {
			return nil, err
		}
		if err = svrConn.SetDeadline(time.Now().Add(timeout)); err != nil {
			klog.ErrorS(err, "SetDeadline failed")
			return nil, err
		}
		activeClients.clients[cliAddr.String()] = svrConn
		go func(cliAddr net.Addr, svrConn net.Conn, activeClients *clientCache, service ServicePortPortalName, timeout time.Duration) {
			defer runtime.HandleCrash()
			udp.proxyClient(cliAddr, svrConn, activeClients, service, timeout)
		}(cliAddr, svrConn, activeClients, service, timeout)
	}
	return svrConn, nil
}

// This function is expected to be called as a goroutine.
// TODO: Track and log bytes copied, like TCP
func (udp *udpProxySocket) proxyClient(cliAddr net.Addr, svrConn net.Conn, activeClients *clientCache, service ServicePortPortalName, timeout time.Duration) {
	defer svrConn.Close()
	var buffer [4096]byte
	for {
		n, err := svrConn.Read(buffer[0:])
		if err != nil {
			if !logTimeout(err) {
				klog.ErrorS(err, "Read failed")
			}
			break
		}

		err = svrConn.SetDeadline(time.Now().Add(timeout))
		if err != nil {
			klog.ErrorS(err, "SetDeadline failed")
			break
		}
		_, err = udp.WriteTo(buffer[0:n], cliAddr)
		if err != nil {
			if !logTimeout(err) {
				klog.ErrorS(err, "WriteTo failed")
			}
			break
		}
	}
	activeClients.mu.Lock()
	delete(activeClients.clients, cliAddr.String())
	activeClients.mu.Unlock()
}

func (tcp *tcpProxySocket) ListenPort() int {
	return tcp.port
}

func isTooManyFDsError(err error) bool {
	return strings.Contains(err.Error(), "too many open files")
}

func isClosedError(err error) bool {
	// A brief discussion about handling closed error here:
	// https://code.google.com/p/go/issues/detail?id=4373#c14
	// TODO: maybe create a stoppable TCP listener that returns a StoppedError
	return strings.HasSuffix(err.Error(), "use of closed network connection")
}

// How long we wait for a connection to a backend in seconds
var endpointDialTimeout = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second, 2 * time.Second}

func tryConnect(service ServicePortPortalName, srcAddr net.Addr, protocol string, proxier *Proxier) (out net.Conn, err error) {
	sessionAffinityReset := false
	for _, dialTimeout := range endpointDialTimeout {
		servicePortName := ServicePortName{
			NamespacedName: types.NamespacedName{
				Namespace: service.Namespace,
				Name:      service.Name,
			},
			Port: service.Port,
		}
		endpoint, err := proxier.loadBalancer.NextEndpoint(servicePortName, srcAddr, sessionAffinityReset)
		if err != nil {
			klog.ErrorS(err, "Couldn't find an endpoint for service", "service", klog.KRef(service.Namespace, service.Name))
			return nil, err
		}
		klog.V(3).InfoS("Mapped service to endpoint", "service", klog.KRef(service.Namespace, service.Name), "endpoint", endpoint)
		// TODO: This could spin up a new goroutine to make the outbound connection,
		// and keep accepting inbound traffic.
		outConn, err := net.DialTimeout(protocol, endpoint, dialTimeout)
		if err != nil {
			if isTooManyFDsError(err) {
				panic(err)
			}
			klog.ErrorS(err, "Dial failed")
			sessionAffinityReset = true
			continue
		}
		return outConn, nil
	}
	return nil, fmt.Errorf("failed to connect to an endpoint")
}

func (tcp *tcpProxySocket) ProxyLoop(service ServicePortPortalName, myInfo *serviceInfo, proxier *Proxier) {
	for {
		if !myInfo.isAlive() {
			// The service port was closed or replaced.
			return
		}
		// Block until a connection is made.
		inConn, err := tcp.Accept()
		if err != nil {
			if isTooManyFDsError(err) {
				panic("Accept failed: " + err.Error())
			}

			if isClosedError(err) {
				return
			}
			if !myInfo.isAlive() {
				// Then the service port was just closed so the accept failure is to be expected.
				return
			}
			klog.ErrorS(err, "Accept failed")
			continue
		}
		klog.V(3).InfoS("Accepted TCP connection from remote", "remoteAddress", inConn.RemoteAddr(), "localAddress", inConn.LocalAddr())
		outConn, err := tryConnect(service, inConn.(*net.TCPConn).RemoteAddr(), "tcp", proxier)
		if err != nil {
			klog.ErrorS(err, "Failed to connect to balancer")
			inConn.Close()
			continue
		}
		// Spin up an async copy loop.
		go proxyTCP(inConn.(*net.TCPConn), outConn.(*net.TCPConn))
	}
}

// proxyTCP proxies data bi-directionally between in and out.
func proxyTCP(in, out *net.TCPConn) {
	var wg sync.WaitGroup
	wg.Add(2)
	klog.V(4).InfoS("Creating proxy between remote and local addresses",
		"inRemoteAddress", in.RemoteAddr(), "inLocalAddress", in.LocalAddr(), "outLocalAddress", out.LocalAddr(), "outRemoteAddress", out.RemoteAddr())
	go copyBytes("from backend", in, out, &wg)
	go copyBytes("to backend", out, in, &wg)
	wg.Wait()
}

func copyBytes(direction string, dest, src *net.TCPConn, wg *sync.WaitGroup) {
	defer wg.Done()
	klog.V(4).InfoS("Copying remote address bytes", "direction", direction, "sourceRemoteAddress", src.RemoteAddr(), "destinationRemoteAddress", dest.RemoteAddr())
	n, err := io.Copy(dest, src)
	if err != nil {
		if !isClosedError(err) {
			klog.ErrorS(err, "I/O error occurred")
		}
	}
	klog.V(4).InfoS("Copied remote address bytes", "bytes", n, "direction", direction, "sourceRemoteAddress", src.RemoteAddr(), "destinationRemoteAddress", dest.RemoteAddr())
	dest.Close()
	src.Close()
}

const allAvailableInterfaces string = ""

// getListenIPPortMap returns a slice of all listen IPs for a service.
func getListenIPPortMap(service *localnetv1.Service, listenPort, nodePort int32) map[string]int32 {
	serviceIP := service.IPs
	listenIPPortMap := make(map[string]int32)
	listenIPPortMap[serviceIP.ClusterIPs.V4[0]] = listenPort

	for _, ip := range serviceIP.ExternalIPs.V4 {
		listenIPPortMap[ip] = listenPort
	}

	// Loadbalancer ?
	//for _, ingress := range service.Status.LoadBalancer.Ingress {
	//	listenIPPortMap[ingress.IP] = listenPort
	//}

	if nodePort != 0 {
		listenIPPortMap[allAvailableInterfaces] = nodePort
	}

	return listenIPPortMap
}

func newProxySocket(protocol localnetv1.Protocol, ip net.IP, port int) (proxySocket, error) {
	host := ""
	if ip != nil {
		host = ip.String()
	}

	switch protocol {
	case localnetv1.Protocol_TCP:
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return nil, err
		}
		return &tcpProxySocket{Listener: listener, port: port}, nil
	case localnetv1.Protocol_UDP:
		addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return nil, err
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			return nil, err
		}
		return &udpProxySocket{UDPConn: conn, port: port}, nil
		//case "SCTP":
		//	return nil, fmt.Errorf("SCTP is not supported for user space proxy")
	}
	return nil, fmt.Errorf("unknown protocol %q", protocol)
}

// BuildPortsToEndpointsMap builds a map of portname -> all ip:ports for that
// portname. Explode Endpoints.Subsets[*] into this structure.
func buildPortsToEndpointsMap(endpoints *client.ServiceEndpoints) map[string][]string {
	portsToEndpoints := map[string][]string{}

	for _, se := range endpoints.Endpoints {
		for _, ip := range se.IPs.V4 {
			for _, port := range endpoints.Service.Ports {
				portsToEndpoints[port.Name] = append(
					portsToEndpoints[port.Name], net.JoinHostPort(
						ip,
						strconv.Itoa(int(port.Port)),
					),
				)
			}
		}
	}
	return portsToEndpoints
}
