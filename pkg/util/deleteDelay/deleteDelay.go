package deleteDelay

import (
	"github.com/kuadrant/kcp-glbc/pkg/util/metadata"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"strconv"
	"time"
)

const (
	DeleteAtFinalizer  = "kcp.dev/awaiting-delete-time"
	DeleteAtAnnotation = "kcp.dev/delete-at"
	TTLDefault         = 120 * time.Second
)

func SetDefaultDeleteAt(obj metav1.Object, queue workqueue.RateLimitingInterface) (metav1.Object, error) {
	at := time.Now()
	at = at.Add(TTLDefault)
	return SetDeleteAt(obj, at, queue)
}

func SetDeleteAt(obj metav1.Object, at time.Time, queue workqueue.RateLimitingInterface) (metav1.Object, error) {
	//object already queued for deletion
	if metadata.HasFinalizer(obj, DeleteAtFinalizer) {
		return obj, nil
	}
	metadata.AddFinalizer(obj, DeleteAtFinalizer)
	metadata.AddAnnotation(obj, DeleteAtAnnotation, strconv.FormatInt(at.Unix(), 10))
	klog.Infof("setting delete at annotation for %v to %v, current time: %v", obj.GetName(), strconv.FormatInt(at.Unix(), 10), time.Now().Unix())

	err := Requeue(obj, queue)
	if err != nil {
		return nil, err
	}

	return obj, nil
}

func Requeue(obj metav1.Object, queue workqueue.RateLimitingInterface) error {
	after, err := TimeToLiveInSeconds(obj)
	if err != nil {
		return err
	}

	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return err
	}
	klog.Infof("requeueing %v after %v seconds", key, after)
	queue.AddAfter(key, after)

	return nil
}

func CleanForDeletion(obj metav1.Object) metav1.Object {
	metadata.RemoveFinalizer(obj, DeleteAtFinalizer)
	metadata.RemoveAnnotation(obj, DeleteAtAnnotation)
	return obj
}

func TimeToLiveInSeconds(obj metav1.Object) (time.Duration, error) {
	if deleteTimeString, ok := obj.GetAnnotations()[DeleteAtAnnotation]; ok {
		deleteTimeInt, err := strconv.ParseInt(deleteTimeString, 10, 64)
		if err != nil {
			return time.Duration(0), err
		}
		deleteTime := metav1.Unix(deleteTimeInt, 0)
		now := time.Now()

		return deleteTime.Sub(now), nil
	}

	//no annotation, so return as zero
	return time.Duration(0), nil
}

func CanDelete(obj metav1.Object) bool {
	duration, err := TimeToLiveInSeconds(obj)
	if err != nil {
		klog.Warningf("error determining if %v can be deleted: %v", obj.GetName(), err.Error())
		return false
	}

	return duration <= 0
}
