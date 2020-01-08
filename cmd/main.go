package main

import (
	"flag"
	"fmt"
	"github.com/tanelmae/private-dns/internal/service"
	"github.com/tanelmae/private-dns/pkg/pdns"
	"io/ioutil"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"net/http"
	"os"
	"strings"
	"time"
)

// TODO: support passing in kubeconfig and context for local testing
func main() {
	klog.InitFlags(nil)
	// Fix for Kubernetes client trying to log to /tmp
	klog.SetOutput(os.Stderr)

	project := flag.String("project", "", "GCP project where the DNS zone is. Defaults to the same as GKE cluster.")
	zone := flag.String("zone", "", "GCP DNS zone where to write the records")
	saFile := flag.String("sa-file", "", "Path to GCP service account credentials")

	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file. Not needed on Kubernetes.")

	flag.Parse()

	config, err := resolveConfig(*kubeconfig)
	if err != nil {
		klog.Fatalln(err)
	}

	// Init DNS client and pass it in

	if *project == "" {
		for i := 1; i <= 3; i++ {
			p, err := getMetadata("project/project-id")
			if err != nil {
				klog.Infoln("Reading GCP project name from metadata failed")
				time.Sleep(time.Second * time.Duration(i))
				klog.Infoln("Will try again reading GCP project name from metadata")
			} else {
				project = &p
				break
			}
		}

	}

	if *project == "" {
		klog.Fatalln("Failed to resolve GCP project")
	}

	// JSON key file for service account with DNS admin permissions
	dnsClient := pdns.FromJSON(*saFile, *zone, *project)
	klog.Infof("DNS client: %+v\n", dnsClient)
	klog.Flush()

	controller, err := service.NewPrivateDNSController(config, nil)
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

func getMetadata(urlPath string) (string, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET",
		fmt.Sprintf("http://metadata/computeMetadata/v1/%s", urlPath), nil)
	req.Header.Add("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(bodyBytes), nil
}
