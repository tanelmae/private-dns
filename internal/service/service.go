package service

import (
	"fmt"
	"github.com/tanelmae/private-dns/internal/records"
	dnsAPI "github.com/tanelmae/private-dns/pkg/apis/privatedns/v1"
	"github.com/tanelmae/private-dns/pkg/gcp"
	"github.com/tanelmae/private-dns/pkg/gen/clientset/privatedns"
	dnsV1 "github.com/tanelmae/private-dns/pkg/gen/informers/externalversions/privatedns/v1"
	"github.com/tanelmae/private-dns/pkg/pdns"

	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"k8s.io/klog/v2"
	"os"
	"os/signal"

	"sync"
	"syscall"
	"time"
)

const (
	defaultTimeout = time.Minute * 2
)

type close struct{}

// New creates a new private DNS Controller
func New(kubeConf *rest.Config, dnsClient *pdns.CloudDNS, namespace string) (*Controller, error) {
	var err error

	c := &Controller{
		dnsClient: dnsClient,
		res:       make(map[string]*records.Manager),
		namespace: namespace, // Empty will mean all
	}

	c.kubeClient, err = kubernetes.NewForConfig(kubeConf)
	if err != nil {
		return nil, err
	}

	c.crdClient, err = privatedns.NewForConfig(kubeConf)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// Controller is the controller that manages DNS workers based on found CRDs
type Controller struct {
	mu         sync.Mutex
	kubeClient *kubernetes.Clientset
	crdClient  *privatedns.Clientset
	dnsClient  *pdns.CloudDNS
	res        map[string]*records.Manager
	namespace  string
}

// Run starts the private DNS service
func (c *Controller) Run() {

	// client privatedns.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers
	crdbInformer := dnsV1.NewPrivateDNSInformer(
		c.crdClient,
		c.namespace,
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

	stopChan := make(chan struct{})
	go crdbInformer.Run(stopChan)

	c.gracefulShutdownHandler(stopChan)
}

func (c *Controller) gracefulShutdownHandler(stopChan chan struct{}) {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-done

	// Stop CRD watcher
	stopChan <- close{}

	// Stop any pod watchers that might be running
	if len(c.res) > 0 {
		for _, i := range c.res {
			i.Stop()
		}
	}

	time.Sleep(time.Second)
	klog.Infoln("Private DNS service Stopped")
}

func (c *Controller) dnsRequestCreated(obj interface{}) {
	pdns := obj.(*dnsAPI.PrivateDNS)
	klog.Infof("%s created in %s namespace", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)
	regKey := fmt.Sprintf("%s/%s", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)

	if pdns.Spec.Subdomain {
		name, err := gcp.GetClusterName()
		if err != nil {
			klog.Fatalln(err)
		}
		location, err := gcp.GetClusterLocation()
		if err != nil {
			klog.Fatalln(err)
		}
		pdns.Spec.Domain = fmt.Sprintf("%s.%s.%s", name, location, pdns.Spec.Domain)
	}

	m := records.New(
		pdns.Name,
		pdns.Spec.Domain,
		pdns.Spec.Label,
		pdns.GetNamespace(),
		pdns.Spec.SRVPort,
		pdns.Spec.SRVProto,
		pdns.Spec.Service,
		pdns.Spec.PodTimeout,
		c.kubeClient,
		c.dnsClient,
	)

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.res[regKey]; exists {
		klog.Errorf("Pod watcher for %s already exists. Doing nothing.", regKey)
		return
	}

	c.res[regKey] = &m
	go m.Start()
}

func (c *Controller) dnsRequestDeleted(obj interface{}) {
	pdns := obj.(*dnsAPI.PrivateDNS)
	klog.Infof("%s deleted in %s namespace", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)

	regKey := fmt.Sprintf("%s/%s", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)
	c.mu.Lock()
	defer c.mu.Unlock()

	if m, exists := c.res[regKey]; exists {
		m.Destroy()
		delete(c.res, regKey)
	}
}

func (c *Controller) dnsRequestUpdated(old, new interface{}) {
	pdns := old.(*dnsAPI.PrivateDNS)
	klog.Infof("%s updated in %s namespace", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)

	regKey := fmt.Sprintf("%s/%s", pdns.ObjectMeta.Name, pdns.ObjectMeta.Namespace)
	c.mu.Lock()
	defer c.mu.Unlock()

	if m, exists := c.res[regKey]; exists {
		m.Destroy()
		delete(c.res, regKey)
	} else {
		// This shouldn't happen
		klog.Errorf("Pod watcher for %s didn't exist exists! Something is broken!", regKey)
	}

	m := records.New(
		pdns.Name,
		pdns.Spec.Domain,
		pdns.Spec.Label,
		pdns.GetNamespace(),
		pdns.Spec.SRVPort,
		pdns.Spec.SRVProto,
		pdns.Spec.Service,
		pdns.Spec.PodTimeout,
		c.kubeClient,
		c.dnsClient,
	)
	c.res[regKey] = &m
	go m.Start()

}
