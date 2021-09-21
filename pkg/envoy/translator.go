package envoy

import (
	"fmt"
	"strconv"
	"time"

	envoyclusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	envoycorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyendpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	envoylistenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoyfilterhcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	cachetypes "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	networkingv1 "k8s.io/api/networking/v1"
)

type translator struct {
	envoyListenPort *uint
}

func NewTranslator(envoyListenPort *uint) *translator {
	return &translator{
		envoyListenPort: envoyListenPort,
	}
}

func (t *translator) translateIngress(ingress networkingv1.Ingress) ([]cachetypes.Resource, []*envoyroutev3.VirtualHost) {

	// TODO(jmprusi): Hardcoded port, also, not TLS support. Review
	endpoints := make([]*envoyendpointv3.LbEndpoint, 0)
	for _, lb := range ingress.Status.LoadBalancer.Ingress {
		endpoint := &envoyendpointv3.LbEndpoint{}
		if lb.Hostname != "" {
			endpoint = t.newLBEndpoint(lb.Hostname, 80)
		} else if lb.IP != "" {
			endpoint = t.newLBEndpoint(lb.IP, 80)
		}
		endpoints = append(endpoints, endpoint)
	}

	//TODO(jmprusi): HTTP2 is set to false always, also allow for configuration of the timeout
	cluster := t.newCluster(ingressToKey(ingress), 2*time.Second, endpoints, envoyclusterv3.Cluster_STRICT_DNS)
	cluster.DnsLookupFamily = envoyclusterv3.Cluster_V4_ONLY

	virtualHosts := make([]*envoyroutev3.VirtualHost, 0)
	routes := make([]*envoyroutev3.Route, 0)
	domains := make([]string, 0)

	//TODO(jmprusi): We are ignoring the path type, we need to review this.
	for i, rule := range ingress.Spec.Rules {

		// TODO(jmprusi): If the host is empty we just ignore the rule, not ideal.
		if rule.HTTP.Paths == nil || rule.Host == "" {
			break
		}

		for _, path := range rule.HTTP.Paths {
			route := &envoyroutev3.Route{
				Name: ingress.Name + ingress.Namespace + strconv.Itoa(i),
				Match: &envoyroutev3.RouteMatch{
					PathSpecifier: &envoyroutev3.RouteMatch_Prefix{
						Prefix: path.Path,
					},
				},
				Action: &envoyroutev3.Route_Route{
					Route: &envoyroutev3.RouteAction{
						ClusterSpecifier: &envoyroutev3.RouteAction_Cluster{
							Cluster: ingressToKey(ingress),
						},
						Timeout: &durationpb.Duration{Seconds: 0},
						UpgradeConfigs: []*envoyroutev3.RouteAction_UpgradeConfig{{
							UpgradeType: "websocket",
							Enabled:     wrapperspb.Bool(true),
						}},
					},
				},
			}
			routes = append(routes, route)
		}
		domains = append(domains, rule.Host, rule.Host+":*")
	}

	vh := &envoyroutev3.VirtualHost{
		Name:    ingressToKey(ingress),
		Domains: domains,
		Routes:  routes,
	}

	virtualHosts = append(virtualHosts, vh)

	return []cachetypes.Resource{cluster}, virtualHosts
}

func (t *translator) newLBEndpoint(ip string, port uint32) *envoyendpointv3.LbEndpoint {
	return &envoyendpointv3.LbEndpoint{
		HostIdentifier: &envoyendpointv3.LbEndpoint_Endpoint{
			Endpoint: &envoyendpointv3.Endpoint{
				Address: &envoycorev3.Address{
					Address: &envoycorev3.Address_SocketAddress{
						SocketAddress: &envoycorev3.SocketAddress{
							Protocol: envoycorev3.SocketAddress_TCP,
							Address:  ip,
							PortSpecifier: &envoycorev3.SocketAddress_PortValue{
								PortValue: port,
							},
							Ipv4Compat: true,
						},
					},
				},
			},
		},
	}
}

func (t *translator) newCluster(
	name string,
	connectTimeout time.Duration,
	endpoints []*envoyendpointv3.LbEndpoint,
	discoveryType envoyclusterv3.Cluster_DiscoveryType) *envoyclusterv3.Cluster {

	return &envoyclusterv3.Cluster{
		Name: name,
		ClusterDiscoveryType: &envoyclusterv3.Cluster_Type{
			Type: discoveryType,
		},
		ConnectTimeout: durationpb.New(connectTimeout),
		LoadAssignment: &envoyendpointv3.ClusterLoadAssignment{
			ClusterName: name,
			Endpoints: []*envoyendpointv3.LocalityLbEndpoints{{
				LbEndpoints: endpoints,
			}},
		},
	}
}

func (t *translator) newRouteConfig(name string, virtualHosts []*envoyroutev3.VirtualHost) *envoyroutev3.RouteConfiguration {
	return &envoyroutev3.RouteConfiguration{
		Name:         name,
		VirtualHosts: virtualHosts,
		// Without this validation we can generate routes that point to non-existing clusters
		// That causes some "no_cluster" errors in Envoy and the "TestUpdate"
		// in the Knative serving test suite fails sometimes.
		// Ref: https://github.com/knative/serving/blob/f6da03e5dfed78593c4f239c3c7d67c5d7c55267/test/conformance/ingress/update_test.go#L37
		ValidateClusters: wrapperspb.Bool(true),
	}
}

func (t *translator) newHTTPConnectionManager(routeConfigName string) *envoyfilterhcmv3.HttpConnectionManager {
	filters := make([]*envoyfilterhcmv3.HttpFilter, 0, 1)

	// Append the Router filter at the end.
	filters = append(filters, &envoyfilterhcmv3.HttpFilter{
		Name: wellknown.Router,
	})

	return &envoyfilterhcmv3.HttpConnectionManager{
		CodecType:   envoyfilterhcmv3.HttpConnectionManager_AUTO,
		StatPrefix:  "ingress_http",
		HttpFilters: filters,
		RouteSpecifier: &envoyfilterhcmv3.HttpConnectionManager_Rds{
			Rds: &envoyfilterhcmv3.Rds{
				ConfigSource: &envoycorev3.ConfigSource{
					ResourceApiVersion: resource.DefaultAPIVersion,
					ConfigSourceSpecifier: &envoycorev3.ConfigSource_Ads{
						Ads: &envoycorev3.AggregatedConfigSource{},
					},
					InitialFetchTimeout: durationpb.New(10 * time.Second),
				},
				RouteConfigName: routeConfigName,
			},
		},
	}
}

func (t *translator) newHTTPListener(manager *envoyfilterhcmv3.HttpConnectionManager) (*envoylistenerv3.Listener, error) {
	managerAny, err := anypb.New(manager)
	if err != nil {
		return nil, err
	}

	filters := []*envoylistenerv3.Filter{{
		Name:       wellknown.HTTPConnectionManager,
		ConfigType: &envoylistenerv3.Filter_TypedConfig{TypedConfig: managerAny},
	}}

	return &envoylistenerv3.Listener{
		Name: fmt.Sprintf("listener_%d", *t.envoyListenPort),
		Address: &envoycorev3.Address{
			Address: &envoycorev3.Address_SocketAddress{
				SocketAddress: &envoycorev3.SocketAddress{
					Protocol: envoycorev3.SocketAddress_TCP,
					Address:  "0.0.0.0",
					PortSpecifier: &envoycorev3.SocketAddress_PortValue{
						PortValue: uint32(*t.envoyListenPort),
					},
				},
			},
		},
		FilterChains: []*envoylistenerv3.FilterChain{
			{Filters: filters},
		},
	}, nil
}
