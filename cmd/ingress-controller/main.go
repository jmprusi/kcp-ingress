package main

import (
	"flag"

	ingressSyncing "github.com/jmprusi/kcp-ingress/pkg/syncing/ingress"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"knative.dev/net-kourier/pkg/envoy/server"
)

const numThreads = 2

var kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig")

// TODO(jmprusi): enable this as override.
// var kubecontext = flag.String("context", "", "Context to use in the Kubeconfig file, instead of the current context")

var envoyEnableXDS = flag.Bool("envoyxds", false, "Start an Envoy control plane")
var envoyXDSPort = flag.Uint("envoyxds-port", 18000, "Envoy control plane port")

// Right now this domain flag will determine if a host gets rewritten or not. If the ingress requests a host that matches this domain
// It will be added to the ingress Status.
var domain = flag.String("domain", "kcp-apps.127.0.0.1.nip.io", "The domain to use to expose ingresses")

var envoyListenPort = flag.Uint("envoy-listener-port", 80, "Envoy default listener port")

func main() {
	flag.Parse()

	kubeconfig, err := clientcmd.LoadFromFile(*kubeconfig)
	if err != nil {
		klog.Fatal(err)
	}

	config := &ingressSyncing.SyncingConfig{
		Kubeconfig:      kubeconfig,
		EnvoyXDS:        nil,
		Domain:          domain,
		EnvoyListenPort: envoyListenPort,
	}

	if *envoyEnableXDS {
		config.EnvoyXDS = server.NewXdsServer(*envoyXDSPort, nil)
	}

	syncing := ingressSyncing.NewIngressSyncing(config)
	klog.Infof("Starting ingress syncing")
	syncing.WaitUntilDone()
}
