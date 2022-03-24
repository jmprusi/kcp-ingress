package deployment

import (
	"context"
	service "github.com/kuadrant/kcp-glbc/pkg/reconciler/service"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const controllerName = "kcp-glbc-deployment"

// NewController returns a new Controller which reconciles DNSRecord.
func NewController(config *ControllerConfig) (*Controller, error) {
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	c := &Controller{
		queue:                 queue,
		coreClient:            config.DeploymentClient,
		sharedInformerFactory: config.SharedInformerFactory,
	}

	// Watch for events related to Deployments
	c.sharedInformerFactory.Apps().V1().Deployments().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueue(obj) },
		UpdateFunc: func(_, obj interface{}) { c.enqueue(obj) },
		DeleteFunc: func(obj interface{}) { c.enqueue(obj) },
	})

	// Watch for events related to Services
	c.sharedInformerFactory.Core().V1().Services().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.deploymentsFromService(obj) },
		UpdateFunc: func(_, obj interface{}) { c.deploymentsFromService(obj) },
		DeleteFunc: func(obj interface{}) { c.deploymentsFromService(obj) },
	})

	c.indexer = c.sharedInformerFactory.Apps().V1().Deployments().Informer().GetIndexer()
	c.deploymentLister = c.sharedInformerFactory.Apps().V1().Deployments().Lister()
	c.serviceLister = c.sharedInformerFactory.Core().V1().Services().Lister()

	return c, nil
}

type ControllerConfig struct {
	DeploymentClient      kubernetes.ClusterInterface
	SharedInformerFactory informers.SharedInformerFactory
}

type Controller struct {
	queue                 workqueue.RateLimitingInterface
	sharedInformerFactory informers.SharedInformerFactory
	coreClient            kubernetes.ClusterInterface
	indexer               cache.Indexer
	deploymentLister      appsv1listers.DeploymentLister
	serviceLister         corev1listers.ServiceLister
}

func (c *Controller) enqueue(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.queue.AddRateLimited(key)
}

func (c *Controller) Start(ctx context.Context, numThreads int) {
	defer runtime.HandleCrash()
	defer c.queue.ShutDown()

	klog.InfoS("Starting workers", "controller", controllerName)
	defer klog.InfoS("Stopping workers", "controller", controllerName)

	for i := 0; i < numThreads; i++ {
		go wait.UntilWithContext(ctx, c.startWorker, time.Second)
	}

	<-ctx.Done()
}

func (c *Controller) startWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	// Wait until there is a new item in the working queue
	k, quit := c.queue.Get()
	if quit {
		return false
	}
	key := k.(string)

	// No matter what, tell the queue we're done with this key, to unblock
	// other workers.
	defer c.queue.Done(key)

	err := c.process(ctx, key)
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

func (c *Controller) process(ctx context.Context, key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		klog.Infof("Object with key %q was deleted", key)
		return nil
	}

	current := obj.(*appsv1.Deployment)

	previous := current.DeepCopy()

	if err := c.reconcile(ctx, current); err != nil {
		return err
	}

	// If the object being reconciled changed as a result, update it.
	if !equality.Semantic.DeepEqual(previous, current) {
		_, err := c.coreClient.Cluster(current.ClusterName).AppsV1().Deployments(current.Namespace).Update(ctx, current, metav1.UpdateOptions{})
		return err
	}

	return err
}

func (c *Controller) deploymentsFromService(obj interface{}) {
	svc := obj.(*corev1.Service)

	if !service.IsRootService(svc) {
		return
	}

	deployments, err := c.getReferencedDeployments(svc)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	for _, deployment := range deployments {
		c.enqueue(deployment)
	}
}

func (c *Controller) getReferencedDeployments(service *corev1.Service) ([]*appsv1.Deployment, error) {
	deployments, err := c.deploymentLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	deployments = findDeploymentsBySelector(labels.SelectorFromSet(service.Spec.Selector), deployments)
	return deployments, nil
}

func findDeploymentsBySelector(selector labels.Selector, deployments []*appsv1.Deployment) []*appsv1.Deployment {
	retDeps := make([]*appsv1.Deployment, 0, len(deployments))

	for _, deployment := range deployments {
		deploymentTemplateLabels := labels.Set(deployment.Spec.Template.Labels)
		if selector.Matches(deploymentTemplateLabels) {
			retDeps = append(retDeps, deployment)
		}
	}

	return retDeps
}
