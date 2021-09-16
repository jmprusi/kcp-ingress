package cloudflarelb

import (
	"context"
	"time"

	v1alpha12 "github.com/jmprusi/kcp-ingress/pkg/apis/globalloadbalancer/v1alpha1"

	"github.com/jmprusi/kcp-ingress/pkg/client/listers/globalloadbalancer/v1alpha1"

	networkingv1alpha1Informers "github.com/jmprusi/kcp-ingress/pkg/client/informers/externalversions"

	networkingv1alpha1 "github.com/jmprusi/kcp-ingress/pkg/client/clientset/versioned"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

const resyncPeriod = 10 * time.Hour

// NewController returns a new Controller which splits new Ingress objects
// into N virtual Ingresses labeled for each Cluster that exists at the time
// the Ingress is created.
func NewController(cfg *rest.Config, CloudflareConfig *CloudflareConfig) *Controller {
	networkingClient := networkingv1alpha1.NewForConfigOrDie(cfg)
	kubeClient := kubernetes.NewForConfigOrDie(cfg)
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	stopCh := make(chan struct{}) // TODO: hook this up to SIGTERM/SIGINT

	csif := networkingv1alpha1Informers.NewSharedInformerFactoryWithOptions(networkingv1alpha1.NewForConfigOrDie(cfg), resyncPeriod)

	c := &Controller{
		queue:            queue,
		networkingClient: networkingClient,
		kubeClient:       kubeClient,
		stopCh:           stopCh,
		cloudflareConfig: CloudflareConfig,
	}
	csif.WaitForCacheSync(stopCh)
	csif.Start(stopCh)

	sif := networkingv1alpha1Informers.NewSharedInformerFactoryWithOptions(networkingv1alpha1.NewForConfigOrDie(cfg), resyncPeriod)
	sif.Networking().V1alpha1().GlobalLoadBalancers().Informer().AddEventHandler(cache.
		ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.enqueue(obj)
		},
		UpdateFunc: func(_, obj interface{}) { c.enqueue(obj) },
	})
	sif.WaitForCacheSync(stopCh)
	sif.Start(stopCh)

	c.indexer = sif.Networking().V1alpha1().GlobalLoadBalancers().Informer().GetIndexer()
	c.lister = sif.Networking().V1alpha1().GlobalLoadBalancers().Lister()

	return c
}

type Controller struct {
	queue            workqueue.RateLimitingInterface
	networkingClient *networkingv1alpha1.Clientset
	kubeClient       kubernetes.Interface
	stopCh           chan struct{}
	indexer          cache.Indexer
	ingressQueue     workqueue.RateLimitingInterface
	lister           v1alpha1.GlobalLoadBalancerLister
	cloudflareConfig *CloudflareConfig
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
		klog.Errorf("Error getting key %s from indexer %s", key, err)
		return err
	}

	if !exists {
		klog.Infof("Object with key %q was deleted", key)
		return nil
	}
	ctx := context.TODO()

	current := obj.(*v1alpha12.GlobalLoadBalancer)
	previous := current.DeepCopy()

	if err := c.reconcile(ctx, current); err != nil {
		return err
	}

	// If the object being reconciled changed as a result, update it.
	if !equality.Semantic.DeepEqual(previous, current) {
		_, err := c.networkingClient.NetworkingV1alpha1().GlobalLoadBalancers(current.Namespace).Update(ctx, current, metav1.UpdateOptions{})
		return err
	}

	return err
}
