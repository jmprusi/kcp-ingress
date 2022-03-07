package cluster

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	LABEL_HCG_HOST           = "kuadrant.dev/hcg.host"
	ANNOTATION_HCG_WORKSPACE = "kuadrant.dev/hcg.workspace"
	ANNOTATION_HCG_NAMESPACE = "kuadrant.dev/hcg.namespace"
	LABEL_HCG_MANAGED        = "kuadrant.dev/hcg.managed"
	ANNOTATION_HCG_HOST      = "kuadrant.dev/host.generated"
	LABEL_OWNED_BY           = "kcp.dev/owned-by"
)

type context struct {
	workspace string
	nameSpace string
	name      string
	host      string
	ownedBy   string
}

type ObjectMapper interface {
	Name() string
	NameSpace() string
	WorkSpace() string
	Host() string
	Labels() map[string]string
	Annotations() map[string]string
	OwnedBy() string
}

var noContextErr = errors.New("object is missing needed context")

func IsNoContextErr(err error) bool {
	return errors.Is(err, noContextErr)
}

// NewKCPObjectMapper will return an object that can map a resource in the the control cluster to
// objects in KCP based on the annotations applied to it in the control cluster. It will fail if the annotations are missing
func NewKCPObjectMapper(ob runtime.Object) (ObjectMapper, error) {
	ac := meta.NewAccessor()
	annotations, err := ac.Annotations(ob)
	if err != nil {
		return nil, err
	}
	labels, err := ac.Labels(ob)
	if err != nil {
		return nil, err
	}
	name, err := ac.Name(ob)
	if err != nil {
		return nil, err
	}
	kcpContext := &context{
		name: name,
	}
	v, ok := annotations[ANNOTATION_HCG_WORKSPACE]
	if !ok || v == "" {
		return nil, fmt.Errorf("%w expected annotation %s to be present and not empty ", noContextErr, ANNOTATION_HCG_WORKSPACE)
	}
	kcpContext.workspace = v
	v, ok = annotations[ANNOTATION_HCG_NAMESPACE]
	if !ok || v == "" {
		return nil, fmt.Errorf("%w expected annotation %s to be present and not empty", noContextErr, ANNOTATION_HCG_NAMESPACE)
	}
	kcpContext.nameSpace = v
	v, ok = annotations[ANNOTATION_HCG_HOST]
	if !ok || v == "" {
		return nil, fmt.Errorf("%w expected annotation %s to be present and not empty", noContextErr, ANNOTATION_HCG_HOST)
	}
	kcpContext.host = v

	kcpContext.ownedBy = labels[LABEL_OWNED_BY]
	return kcpContext, nil
}

func (kc context) OwnedBy() string {
	return kc.ownedBy
}

func (kc *context) Name() string {
	return kc.name
}

func (kc *context) Labels() map[string]string {
	return map[string]string{
		LABEL_HCG_HOST:    kc.host,
		LABEL_HCG_MANAGED: "true",
		LABEL_OWNED_BY:    kc.ownedBy,
	}
}

func (kc *context) Host() string {
	return kc.host
}

func (kc *context) NameSpace() string {
	return kc.nameSpace
}

func (kc *context) WorkSpace() string {
	return kc.workspace
}

type controlContext struct {
	*context
}

// NewControlObjectMapper returns an object that can map from something in the KCP API
// to something in the control cluster. It provides a set of Labels and Annotations to apply to objects
// that will be created in the control cluster to enable them to be mapped back. It expexcts a Host annotation
func NewControlObjectMapper(obj runtime.Object) (ObjectMapper, error) {

	ctx := &context{}
	ac := meta.NewAccessor()
	annotations, err := ac.Annotations(obj)
	if err != nil {
		return nil, err
	}
	name, err := ac.Name(obj)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("expected object to have a name "))
	}
	ctx.name = name
	v, ok := annotations[ANNOTATION_HCG_HOST]
	if !ok || v == "" {
		return nil, fmt.Errorf("%w expected annotation %s to be present and not empty", noContextErr, ANNOTATION_HCG_HOST)
	}
	ctx.host = v
	workspace, err := ac.ClusterName(obj)
	if err != nil {
		return nil, err
	}
	ctx.workspace = workspace
	namespace, err := ac.Namespace(obj)
	if err != nil {
		return nil, err
	}
	ctx.nameSpace = namespace
	ctx.ownedBy = name // this is the object context
	return &controlContext{
		context: ctx,
	}, nil

}

func (cr *context) Annotations() map[string]string {
	return map[string]string{ANNOTATION_HCG_WORKSPACE: cr.workspace,
		ANNOTATION_HCG_NAMESPACE: cr.nameSpace, ANNOTATION_HCG_HOST: cr.host}
}

func (cr *controlContext) Name() string {
	return fmt.Sprintf("%s-%s-%s", cr.WorkSpace(), cr.NameSpace(), cr.name)
}

func (cr *controlContext) Host() string {
	return cr.host
}

func (cr *controlContext) Labels() map[string]string {
	return map[string]string{
		LABEL_HCG_HOST:    cr.host,
		LABEL_HCG_MANAGED: "true",
		LABEL_OWNED_BY:    cr.ownedBy,
	}
}
