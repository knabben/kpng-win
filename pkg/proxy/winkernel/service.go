package winkernel

import (
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/knabben/kpng-win/pkg/proxy"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	netutils "k8s.io/utils/net"
)

// returns a new proxy.ServicePort which abstracts a serviceInfo
func (proxier *Proxier) newServiceInfo(port *v1.ServicePort, service *v1.Service, baseInfo *proxy.BaseServiceInfo) proxy.ServicePort {
	info := &serviceInfo{BaseServiceInfo: baseInfo}
	preserveDIP := service.Annotations["preserve-destination"] == "true"
	localTrafficDSR := service.Spec.ExternalTrafficPolicy == v1.ServiceExternalTrafficPolicyTypeLocal
	err := hcn.DSRSupported()
	if err != nil {
		preserveDIP = false
		localTrafficDSR = false
	}
	// targetPort is zero if it is specified as a name in port.TargetPort.
	// Its real value would be got later from endpoints.
	targetPort := 0
	if port.TargetPort.Type == intstr.Int {
		targetPort = port.TargetPort.IntValue()
	}

	info.preserveDIP = preserveDIP
	info.targetPort = targetPort
	info.hns = proxier.hns
	info.localTrafficDSR = localTrafficDSR

	for _, eip := range service.Spec.ExternalIPs {
		info.externalIPs = append(info.externalIPs, &externalIPInfo{ip: eip})
	}

	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if netutils.ParseIPSloppy(ingress.IP) != nil {
			info.loadBalancerIngressIPs = append(info.loadBalancerIngressIPs, &loadBalancerIngressInfo{ip: ingress.IP})
		}
	}
	return info
}


func (proxier *Proxier) onServiceMapChange(svcPortName *proxy.ServicePortName) {

	svc, exists := proxier.serviceMap[*svcPortName]

	if exists {
		svcInfo, ok := svc.(*serviceInfo)

		if !ok {
			klog.ErrorS(nil, "Failed to cast serviceInfo", "servicePortName", svcPortName)
			return
		}

		klog.V(3).InfoS("Updating existing service port", "servicePortName", svcPortName, "clusterIP", svcInfo.ClusterIP(), "port", svcInfo.Port(), "protocol", svcInfo.Protocol())
		svcInfo.cleanupAllPolicies(proxier.endpointsMap[*svcPortName])
	}
}

func (proxier *Proxier) serviceMapChange(previous, current proxy.ServiceMap) {
	for svcPortName := range current {
		proxier.onServiceMapChange(&svcPortName)
	}

	for svcPortName := range previous {
		if _, ok := current[svcPortName]; ok {
			continue
		}
		proxier.onServiceMapChange(&svcPortName)
	}
}
