package dns

import (
	"context"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	v1 "github.com/kuadrant/kcp-ingress/pkg/apis/kuadrant/v1"
	kuadrantv1 "github.com/kuadrant/kcp-ingress/pkg/client/kuadrant/clientset/versioned"
	"github.com/kuadrant/kcp-ingress/pkg/client/kuadrant/informers/externalversions"
	kuadrantv1lister "github.com/kuadrant/kcp-ingress/pkg/client/kuadrant/listers/kuadrant/v1"
	"github.com/kuadrant/kcp-ingress/pkg/dns"
	awsdns "github.com/kuadrant/kcp-ingress/pkg/dns/aws"
)

const resyncPeriod = 10 * time.Hour

// NewController returns a new Controller which reconciles DNSRecord.
func NewController(config *ControllerConfig) (*Controller, error) {
	client := kuadrantv1.NewForConfigOrDie(config.Cfg)
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	stopCh := make(chan struct{}) // TODO: hook this up to SIGTERM/SIGINT

	c := &Controller{
		queue:  queue,
		client: client,
		stopCh: stopCh,
	}

	dnsProvider, err := createDNSProvider(*config.DNSProvider)
	if err != nil {
		return nil, err
	}
	c.dnsProvider = dnsProvider

	var dnsZones []v1.DNSZone
	zoneID, zoneIDSet := os.LookupEnv("AWS_DNS_PUBLIC_ZONE_ID")
	if zoneIDSet {
		dnsZone := &v1.DNSZone{
			ID: zoneID,
		}
		dnsZones = append(dnsZones, *dnsZone)
		klog.Infof("Using aws dns zone id : %s", zoneID)
	} else {
		klog.Warningf("No aws dns zone id set(AWS_DNS_PUBLIC_ZONE_ID). No DNS records will be created!!")
	}
	c.dnsZones = dnsZones

	sif := externalversions.NewSharedInformerFactoryWithOptions(c.client, resyncPeriod)

	// Watch for events related to DNSRecords
	sif.Kuadrant().V1().DNSRecords().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueue(obj) },
		UpdateFunc: func(_, obj interface{}) { c.enqueue(obj) },
		DeleteFunc: func(obj interface{}) { c.enqueue(obj) },
	})

	sif.Start(stopCh)
	for inf, sync := range sif.WaitForCacheSync(stopCh) {
		if !sync {
			klog.Fatalf("Failed to sync %s", inf)
		}
	}

	c.indexer = sif.Kuadrant().V1().DNSRecords().Informer().GetIndexer()
	c.lister = sif.Kuadrant().V1().DNSRecords().Lister()

	return c, nil
}

type ControllerConfig struct {
	Cfg         *rest.Config
	DNSProvider *string
}

type Controller struct {
	queue       workqueue.RateLimitingInterface
	client      kuadrantv1.Interface
	stopCh      chan struct{}
	indexer     cache.Indexer
	lister      kuadrantv1lister.DNSRecordLister
	dnsProvider dns.Provider
	dnsZones    []v1.DNSZone
}

func (c *Controller) enqueue(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.queue.AddRateLimited(key)
}

func (c *Controller) Start(numThreads int) {
	defer c.queue.ShutDown()
	for i := 0; i < numThreads; i++ {
		go wait.Until(c.startWorker, time.Second, c.stopCh)
	}
	klog.Infof("Starting dns workers")
	<-c.stopCh
	klog.Infof("Stopping dns workers")
}

func (c *Controller) startWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) processNextWorkItem() bool {
	// Wait until there is a new item in the working queue
	k, quit := c.queue.Get()
	if quit {
		return false
	}
	key := k.(string)

	// No matter what, tell the queue we're done with this key, to unblock
	// other workers.
	defer c.queue.Done(key)

	err := c.process(key)
	c.handleErr(err, key)
	return true
}

func (c *Controller) handleErr(err error, key string) {
	// Reconcile worked, nothing else to do for this workqueue item.
	if err == nil {
		c.queue.Forget(key)
		return
	}

	// Re-enqueue up to 5 times.
	num := c.queue.NumRequeues(key)
	if num < 5 {
		klog.Errorf("Error reconciling key %q, retrying... (#%d): %v", key, num, err)
		c.queue.AddRateLimited(key)
		return
	}

	// Give up and report error elsewhere.
	c.queue.Forget(key)
	runtime.HandleError(err)
	klog.Infof("Dropping key %q after failed retries: %v", key, err)
}

func (c *Controller) process(key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		klog.Infof("Object with key %q was deleted", key)
		return nil
	}

	current := obj.(*v1.DNSRecord)

	previous := current.DeepCopy()

	ctx := context.TODO()
	if err := c.reconcile(ctx, current); err != nil {
		return err
	}

	// If the object being reconciled changed as a result, update it.
	if !equality.Semantic.DeepEqual(previous, current) {
		_, uerr := c.client.KuadrantV1().DNSRecords(current.Namespace).Update(ctx, current, metav1.UpdateOptions{})
		return uerr
	}

	return err
}

func createDNSProvider(dnsProviderName string) (dns.Provider, error) {
	var dnsProvider dns.Provider
	var dnsError error
	switch dnsProviderName {
	case "aws":
		klog.Infof("Using aws dns provider")
		dnsProvider, dnsError = newAWSDNSProvider()
	default:
		klog.Infof("Using fake dns provider")
		dnsProvider = &dns.FakeProvider{}
	}
	return dnsProvider, dnsError
}

func newAWSDNSProvider() (dns.Provider, error) {
	var dnsProvider dns.Provider
	provider, err := awsdns.NewProvider(awsdns.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS DNS manager: %v", err)
	}
	dnsProvider = provider

	return dnsProvider, nil
}
