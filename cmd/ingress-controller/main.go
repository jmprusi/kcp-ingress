package main

import (
	"flag"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	envoyserver "knative.dev/net-kourier/pkg/envoy/server"

	"github.com/kuadrant/kcp-ingress/pkg/reconciler/dns"
	"github.com/kuadrant/kcp-ingress/pkg/reconciler/ingress"
)

const numThreads = 2

var kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig")
var kubecontext = flag.String("context", "", "Context to use in the Kubeconfig file, instead of the current context")

var domain = flag.String("domain", "kcp-apps.127.0.0.1.nip.io", "The domain to use to expose ingresses")

var envoyEnableXDS = flag.Bool("envoyxds", false, "Start an Envoy control plane")
var envoyXDSPort = flag.Uint("envoyxds-port", 18000, "Envoy control plane port")
var envoyListenPort = flag.Uint("envoy-listener-port", 80, "Envoy default listener port")

func main() {
	flag.Parse()

	var overrides clientcmd.ConfigOverrides
	if *kubecontext != "" {
		overrides.CurrentContext = *kubecontext
	}

	r, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig},
		&overrides).ClientConfig()
	if err != nil {
		klog.Fatal(err)
	}

	controllerConfig := &ingress.ControllerConfig{
		Cfg:    r,
		Domain: domain,
	}

	if *envoyEnableXDS {
		controllerConfig.EnvoyXDS = envoyserver.NewXdsServer(*envoyXDSPort, nil)
		controllerConfig.EnvoyListenPort = envoyListenPort
	}

	go func() {
		ingress.NewController(controllerConfig).Start(numThreads)
	}()
	dns.NewController(&dns.ControllerConfig{Cfg: r}).Start(numThreads)
}
