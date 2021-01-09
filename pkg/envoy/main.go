package envoy

import (
	"context"
	"log"
	"net"
	"time"

	api "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	listener "github.com/envoyproxy/go-control-plane/envoy/api/v2/listener"
	tcp_proxy "github.com/envoyproxy/go-control-plane/envoy/config/filter/network/tcp_proxy/v2"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v2"
	xds "github.com/envoyproxy/go-control-plane/pkg/server/v2"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/golang/protobuf/ptypes"
	any "github.com/golang/protobuf/ptypes/any"
	wrappers "github.com/golang/protobuf/ptypes/wrappers"
	"github.com/networkop/envoy-split-proxy/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

const prefix = "envoy-split-proxy"

var (
	defaultClusterName  = prefix + "-default"
	bypassClusterName   = prefix + "-bypass"
	defaultListenerName = prefix + "-listener"
)

// Envoy stores the XDS server configuration
type Envoy struct {
	cache  cache.SnapshotCache
	nodeID string
	lPort  int
}

// NewServer creates a new XDS server
func NewServer(grpcURL string, nodeID string, lPort int) (*Envoy, error) {

	snapshotCache := cache.NewSnapshotCache(false, cache.IDHash{}, nil)

	server := xds.NewServer(context.Background(), snapshotCache, nil)

	grpcServer := grpc.NewServer()
	lis, err := net.Listen("tcp", grpcURL)
	if err != nil {
		return nil, err
	}

	api.RegisterClusterDiscoveryServiceServer(grpcServer, server)
	api.RegisterListenerDiscoveryServiceServer(grpcServer, server)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("Failed to initialize grpc server: %s\n", err)
		}
	}()

	return &Envoy{
		cache:  snapshotCache,
		nodeID: nodeID,
		lPort:  lPort,
	}, nil
}

// Configure applies the desired state to the proxy
func (e *Envoy) Configure(in chan *config.Data) {

	for d := range in {
		logrus.Infof("Received new config: %+v", d)

		cluster := buildCluster(d.IP.String())
		listener := buildListener(d.URLs, e.lPort)
		snapshot := cache.NewSnapshot(time.Now().String(), nil, cluster, nil, listener, nil, nil)
		err := e.cache.SetSnapshot(e.nodeID, snapshot)
		if err != nil {
			logrus.Infof("Failed to update envoy config: %s", err)
		} else {
			d.Changed = false
		}

	}
}

func buildCluster(ip string) []types.Resource {
	defaultCluster := newEnvoyCluster(defaultClusterName)

	bypassCluster := newEnvoyCluster(bypassClusterName)
	bypassCluster.UpstreamBindConfig = &core.BindConfig{
		SourceAddress: &core.SocketAddress{
			Address: ip,
			PortSpecifier: &core.SocketAddress_PortValue{
				PortValue: uint32(0),
			},
		},
	}

	return []types.Resource{defaultCluster, bypassCluster}
}

func newEnvoyCluster(name string) *api.Cluster {
	logrus.Debugf("Creating Envoy cluster %s", name)
	return &api.Cluster{
		Name:                 name,
		ConnectTimeout:       ptypes.DurationProto(5 * time.Second),
		ClusterDiscoveryType: &api.Cluster_Type{Type: api.Cluster_ORIGINAL_DST},
		DnsLookupFamily:      api.Cluster_V4_ONLY,
		LbPolicy:             api.Cluster_CLUSTER_PROVIDED,
	}
}

func buildListener(urls []string, port int) []types.Resource {
	return []types.Resource{
		&api.Listener{
			Name: defaultListenerName,
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						Address: "0.0.0.0",
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: uint32(port),
						},
					},
				},
			},
			UseOriginalDst: &wrappers.BoolValue{
				Value: true,
			},
			FilterChains: []*listener.FilterChain{
				{
					FilterChainMatch: &listener.FilterChainMatch{
						ServerNames: urls,
					},
					Filters: []*listener.Filter{
						{
							Name: wellknown.TCPProxy,
							ConfigType: &listener.Filter_TypedConfig{
								TypedConfig: newClusterTypedConfig(bypassClusterName),
							},
						},
					},
				},
				{
					Filters: []*listener.Filter{
						{
							Name: wellknown.TCPProxy,
							ConfigType: &listener.Filter_TypedConfig{
								TypedConfig: newClusterTypedConfig(defaultClusterName),
							},
						},
					},
				},
			},
		},
	}
}

func newClusterTypedConfig(name string) *any.Any {
	logrus.Debugf("Building cluster config for %s", name)

	cluster := &tcp_proxy.TcpProxy{
		StatPrefix: prefix,
		ClusterSpecifier: &tcp_proxy.TcpProxy_Cluster{
			Cluster: name,
		},
	}

	config, err := ptypes.MarshalAny(cluster)
	if err != nil {
		logrus.Infof("Failed to build the listener config: %s", err)
	}
	return config
}
