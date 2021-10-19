package envoy

import (
	"sync"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	cachetypes "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	envoycachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/google/uuid"
	gocache "github.com/patrickmn/go-cache"
	networkingv1 "k8s.io/api/networking/v1"
)

const (
	defaultCleanupInterval = 1 * time.Minute
	NodeID                 = "kcp-ingress"
)

type Cache struct {
	mu         sync.Mutex
	ingresses  *gocache.Cache
	translator *translator
}

func NewCache(translator *translator) *Cache {
	return &Cache{
		mu:         sync.Mutex{},
		ingresses:  gocache.New(gocache.NoExpiration, defaultCleanupInterval),
		translator: translator,
	}
}

func (c *Cache) UpdateIngress(ingress networkingv1.Ingress) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ingresses.Delete(ingressToKey(ingress))
	c.ingresses.Add(ingressToKey(ingress), ingress, gocache.NoExpiration)
}

// TODO(jmprusi): Lacks location.
func (c *Cache) DeleteIngress(name, namespace string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ingresses.Delete(namespace + "#$#" + name)
}

func (c *Cache) ToEnvoySnapshot() cache.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	clustersResources := make([]cachetypes.Resource, 0)
	virtualhosts := make([]*envoyroutev3.VirtualHost, 0)

	for _, ingress := range c.ingresses.Items() {
		ingclusters, ingvhosts := c.translator.translateIngress(ingress.Object.(networkingv1.Ingress))
		clustersResources = append(clustersResources, ingclusters...)
		virtualhosts = append(virtualhosts, ingvhosts...)
	}

	routeConfig := c.translator.newRouteConfig("defaultroute", virtualhosts)
	hcm := c.translator.newHTTPConnectionManager(routeConfig.Name)
	listener, _ := c.translator.newHTTPListener(hcm)

	return envoycachev3.NewSnapshot(
		uuid.NewString(),
		make([]cachetypes.Resource, 0),
		clustersResources,
		[]cachetypes.Resource{routeConfig},
		[]cachetypes.Resource{listener},
		make([]cachetypes.Resource, 0),
		make([]cachetypes.Resource, 0),
	)
}

func ingressToKey(ingress networkingv1.Ingress) string {
	return ingress.Namespace + "#$#" + ingress.Name
}
