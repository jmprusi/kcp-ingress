module github.com/kuadrant/kcp-ingress

go 1.16

require (
	github.com/aws/aws-sdk-go v1.38.49
	github.com/envoyproxy/go-control-plane v0.10.1
	github.com/envoyproxy/protoc-gen-validate v0.6.1 // indirect
	github.com/go-logr/logr v1.1.0 // indirect
	github.com/google/go-cmp v0.5.6
	github.com/google/uuid v1.3.0
	github.com/imdario/mergo v0.3.9 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/rs/xid v1.3.0
	google.golang.org/protobuf v1.27.1
	k8s.io/api v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v0.21.4
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.20.0 // indirect
	k8s.io/utils v0.0.0-20210707171843-4b05e18ac7d9
	knative.dev/net-kourier v0.28.0
)

replace (
	k8s.io/api => github.com/kcp-dev/kubernetes/staging/src/k8s.io/api v0.0.0-20210921141446-281309ebaa64
	k8s.io/apiextensions-apiserver => github.com/kcp-dev/kubernetes/staging/src/k8s.io/apiextensions-apiserver v0.0.0-20210921141446-281309ebaa64
	k8s.io/apimachinery => github.com/kcp-dev/kubernetes/staging/src/k8s.io/apimachinery v0.0.0-20210921141446-281309ebaa64
	k8s.io/apiserver => github.com/kcp-dev/kubernetes/staging/src/k8s.io/apiserver v0.0.0-20210921141446-281309ebaa64
	k8s.io/client-go => github.com/kcp-dev/kubernetes/staging/src/k8s.io/client-go v0.0.0-20210921141446-281309ebaa64
	k8s.io/code-generator => github.com/kcp-dev/kubernetes/staging/src/k8s.io/code-generator v0.0.0-20210921141446-281309ebaa64
	k8s.io/component-base => github.com/kcp-dev/kubernetes/staging/src/k8s.io/component-base v0.0.0-20210921141446-281309ebaa64
)
