package ingress

import (
	"context"
	"time"

	networkingv1alpha1 "github.com/jmprusi/kcp-ingress/pkg/client/clientset/versioned"
	glbclient "github.com/jmprusi/kcp-ingress/pkg/client/clientset/versioned/typed/globalloadbalancer/v1alpha1"
	networkingv1alpha1Informers "github.com/jmprusi/kcp-ingress/pkg/client/informers/externalversions"
	v1 "k8s.io/api/networking/v1"
	networkingv1client "k8s.io/client-go/kubernetes/typed/networking/v1"
	networkingv1lister "k8s.io/client-go/listers/networking/v1"

	clusterclient "github.com/kcp-dev/kcp/pkg/client/clientset/versioned"
	"github.com/kcp-dev/kcp/pkg/client/informers/externalversions"
	clusterlisters "github.com/kcp-dev/kcp/pkg/client/listers/cluster/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
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
func NewController(cfg *rest.Config) *Controller {
	networkingClient := networkingv1client.NewForConfigOrDie(cfg)
	glbClient := glbclient.NewForConfigOrDie(cfg)
	kubeClient := kubernetes.NewForConfigOrDie(cfg)
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	stopCh := make(chan struct{}) // TODO: hook this up to SIGTERM/SIGINT

	csif := externalversions.NewSharedInformerFactoryWithOptions(clusterclient.NewForConfigOrDie(cfg), resyncPeriod)
	c := &Controller{
		queue:            queue,
		networkingClient: networkingClient,
		glbClient:        glbClient,
		clusterLister:    csif.Cluster().V1alpha1().Clusters().Lister(),
		kubeClient:       kubeClient,
		stopCh:           stopCh,
	}
	csif.WaitForCacheSync(stopCh)
	csif.Start(stopCh)

	sif := informers.NewSharedInformerFactoryWithOptions(kubeClient, resyncPeriod)
	//TODO(jmprusi): Handle deletion
	sif.Networking().V1().Ingresses().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueue(obj) },
		UpdateFunc: func(_, obj interface{}) { c.enqueue(obj) },
	})
	sif.WaitForCacheSync(stopCh)
	sif.Start(stopCh)

	//TODO(jmprusi): fix this with an EnqueueForOwner style of handler.
	glbif := networkingv1alpha1Informers.NewSharedInformerFactory(networkingv1alpha1.NewForConfigOrDie(cfg), resyncPeriod)
	glbif.Networking().V1alpha1().GlobalLoadBalancers().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueue(obj) },
		UpdateFunc: func(_, obj interface{}) { c.enqueue(obj) },
	})
	glbif.WaitForCacheSync(stopCh)
	glbif.Start(stopCh)

	c.indexer = sif.Networking().V1().Ingresses().Informer().GetIndexer()
	c.lister = sif.Networking().V1().Ingresses().Lister()

	return c
}

type Controller struct {
	queue            workqueue.RateLimitingInterface
	networkingClient *networkingv1client.NetworkingV1Client
	glbClient        *glbclient.NetworkingV1alpha1Client
	clusterLister    clusterlisters.ClusterLister
	kubeClient       kubernetes.Interface
	stopCh           chan struct{}
	indexer          cache.Indexer
	lister           networkingv1lister.IngressLister
}

func (c *Controller) enqueueForOwner(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.queue.AddRateLimited(key)
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
	ctx := context.TODO()

	// TODO(jmprusi): Handle deletion
	if !exists {
		klog.Infof("Object with key %q was deleted", key)
		return nil
	}

	current := obj.(*v1.Ingress)
	previous := current.DeepCopy()

	if err := c.reconcile(ctx, current); err != nil {
		return err
	}

	// If the object being reconciled changed as a result, update it.
	if !equality.Semantic.DeepEqual(previous, current) {
		_, uerr := c.networkingClient.Ingresses(current.Namespace).Update(ctx, current, metav1.UpdateOptions{})
		return uerr
	}

	return err
}
