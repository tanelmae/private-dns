package records

import (
	"fmt"
	"github.com/tanelmae/private-dns/pkg/pdns"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"time"
)

type close struct{}

// New creates the controller to watch pods with given properties
// and trigger changes in the DNS records
func New(name, domain, label, namespace, srvName string, service bool, podTimeout time.Duration,
	kubeClient *kubernetes.Clientset, cloudDNS *pdns.CloudDNS) Manager {

	m := Manager{
		name:       name,
		kubeClient: kubeClient,
		dnsClient:  cloudDNS,
		namespace:  namespace,
		label:      label,
		domain:     domain,
		srvName:    srvName,
		service:    service,
		timeout:    podTimeout,
		pendingIP:  make(map[string]time.Time),
		stopChan:   make(chan struct{}),
	}

	watchlist := cache.NewFilteredListWatchFromClient(
		m.kubeClient.CoreV1().RESTClient(), "pods", m.namespace,
		func(options *metav1.ListOptions) {
			options.LabelSelector = m.label
		})

	s, c := cache.NewInformer(
		watchlist,
		&v1.Pod{},
		0,
		//m.watcherResync,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    m.podCreated,
			DeleteFunc: m.podDeleted,
			UpdateFunc: m.podUpdated,
		},
	)
	m.store = s
	m.controller = c
	return m
}

// Manager ..
type Manager struct {
	name       string
	kubeClient *kubernetes.Clientset
	dnsClient  *pdns.CloudDNS
	timeout    time.Duration
	resLabel   string
	pendingIP  map[string]time.Time
	stopChan   chan struct{}
	namespace  string
	label      string
	domain     string
	srvName    string
	service    bool
	store      cache.Store
	controller cache.Controller
}

// Start will start watching pods defined in the CRD
func (m Manager) Start() {
	/*
		Initial startup will triggger AddFunc for all the pods that match the watchlist.
		Handlers are run sequentally as the events come in.
	*/
	klog.Infof("Will watch pods with %s label in %s namespace\n", m.label, m.namespace)

	// Checks with given interval that all expected records are there
	// and removes any stale record if any is found.
	m.controller.Run(m.stopChan)
	klog.Infof("Records manager for %s/%s\n stopped", m.name, m.namespace)
}

// Stop will close the controller
func (m Manager) Stop() {
	m.stopChan <- close{}
	klog.Infof("Stopping pod watcher for %s/%s \n", m.namespace, m.name)
}

// Destroy will close the controller and delete all DNS records
// Should be used when CRD is deleted
func (m Manager) Destroy() {
	m.Stop()
	klog.Infof("Remove all %s/%s private DNS records\n", m.namespace, m.name)

	if len(m.store.List()) > 0 {
		for _, i := range m.store.List() {
			m.podDeleted(i)
		}
	} else {
		klog.Infof("No pods found for %s/%s\n", m.namespace, m.name)
	}
}

func (m Manager) podAddresss(pod *v1.Pod) string {
	// Example: httppod-0.httpstatefulset.example.com
	return fmt.Sprintf("%s.%s.%s", pod.GetName(), pod.GetOwnerReferences()[0].Name, m.domain)
}

func (m Manager) serviceAddresss(pod *v1.Pod) string {
	// Example: httpstatefulset.example.com
	return fmt.Sprintf("%s.%s", pod.GetOwnerReferences()[0].Name, m.domain)
}

func (m Manager) srvAddresss() string {
	return m.srvName
}

func (m Manager) podUpdated(oldObj, newObj interface{}) {
	newPod := newObj.(*v1.Pod)
	klog.V(2).Infof("Pod updated: %s\n", newPod.Name)

	lastTime, isPendingIP := m.pendingIP[fmt.Sprintf("%s/%s", newPod.GetNamespace(), newPod.GetName())]
	if isPendingIP && newPod.Status.PodIP != "" {
		klog.V(2).Infof("Able to resolve a pending record for %s since %s\n", newPod.GetName(), lastTime.String())

		m.dnsClient.NewRequest().CreateRecord(m.podAddresss(newPod), newPod.Status.PodIP).Do()
		delete(m.pendingIP, newPod.GetName())
	}
}

// Handler for pod creation
func (m Manager) podCreated(obj interface{}) {
	pod := obj.(*v1.Pod)
	podName := pod.GetName()
	namespace := pod.GetNamespace()
	klog.V(2).Infof("Pod created: %s/%s", namespace, podName)
	var err error

	if pod.Status.PodIP == "" {
		klog.Warningln("Pod IP missing. Will try to resolve.")
		wait.Poll(2*time.Second, m.timeout, func() (bool, error) {
			pod, err := m.kubeClient.CoreV1().Pods(
				pod.GetNamespace()).Get(pod.GetName(), metav1.GetOptions{})
			if err != nil {
				klog.Error(err)
				return false, nil
			}
			if pod.Status.PodIP != "" {
				klog.V(2).Infof("Pod IP resolved: %s\n", pod.Status.PodIP)
				return true, nil
			}
			return false, nil
		})

		pod, err = m.kubeClient.CoreV1().Pods(
			pod.GetNamespace()).Get(pod.GetName(), metav1.GetOptions{})
		if err != nil {
			klog.Error(err)
		}

		// Leave if for the pod updated event handler
		if err != nil || pod.Status.PodIP == "" {
			klog.V(2).Infof("Failed get pod IP in %s\n", m.timeout)
			m.pendingIP[fmt.Sprintf("%s/%s", namespace, podName)] = time.Now()
			return
		}
	}

	req := m.dnsClient.NewRequest()
	req.CreateRecord(m.podAddresss(pod), pod.Status.PodIP)
	if m.service {
		req.AddToService(m.serviceAddresss(pod), pod.Status.PodIP)
	}

	if m.srvName != "" {
		req.AddToSRV(m.srvAddresss(), m.serviceAddresss(pod), 1)
	}
	req.Do()
}

// Handler for pod deletion events
func (m Manager) podDeleted(obj interface{}) {
	pod := obj.(*v1.Pod)
	klog.V(2).Infof("Pod deleted: %s", pod.GetName())

	req := m.dnsClient.NewRequest()

	req.DeleteRecord(m.podAddresss(pod), pod.Status.PodIP)

	if m.service {
		req.RemoveFromService(m.serviceAddresss(pod), pod.Status.PodIP)
	}

	if m.srvName != "" {
		req.RemoveFromSRV(m.srvAddresss(), m.serviceAddresss(pod))
	}
	req.Do()
}
