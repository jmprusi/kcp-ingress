package tls

import (
	"context"

	"github.com/kuadrant/kcp-glbc/pkg/cluster"
	v1 "k8s.io/api/core/v1"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	secretsFinalizer   = "kcp.dev/cascade-cleanup"
	tlsreadyAnnotation = "kuadrant.dev/tls.enabled"
)

// this controller watches the control cluster and mirrors cert secrets into the KCP cluster
func (c *Controller) reconcile(ctx context.Context, secret *v1.Secret) error {
	// create our context to avoid repeatedly pulling out annotations etc
	kcpCtx, err := cluster.NewKCPObjectMapper(secret)
	// may be a better way to filter these out TODO look at label selector in the controller
	if err != nil && cluster.IsNoContextErr(err) {
		// ignore this secret
		klog.Infof("ignoring control cluster secret as doesn't have kcp context annotations", secret.Name)
		return nil
	}
	if err != nil {
		return err
	}

	if secret.DeletionTimestamp != nil {
		klog.Infof("control cluster secret %s deleted removing mirrored secret from kcp", secret.Name)
		if err := c.ensureDelete(ctx, kcpCtx, secret); err != nil {
			return err
		}
		// remove finalizer from the control cluster secret so it can be cleaned up
		removeFinalizer(secret, secretsFinalizer)
		if _, err = c.glbcKubeClient.CoreV1().Secrets(secret.Namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil && !k8errors.IsNotFound(err) {
			return err
		}
		return nil
	}
	AddFinalizer(secret, secretsFinalizer)
	secret, err = c.glbcKubeClient.CoreV1().Secrets(secret.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	if err := c.ensureMirrored(ctx, kcpCtx, secret); err != nil {
		klog.Errorf("failed to mirror secret %s", err.Error())
		return err
	}

	return nil
}

func removeFinalizer(secret *v1.Secret, finalizer string) {
	for i, v := range secret.Finalizers {
		if v == finalizer {
			secret.Finalizers[i] = secret.Finalizers[len(secret.Finalizers)-1]
			secret.Finalizers = secret.Finalizers[:len(secret.Finalizers)-1]
			return
		}
	}
}

func AddFinalizer(secret *v1.Secret, finalizer string) {
	for _, v := range secret.Finalizers {
		if v == finalizer {
			return
		}
	}
	secret.Finalizers = append(secret.Finalizers, finalizer)
}

func (c *Controller) ensureDelete(ctx context.Context, kctx cluster.ObjectMapper, secret *v1.Secret) error {
	// delete the mirrored secret
	if err := c.kcpClient.Cluster(kctx.WorkSpace()).CoreV1().Secrets(kctx.NameSpace()).Delete(ctx, kctx.Name(), metav1.DeleteOptions{}); err != nil && !k8errors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Controller) ensureMirrored(ctx context.Context, kctx cluster.ObjectMapper, secret *v1.Secret) error {
	//create a mirrored secret

	klog.Infof("mirroring %s tls secret to workspace %s namespace %s and secret %s ", kctx.Name(), kctx.WorkSpace(), kctx.NameSpace(), kctx.Name())
	mirror := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kctx.Name(),
			Namespace: kctx.NameSpace(),
			Labels:    kctx.Labels(),
		},
		Data: secret.Data,
		Type: secret.Type,
	}
	secretClient := c.kcpClient.Cluster(kctx.WorkSpace()).CoreV1().Secrets(kctx.NameSpace())
	// using kcpClient here to target the kcp cluster
	_, err := secretClient.Create(ctx, mirror, metav1.CreateOptions{})
	if err != nil && !k8errors.IsAlreadyExists(err) {
		return err
	}
	if err != nil && k8errors.IsAlreadyExists(err) {
		s, err := secretClient.Get(ctx, mirror.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		mirror.ResourceVersion = s.ResourceVersion
		mirror.UID = s.UID
		if _, err := secretClient.Update(ctx, mirror, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	// find the ingress this secret is for and add an annotation to notify tls is ready and trigger reconcile
	ingressClient := c.kcpClient.Cluster(kctx.WorkSpace()).NetworkingV1().Ingresses(kctx.NameSpace())
	rootIngress, err := ingressClient.Get(ctx, kctx.OwnedBy(), metav1.GetOptions{})
	if err != nil {
		return err
	}
	if rootIngress.Annotations == nil {
		rootIngress.Annotations = map[string]string{}
	}
	if _, ok := rootIngress.Annotations[tlsreadyAnnotation]; !ok {
		rootIngress.Annotations[tlsreadyAnnotation] = "true"
		if _, err := ingressClient.Update(ctx, rootIngress, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}

	return nil
}
