package main

import (
	"flag"
	"github.com/tanelmae/private-dns/internal/service"
	"k8s.io/klog/v2"
	"os"
)

// TODO: support passing in kubeconfig and context for local testing
func main() {
	klog.InitFlags(nil)
	// Fix for Kubernetes client trying to log to /tmp
	klog.SetOutput(os.Stderr)
	flag.Parse()

	controller, err := service.NewPrivateDNSController(nil, nil)
	if err != nil {
		klog.Fatalln(err)
	}

	// TODO: graceful controller shutdown
	controller.Run()
}
