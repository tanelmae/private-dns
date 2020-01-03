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

type DNSResource struct {
	Namespace string
	Label     string
	Domain    string
	SrvName   string
	Service   bool
}

// Run starts the service
func Run(res DNSResource, podTimeout time.Duration,
	kubeClient *kubernetes.Clientset, cloudDNS *pdns.CloudDNS, stopCh chan struct{}) {

	manager := RecordsManager{
		kubeClient: kubeClient,
		dnsClient:  cloudDNS,
		resource:   res,
		timeout:    podTimeout,
		pendingIP:  make(map[string]time.Time),
		stopChan:   stopCh,
	}

	manager.startWatcher()
}

// RecordsManager ..
type RecordsManager struct {
	kubeClient *kubernetes.Clientset
	dnsClient  *pdns.CloudDNS
	resource   DNSResource
	timeout    time.Duration
	namespace  string
	resLabel   string
	pendingIP  map[string]time.Time
	stopChan   chan struct{}
}

func (m RecordsManager) startWatcher() {
	watchlist := cache.NewFilteredListWatchFromClient(
		m.kubeClient.CoreV1().RESTClient(), "pods", m.resource.Namespace,
		func(options *metav1.ListOptions) {
			options.LabelSelector = m.resource.Label
		})

	_, controller := cache.NewInformer(
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
	/*
		Initial startup will triggger AddFunc for all the pods that match the watchlist.
		Handlers are run sequentally as the events come in.
	*/
	klog.Infof("Will watch pods with %s label in %s namespace\n", m.resource.Label, m.resource.Namespace)

	// Checks with given interval that all expected records are there
	// and removes any stale record if any is found.
	//m.startSyncJob()
	go controller.Run(m.stopChan)
	<-m.stopChan
	m.removeAllRecords()
}

func (m RecordsManager) removeAllRecords() {
	klog.Infoln("Remove all records")
}

func (m RecordsManager) podAddresss(pod *v1.Pod) string {
	// Example: httppod-0.httpstatefulset.example.com
	return fmt.Sprintf("%s.%s.%s", pod.GetName(), pod.GetOwnerReferences()[0].Name, m.resource.Domain)
}

func (m RecordsManager) serviceAddresss(pod *v1.Pod) string {
	// Example: httpstatefulset.example.com
	return fmt.Sprintf("%s.%s", pod.GetOwnerReferences()[0].Name, m.resource.Domain)
}

func (m RecordsManager) srvAddresss() string {
	return m.resource.SrvName
}

func (m RecordsManager) podUpdated(oldObj, newObj interface{}) {
	newPod := newObj.(*v1.Pod)
	klog.V(2).Infof("Pod updated: %s\n", newPod.Name)

	/*
		Pod update handler is triggered quite often and for things
		we don't care about here. So we keep in memory list of pods that
		we know that record hasn't been created.
	*/
	lastTime, isPendingIP := m.pendingIP[fmt.Sprintf("%s/%s", newPod.GetNamespace(), newPod.GetName())]
	if isPendingIP && newPod.Status.PodIP != "" {
		klog.V(2).Infof("Able to resolve a pending record for %s since %s\n", newPod.GetName(), lastTime.String())

		m.dnsClient.NewRequest().CreateRecord(m.podAddresss(newPod), newPod.Status.PodIP).Do()
		delete(m.pendingIP, newPod.GetName())
	}
}

// Handler for pod creation
func (m RecordsManager) podCreated(obj interface{}) {
	pod := obj.(*v1.Pod)
	podName := pod.GetName()
	namespace := pod.GetNamespace()
	klog.V(2).Infof("Pod created: %s/%s", namespace, podName)
	var err error

	/*
		Needs to wait for slow services to be ready but
		not block everything if pod fails for whatever reasons.
		Either pod updated event handler or fallback sync jobs
		should catch those missing DNS recrods.
	*/
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
	if m.resource.Service {
		req.AddToService(m.serviceAddresss(pod), pod.Status.PodIP)
	}

	if m.resource.SrvName != "" {
		req.AddToSRV(m.srvAddresss(), m.serviceAddresss(pod), 1)
	}
	req.Do()
}

// Handler for pod deletion events
func (m RecordsManager) podDeleted(obj interface{}) {
	pod := obj.(*v1.Pod)
	klog.V(2).Infof("Pod deleted: %s", pod.GetName())

	req := m.dnsClient.NewRequest()

	req.DeleteRecord(m.podAddresss(pod), pod.Status.PodIP)

	if m.resource.Service {
		req.RemoveFromService(m.serviceAddresss(pod), pod.Status.PodIP)
	}

	if m.resource.SrvName != "" {
		req.RemoveFromSRV(m.srvAddresss(), m.serviceAddresss(pod))
	}
	req.Do()
}
