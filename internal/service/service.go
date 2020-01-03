package service

import (
	"fmt"
	"github.com/tanelmae/private-dns/internal/records"
	dnsAPI "github.com/tanelmae/private-dns/pkg/apis/privatedns/v1"
	"github.com/tanelmae/private-dns/pkg/gen/clientset/privatedns"
	dnsV1 "github.com/tanelmae/private-dns/pkg/gen/informers/externalversions/privatedns/v1"
	"github.com/tanelmae/private-dns/pkg/pdns"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"sync"
)

const (
	defaultTimeout = time.Minute * 2
)

type CloseController struct{}

// TODO: support both auto init and passing API clients in for testing
func NewPrivateDNSController(kubeClient *kubernetes.Clientset, dnsClient *pdns.CloudDNS) (*Controller, error) {
	return &Controller{
		kubeClient: kubeClient,
		dnsClient:  dnsClient,
		res:        make(map[string]chan struct{}),
	}, nil
}

type Controller struct {
	mu         sync.Mutex
	kubeClient *kubernetes.Clientset
	dnsClient  *pdns.CloudDNS
	res        map[string]chan struct{}
}

// Run starts the private DNS service
func (c *Controller) Run() {

	/*
		if c.kubeClient == nil {
		}*/
	config, err := resolveConfig()
	if err != nil {
		klog.Fatalln(err)
	}

	kclient, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalln(err)
	}
	c.kubeClient = kclient

	crdClient, err := privatedns.NewForConfig(config)
	if err != nil {
		klog.Fatalln(err)
	}

	// client privatedns.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers
	crdbInformer := dnsV1.NewPrivateDNSInformer(
		crdClient,
		metaV1.NamespaceAll,
		0,
		cache.Indexers{},
	)

	crdbInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.dnsRequestCreated,
			DeleteFunc: c.dnsRequestDeleted,
			UpdateFunc: c.dnsRequestUpdated,
		},
	)

	crdbInformer.Run(wait.NeverStop)
	// Create RecordsManagers per found CRDs
}

// TODO: create managers per CRD and destroy manager and records when CRD is removed
func (c *Controller) dnsRequestCreated(obj interface{}) {
	pdns := obj.(*dnsAPI.PrivateDNS)
	klog.Infof("%s create in %s namespace", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)

	klog.Infof("%+v", *c)
	c.mu.Lock()
	defer c.mu.Unlock()

	dnsResource := records.DNSResource{
		Namespace: pdns.GetNamespace(),
		Label:     pdns.Spec.Label,
		Domain:    pdns.Spec.Domain,
		SrvName:   pdns.Spec.SRVName,
		Service:   pdns.Spec.Service,
	}

	stopCh := make(chan struct{})
	records.Run(
		dnsResource,
		pdns.Spec.PodTimeout,
		c.kubeClient,
		c.dnsClient,
		stopCh,
	)
	c.res[fmt.Sprintf("%s/%s", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)] = stopCh
}

func (c *Controller) dnsRequestDeleted(obj interface{}) {
	pdns := obj.(*dnsAPI.PrivateDNS)
	klog.Infof("%s deleted in %s namespace", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)

	c.mu.Lock()
	defer c.mu.Unlock()

	regKey := fmt.Sprintf("%s/%s", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)
	c.res[regKey] <- CloseController{}
	close(c.res[regKey])
	delete(c.res, regKey)
}

func (c *Controller) dnsRequestUpdated(old, new interface{}) {
	pdns := old.(*dnsAPI.PrivateDNS)
	klog.Infof("%s updated in %s namespace", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)
	klog.Infof("Not supported yet")
	/*
		c.mu.Lock()
		defer c.mu.Unlock()
	*/
}

// Will use local conf file when found. Otherwise assumes running
// on a Kubernetes cluster.
func resolveConfig() (*rest.Config, error) {
	// construct the path to resolve to `~/.kube/config`

	kubeConfigPath := os.Getenv("KUBECONFIG")
	if len(kubeConfigPath) == 0 {
		homedir, err := os.UserHomeDir()
		if err != nil {
			return rest.InClusterConfig()
		}
		kubeConfigPath = fmt.Sprintf("%s/.kube/config", homedir)
	}

	if strings.Contains(kubeConfigPath, ":") {
		kconfigs := strings.Split(kubeConfigPath, ":")
		klog.Infof("KUBECONFIG contains %d paths. Picking the first one: %s\n", len(kconfigs), kconfigs[0])
		kubeConfigPath = kconfigs[0]
	}

	if _, err := os.Stat(kubeConfigPath); err == nil {
		klog.Infof("Using local kubeconfig file: %s", kubeConfigPath)
		config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
		if err != nil {
			return nil, err
		}
		return config, nil
	}
	klog.Infoln("Using incluster config")
	return rest.InClusterConfig()
}
