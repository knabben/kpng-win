package winkernel

import (
	"fmt"
	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/knabben/kpng-win/pkg/proxy"
	"k8s.io/klog/v2"
	"os"
	"strings"
	"time"
)

const NETWORK_TYPE_OVERLAY = "overlay"

type hnsNetworkInfo struct {
	name          string
	id            string
	networkType   string
	remoteSubnets []*remoteSubnetInfo
}

type remoteSubnetInfo struct {
	destinationPrefix string
	isolationID       uint16
	providerAddress   string
	drMacAddress      string
}

type loadBalancerFlags struct {
	isILB           bool
	isDSR           bool
	localRoutedVIP  bool
	useMUX          bool
	preserveDIP     bool
	sessionAffinity bool
	isIPv6          bool
}

type loadBalancerInfo struct {
	hnsID string
}


type externalIPInfo struct {
	ip    string
	hnsID string
}

type loadBalancerIngressInfo struct {
	ip    string
	hnsID string
}

func newHostNetworkService() (HostNetworkService, hcn.SupportedFeatures) {
	var hns HostNetworkService
	hns = hnsV1{}
	supportedFeatures := hcn.GetSupportedFeatures()
	if supportedFeatures.Api.V2 {
		hns = hnsV2{}
	}

	return hns, supportedFeatures
}

func getNetworkName(hnsNetworkName string) (string, error) {
	if len(hnsNetworkName) == 0 {
		fmt.Println("Flag --network-name not set, checking environment variable")
		hnsNetworkName = os.Getenv("KUBE_NETWORK")
		if len(hnsNetworkName) == 0 {
			return "", fmt.Errorf("Environment variable KUBE_NETWORK and network-flag not initialized")
		}
	}
	return hnsNetworkName, nil
}

func deleteAllHnsLoadBalancerPolicy() {
	plists, err := hcsshim.HNSListPolicyListRequest()
	if err != nil {
		return
	}
	for _, plist := range plists {
		fmt.Println("Remove policy", "policies", plist)
		_, err = plist.Delete()
		if err != nil {
			klog.ErrorS(err, "Failed to delete policy list")
		}
	}
}

func getNetworkInfo(hns HostNetworkService, hnsNetworkName string) (*hnsNetworkInfo, error) {
	hnsNetworkInfo, err := hns.getNetworkByName(hnsNetworkName)
	for err != nil {
		klog.ErrorS(err, "Unable to find HNS Network specified, please check network name and CNI deployment", "hnsNetworkName", hnsNetworkName)
		time.Sleep(1 * time.Second)
		hnsNetworkInfo, err = hns.getNetworkByName(hnsNetworkName)
	}
	return hnsNetworkInfo, err
}

func isOverlay(hnsNetworkInfo *hnsNetworkInfo) bool {
	return strings.EqualFold(hnsNetworkInfo.networkType, NETWORK_TYPE_OVERLAY)
}

// internal struct for string service information
type serviceInfo struct {
	*proxy.BaseServiceInfo
	targetPort             int
	externalIPs            []*externalIPInfo
	loadBalancerIngressIPs []*loadBalancerIngressInfo
	hnsID                  string
	nodePorthnsID          string
	policyApplied          bool
	remoteEndpoint         *endpointsInfo
	hns                    HostNetworkService
	preserveDIP            bool
	localTrafficDSR        bool
}

func (svcInfo *serviceInfo) deleteAllHnsLoadBalancerPolicy() {
	// Remove the Hns Policy corresponding to this service
	hns := svcInfo.hns
	hns.deleteLoadBalancer(svcInfo.hnsID)
	svcInfo.hnsID = ""

	hns.deleteLoadBalancer(svcInfo.nodePorthnsID)
	svcInfo.nodePorthnsID = ""

	for _, externalIP := range svcInfo.externalIPs {
		hns.deleteLoadBalancer(externalIP.hnsID)
		externalIP.hnsID = ""
	}
	for _, lbIngressIP := range svcInfo.loadBalancerIngressIPs {
		hns.deleteLoadBalancer(lbIngressIP.hnsID)
		lbIngressIP.hnsID = ""
	}
}

func (svcInfo *serviceInfo) cleanupAllPolicies(endpoints []proxy.Endpoint) {
	klog.V(3).InfoS("Service cleanup", "serviceInfo", svcInfo)
	// Skip the svcInfo.policyApplied check to remove all the policies
	svcInfo.deleteAllHnsLoadBalancerPolicy()
	// Cleanup Endpoints references
	for _, ep := range endpoints {
		epInfo, ok := ep.(*endpointsInfo)
		if ok {
			epInfo.Cleanup()
		}
	}
	if svcInfo.remoteEndpoint != nil {
		svcInfo.remoteEndpoint.Cleanup()
	}

	svcInfo.policyApplied = false
}
