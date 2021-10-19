module github.com/jmprusi/kcp-ingress

go 1.16

require (
	github.com/cncf/xds/go v0.0.0-20210805033703-aa0b78936158 // indirect
	github.com/envoyproxy/go-control-plane v0.9.9
	github.com/envoyproxy/protoc-gen-validate v0.6.1 // indirect
	github.com/go-logr/logr v1.1.0 // indirect
	github.com/google/uuid v1.3.0
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kcp-dev/kcp v0.0.0-20211007140613-f1145a6ec820
	github.com/patrickmn/go-cache v2.1.0+incompatible
	golang.org/x/net v0.0.0-20210917221730-978cfadd31cf // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/protobuf v1.27.1
	k8s.io/api v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v0.21.4
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.20.0 // indirect
	knative.dev/net-kourier v0.25.1-0.20210920060635-5e8ac6c0beaf
)

replace (
	k8s.io/api => ../kubernetes/staging/src/k8s.io/api
	k8s.io/apiextensions-apiserver => ../kubernetes/staging/src/k8s.io/apiextensions-apiserver
	k8s.io/apimachinery => ../kubernetes/staging/src/k8s.io/apimachinery
	k8s.io/apiserver => ../kubernetes/staging/src/k8s.io/apiserver
	k8s.io/cli-runtime => ../kubernetes/staging/src/k8s.io/cli-runtime
	k8s.io/client-go => ../kubernetes/staging/src/k8s.io/client-go
	k8s.io/cloud-provider => ../kubernetes/staging/src/k8s.io/cloud-provider
	k8s.io/cluster-bootstrap => ../kubernetes/staging/src/k8s.io/cluster-bootstrap
	k8s.io/code-generator => ../kubernetes/staging/src/k8s.io/code-generator
	k8s.io/component-base => ../kubernetes/staging/src/k8s.io/component-base
	k8s.io/component-helpers => ../kubernetes/staging/src/k8s.io/component-helpers
	k8s.io/controller-manager => ../kubernetes/staging/src/k8s.io/controller-manager
	k8s.io/cri-api => ../kubernetes/staging/src/k8s.io/cri-api
	k8s.io/csi-translation-lib => ../kubernetes/staging/src/k8s.io/csi-translation-lib
	k8s.io/kube-aggregator => ../kubernetes/staging/src/k8s.io/kube-aggregator
	k8s.io/kube-controller-manager => ../kubernetes/staging/src/k8s.io/kube-controller-manager
	k8s.io/kube-proxy => ../kubernetes/staging/src/k8s.io/kube-proxy
	k8s.io/kube-scheduler => ../kubernetes/staging/src/k8s.io/kube-scheduler
	k8s.io/kubectl => ../kubernetes/staging/src/k8s.io/kubectl
	k8s.io/kubelet => ../kubernetes/staging/src/k8s.io/kubelet
	k8s.io/legacy-cloud-providers => ../kubernetes/staging/src/k8s.io/legacy-cloud-providers
	k8s.io/metrics => ../kubernetes/staging/src/k8s.io/metrics
	k8s.io/mount-utils => ../kubernetes/staging/src/k8s.io/mount-utils
	k8s.io/pod-security-admission => ../kubernetes/staging/src/k8s.io/pod-security-admission
	k8s.io/sample-apiserver => ../kubernetes/staging/src/k8s.io/sample-apiserver
)

replace k8s.io/kubernetes => ../kubernetes

replace github.com/kcp-dev/kcp => ../kcp
