package main

import (
	"flag"
	"log"
	"os"

	"github.com/cloudflare/cloudflare-go"

	"github.com/jmprusi/kcp-ingress/pkg/providers/cloudflarelb"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

const numThreads = 2

var kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig")
var kubecontext = flag.String("context", "", "Context to use in the Kubeconfig file, instead of the current context")
var lbdomain = flag.String("lbdomain", "", "Cloudflare LB Domain")

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
	if *lbdomain == "" {
		log.Fatal("missing -lbdomain flag")
	}

	klog.Infof("Starting the CloudFlare LB Provider")
	cloudflarelb.NewController(r, &cloudflarelb.CloudflareConfig{
		LBDomain: *lbdomain,
		Client:   cloudflareClient,
	}).Start(numThreads)
}
