package main

import (
	"flag"

	"github.com/jmprusi/kcp-ingress/pkg/reconciler/ingress"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

const numThreads = 2

var kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig")
var kubecontext = flag.String("context", "", "Context to use in the Kubeconfig file, instead of the current context")

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

	klog.Infof("Starting ingress controller")

	ingress.NewController(r).Start(numThreads)
}
