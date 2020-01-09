package main

import (
	"flag"
	"fmt"
	"github.com/tanelmae/private-dns/internal/service"
	"github.com/tanelmae/private-dns/pkg/gcp"
	"github.com/tanelmae/private-dns/pkg/pdns"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"os"
	"strings"
)

// TODO: support passing in kubeconfig and context for local testing
func main() {
	klog.InitFlags(nil)
	// Fix for Kubernetes client trying to log to /tmp
	klog.SetOutput(os.Stderr)

	project := flag.String("project", "", "GCP project where the DNS zone is. Defaults to the same as GKE cluster.")
	zone := flag.String("zone", "", "GCP DNS zone where to write the records")
	saFile := flag.String("sa-file", "", "Path to GCP service account credentials")
	namespace := flag.String("namespace", "", "Limits private DNS to the given namesapce")
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file. Not needed on Kubernetes.")

	flag.Parse()

	config, err := resolveConfig(*kubeconfig)
	if err != nil {
		klog.Fatalln(err)
	}

	if *project == "" {
		*project, err = gcp.GetProject()
	}

	if *project == "" {
		klog.Fatalln("Failed to resolve GCP project")
	}

	// JSON key file for service account with DNS admin permissions
	dnsClient := pdns.FromJSON(*saFile, *zone, *project)
	klog.Infof("DNS client: %+v\n", dnsClient)
	klog.Flush()

	controller, err := service.NewPrivateDNSController(config, dnsClient, *namespace)
	if err != nil {
		klog.Fatalln(err)
	}

	controller.Run()
}

/*
In cluster config is used when
- kubeconfig path not given as an CLI arg
- KUBECONFIG is not set
- $HOME/.kube/config file doesn't exsist
Those will be checked in that order
Will finally return ErrNotInCluster if not running on Kubernetes either.
*/
func resolveConfig(configPath string) (*rest.Config, error) {
	if configPath == "" {
		kubeConfig := os.Getenv("KUBECONFIG")
		if len(kubeConfig) == 0 {
			homedir, err := os.UserHomeDir()
			if err != nil {
				return rest.InClusterConfig()
			}
			kubeConfig = fmt.Sprintf("%s/.kube/config", homedir)
		}

		if strings.Contains(kubeConfig, ":") {
			kconfigs := strings.Split(kubeConfig, ":")
			klog.Infof("KUBECONFIG contains %d paths. Picking the first one: %s\n",
				len(kconfigs), kconfigs[0])
			kubeConfig = kconfigs[0]
		}
		configPath = kubeConfig
	}

	if _, err := os.Stat(configPath); err == nil {
		klog.Infof("Using local kubeconfig file: %s", configPath)
		config, err := clientcmd.BuildConfigFromFlags("", configPath)
		if err != nil {
			return nil, err
		}
		return config, nil
	}

	klog.Infoln("Using incluster config")
	return rest.InClusterConfig()
}
