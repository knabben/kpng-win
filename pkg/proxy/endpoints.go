package proxy

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"net"
	"strconv"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"

	 utilproxy "github.com/knabben/kpng-win/pkg/proxy/util"

	"sync"
	"time"
)

// EndpointsMap maps a service name to a list of all its Endpoints.
type EndpointsMap map[ServicePortName][]Endpoint

// This handler is invoked by the apply function on every change. This function should not modify the
// EndpointsMap's but just use the changes for any Proxier specific cleanup.
type processEndpointsMapChangeFunc func(oldEndpointsMap, newEndpointsMap EndpointsMap)

// BaseEndpointInfo contains base information that defines an endpoint.
// This could be used directly by proxier while processing endpoints,
// or can be used for constructing a more specific EndpointInfo struct
// defined by the proxier if needed.
type BaseEndpointInfo struct {
	Endpoint string // TODO: should be an endpointString type
	// IsLocal indicates whether the endpoint is running in same host as kube-proxy.
	IsLocal bool

	// ZoneHints represent the zone hints for the endpoint. This is based on
	// endpoint.hints.forZones[*].name in the EndpointSlice API.
	ZoneHints sets.String
	// Ready indicates whether this endpoint is ready and NOT terminating.
	// For pods, this is true if a pod has a ready status and a nil deletion timestamp.
	// This is only set when watching EndpointSlices. If using Endpoints, this is always
	// true since only ready endpoints are read from Endpoints.
	// TODO: Ready can be inferred from Serving and Terminating below when enabled by default.
	Ready bool
	// Serving indiciates whether this endpoint is ready regardless of its terminating state.
	// For pods this is true if it has a ready status regardless of its deletion timestamp.
	// This is only set when watching EndpointSlices. If using Endpoints, this is always
	// true since only ready endpoints are read from Endpoints.
	Serving bool
	// Terminating indicates whether this endpoint is terminating.
	// For pods this is true if it has a non-nil deletion timestamp.
	// This is only set when watching EndpointSlices. If using Endpoints, this is always
	// false since terminating endpoints are always excluded from Endpoints.
	Terminating bool

	// NodeName is the name of the node this endpoint belongs to
	NodeName string
	// Zone is the name of the zone this endpoint belongs to
	Zone string
}

var _ Endpoint = &BaseEndpointInfo{}


// String is part of proxy.Endpoint interface.
func (info *BaseEndpointInfo) String() string {
	return info.Endpoint
}

// GetIsLocal is part of proxy.Endpoint interface.
func (info *BaseEndpointInfo) GetIsLocal() bool {
	return info.IsLocal
}

// IsReady returns true if an endpoint is ready and not terminating.
func (info *BaseEndpointInfo) IsReady() bool {
	return info.Ready
}

// IsServing returns true if an endpoint is ready, regardless of if the
// endpoint is terminating.
func (info *BaseEndpointInfo) IsServing() bool {
	return info.Serving
}

// IsTerminating retruns true if an endpoint is terminating. For pods,
// that is any pod with a deletion timestamp.
func (info *BaseEndpointInfo) IsTerminating() bool {
	return info.Terminating
}

// GetZoneHints returns the zone hint for the endpoint.
func (info *BaseEndpointInfo) GetZoneHints() sets.String {
	return info.ZoneHints
}

// IP returns just the IP part of the endpoint, it's a part of proxy.Endpoint interface.
func (info *BaseEndpointInfo) IP() string {
	return utilproxy.IPPart(info.Endpoint)
}

// Port returns just the Port part of the endpoint.
func (info *BaseEndpointInfo) Port() (int, error) {
	return utilproxy.PortPart(info.Endpoint)
}

// Equal is part of proxy.Endpoint interface.
func (info *BaseEndpointInfo) Equal(other Endpoint) bool {
	return info.String() == other.String() && info.GetIsLocal() == other.GetIsLocal()
}

// GetNodeName returns the NodeName for this endpoint.
func (info *BaseEndpointInfo) GetNodeName() string {
	return info.NodeName
}

// GetZone returns the Zone for this endpoint.
func (info *BaseEndpointInfo) GetZone() string {
	return info.Zone
}

func newBaseEndpointInfo(IP, nodeName, zone string, port int, isLocal bool,
	ready, serving, terminating bool, zoneHints sets.String) *BaseEndpointInfo {
	return &BaseEndpointInfo{
		Endpoint:    net.JoinHostPort(IP, strconv.Itoa(port)),
		IsLocal:     isLocal,
		Ready:       ready,
		Serving:     serving,
		Terminating: terminating,
		ZoneHints:   zoneHints,
		NodeName:    nodeName,
		Zone:        zone,
	}
}

type makeEndpointFunc func(info *BaseEndpointInfo) Endpoint

// endpointsChange contains all changes to endpoints that happened since proxy
// rules were synced.  For a single object, changes are accumulated, i.e.
// previous is state from before applying the changes, current is state after
// applying the changes.
type endpointsChange struct {
	previous EndpointsMap
	current  EndpointsMap
}

// EndpointChangeTracker carries state about uncommitted changes to an arbitrary number of
// Endpoints, keyed by their namespace and name.
type EndpointChangeTracker struct {
	// lock protects items.
	lock sync.Mutex
	// hostname is the host where kube-proxy is running.
	hostname string
	// items maps a service to is endpointsChange.
	items map[types.NamespacedName]*endpointsChange
	// makeEndpointInfo allows proxier to inject customized information when processing endpoint.
	makeEndpointInfo          makeEndpointFunc
	processEndpointsMapChange processEndpointsMapChangeFunc
	// endpointSliceCache holds a simplified version of endpoint slices.
	endpointSliceCache *EndpointSliceCache
	// ipfamily identify the ip family on which the tracker is operating on
	ipFamily v1.IPFamily
	recorder events.EventRecorder
	// Map from the Endpoints namespaced-name to the times of the triggers that caused the endpoints
	// object to change. Used to calculate the network-programming-latency.
	lastChangeTriggerTimes map[types.NamespacedName][]time.Time
	// record the time when the endpointChangeTracker was created so we can ignore the endpoints
	// that were generated before, because we can't estimate the network-programming-latency on those.
	// This is specially problematic on restarts, because we process all the endpoints that may have been
	// created hours or days before.
	trackerStartTime time.Time
}

// NewEndpointChangeTracker initializes an EndpointsChangeMap
func NewEndpointChangeTracker(hostname string, makeEndpointInfo makeEndpointFunc, ipFamily v1.IPFamily, processEndpointsMapChange processEndpointsMapChangeFunc) *EndpointChangeTracker {
	return &EndpointChangeTracker{
		hostname:                  hostname,
		items:                     make(map[types.NamespacedName]*endpointsChange),
		makeEndpointInfo:          makeEndpointInfo,
		ipFamily:                  ipFamily,
		//recorder:                  recorder,
		lastChangeTriggerTimes:    make(map[types.NamespacedName][]time.Time),
		trackerStartTime:          time.Now(),
		processEndpointsMapChange: processEndpointsMapChange,
		endpointSliceCache:        NewEndpointSliceCache(hostname, ipFamily, makeEndpointInfo),
	}
}