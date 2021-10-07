package ingress

import (
	"context"
	"time"

	"github.com/jmprusi/kcp-ingress/pkg/envoy"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	networkingv1client "k8s.io/client-go/kubernetes/typed/networking/v1"
	networkingv1lister "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
	envoyserver "knative.dev/net-kourier/pkg/envoy/server"
)

const resyncPeriod = 10 * time.Hour

// NewController returns a new Controller which splits new Ingress objects
// into N virtual Ingresses labeled for each Cluster that exists at the time
// the Ingress is created.
func NewController(config *ControllerConfig) *Controller {
	client := networkingv1client.NewForConfigOrDie(config.Cfg)
	kubeClient := kubernetes.NewForConfigOrDie(config.Cfg)
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	stopCh := make(chan struct{}) // TODO: hook this up to SIGTERM/SIGINT

	c := &Controller{
		queue:      queue,
		client:     client,
		kubeClient: kubeClient,
		stopCh:     stopCh,
		domain:     config.Domain,
		tracker:    *NewTracker(),
	}

	if config.EnvoyXDS != nil {
		c.envoyXDS = config.EnvoyXDS
		c.cache = envoy.NewCache(envoy.NewTranslator(config.EnvoyListenPort))

		go func() {
			err := c.envoyXDS.RunManagementServer()
			if err != nil {
				panic(err)
			}
		}()
	}

	sif := informers.NewSharedInformerFactoryWithOptions(kubeClient, resyncPeriod)
	sif.Networking().V1().Ingresses().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueue(obj) },
		UpdateFunc: func(_, obj interface{}) { c.enqueue(obj) },
		DeleteFunc: func(obj interface{}) { c.enqueue(obj) },
	})

	sif.Core().V1().Services().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(_, obj interface{}) { c.ingressesFromService(obj) },
		DeleteFunc: func(obj interface{}) { c.ingressesFromService(obj) },
	})

	sif.WaitForCacheSync(stopCh)
	sif.Start(stopCh)

	c.indexer = sif.Networking().V1().Ingresses().Informer().GetIndexer()
	c.lister = sif.Networking().V1().Ingresses().Lister()

	return c
}

type ControllerConfig struct {
	Cfg             *rest.Config
	EnvoyXDS        *envoyserver.XdsServer
	Domain          *string
	EnvoyListenPort *uint
}

type Controller struct {
	queue           workqueue.RateLimitingInterface
	client          *networkingv1client.NetworkingV1Client
	kubeClient      kubernetes.Interface
	stopCh          chan struct{}
	indexer         cache.Indexer
	lister          networkingv1lister.IngressLister
	envoyXDS        *envoyserver.XdsServer
	envoyListenPort *uint
	cache           *envoy.Cache
	domain          *string
	tracker         Tracker
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
	klog.Infof("Starting workers")
	<-c.stopCh
	klog.Infof("Stopping workers")
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
		// If Envoy is enabled, delete the Ingress from the config cache.
		if c.envoyXDS != nil {
			// if EnvoyXDS is enabled, delete the Ingress from the cache and set the new snaphost.
			c.cache.DeleteIngress(key)
			c.envoyXDS.SetSnapshot(envoy.NodeID, c.cache.ToEnvoySnapshot())
		}
		// The ingress has been deleted, so we remove any ingress to service tracking.
		c.tracker.deleteIngress(key)
		return nil
	}
	current := obj.(*networkingv1.Ingress)

	previous := current.DeepCopy()

	ctx := context.TODO()
	if err := c.reconcile(ctx, current); err != nil {
		return err
	}

	// If the object being reconciled changed as a result, update it.
	if !equality.Semantic.DeepEqual(previous, current) {
		_, uerr := c.client.Ingresses(current.Namespace).Update(ctx, current, metav1.UpdateOptions{})
		return uerr
	}

	return err
}

// ingressesFromService enqueues all the related ingresses for a given service.
func (c *Controller) ingressesFromService(obj interface{}) {
	ingresses, ok := c.tracker.getIngress(obj.(*v1.Service))
	if ok {
		for _, ingress := range ingresses {
			klog.Infof("tracked service %q triggered Ingress %q reconciliation", obj.(*v1.Service).Name, ingress.Name)
			c.enqueue(ingress.DeepCopy())
		}
	} else {
		klog.Info("Ignoring non-tracked service: ", obj.(*v1.Service).Name)
	}
}
