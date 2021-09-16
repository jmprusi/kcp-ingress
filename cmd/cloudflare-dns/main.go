package main

import (
	"flag"
	"log"
	"os"

	"github.com/cloudflare/cloudflare-go"
	"github.com/jmprusi/kcp-ingress/pkg/providers/cloudflaredns"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

const numThreads = 2

var kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig")
var kubecontext = flag.String("context", "", "Context to use in the Kubeconfig file, instead of the current context")
var rootDomain = flag.String("rootDomain", "", "Cloudflare zone root domain")

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

	cloudflareClient, err := cloudflare.NewWithAPIToken(os.Getenv("CF_API_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}
	if *rootDomain == "" {
		log.Fatal("missing -rootDomain flag")
	}

	klog.Infof("Starting the CloudFlare LB Provider")
	cloudflaredns.NewController(r, &cloudflaredns.CloudflareConfig{
		RootDomain: *rootDomain,
		Client:     cloudflareClient,
	}).Start(numThreads)
}
