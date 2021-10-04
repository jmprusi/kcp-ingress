package ingress

import (
	"sync"

	networkingv1 "k8s.io/api/networking/v1"

	v1 "k8s.io/api/core/v1"
)

type Tracker struct {
	mu                sync.Mutex
	trackedServices   map[string][]networkingv1.Ingress
	ingressToServices map[string]map[string]struct{}
}

func NewTracker() *Tracker {
	return &Tracker{
		trackedServices:   make(map[string][]networkingv1.Ingress),
		ingressToServices: make(map[string]map[string]struct{}),
	}
}

func (t *Tracker) getIngress(service *v1.Service) ([]networkingv1.Ingress, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.trackedServices[serviceToKey(service)]
	return s, ok
}

func (t *Tracker) add(ingress *networkingv1.Ingress, s *v1.Service) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, ti := range t.trackedServices[serviceToKey(s)] {
		if ingressToKey(&ti) == ingressToKey(ingress) {
			return
		}
	}
	t.trackedServices[serviceToKey(s)] = append(t.trackedServices[serviceToKey(s)], *ingress)
	t.ingressToServices[ingressToKey(ingress)] = make(map[string]struct{})
	t.ingressToServices[ingressToKey(ingress)][serviceToKey(s)] = struct{}{}
}

func (t *Tracker) deleteIngress(ingressKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for serviceKey := range t.ingressToServices[ingressKey] {
		for i, ing := range t.trackedServices[serviceKey] {
			if ingressToKey(&ing) == ingressKey {
				t.trackedServices[serviceKey] = append(t.trackedServices[serviceKey][:i], t.trackedServices[serviceKey][i+1:]...)
				break
			}
		}
		// This service is no longer being tracked by another ingress, so let's delete it.
		if len(t.trackedServices[serviceKey]) == 0 {
			delete(t.trackedServices, serviceKey)
		}
	}
	delete(t.ingressToServices, ingressKey)
}

func serviceToKey(service *v1.Service) string {
	return service.Namespace + "/" + service.ClusterName + "#$#" + service.Name
}

func ingressToKey(ingress *networkingv1.Ingress) string {
	return ingress.Namespace + "/" + ingress.ClusterName + "#$#" + ingress.Name
}
