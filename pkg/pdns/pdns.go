package pdns

import (
	"fmt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/dns/v1"
	"io/ioutil"
	"k8s.io/klog/v2"
	"time"
)

// CloudDNS is a wrapper for GCP SDK api to hold relevant conf
type CloudDNS struct {
	api     *dns.Service
	zone    string
	project string
}

// FromJSON creaties DNS client instance with JSON key file
func FromJSON(filePath, zone, project string) *CloudDNS {
	dat, err := ioutil.ReadFile(filePath)
	if err != nil {
		klog.Fatalln(err)
	}

	conf, err := google.JWTConfigFromJSON(dat, dns.NdevClouddnsReadwriteScope)
	if err != nil {
		klog.Fatalln(err)
	}
	dnsSvc, err := dns.New(conf.Client(oauth2.NoContext))
	if err != nil {
		klog.Fatalln(err)
	}
	return &CloudDNS{api: dnsSvc, zone: zone, project: project}
}

func (c *CloudDNS) applyChange(change *dns.Change) {
	chg, err := c.api.Changes.Create(c.project, c.zone, change).Do()
	if err != nil {
		klog.Errorln(err)
		return
	}

	// wait for change to be acknowledged
	for chg.Status == "pending" {
		time.Sleep(time.Second)

		chg, err = c.api.Changes.Get(c.project, c.zone, chg.Id).Do()
		if err != nil {
			klog.Errorln(err)
			return
		}
	}
}

func (c *CloudDNS) checkForRec(rec *dns.ResourceRecordSet) *dns.ResourceRecordSet {
	list, err := c.api.ResourceRecordSets.List(c.project, c.zone).Name(rec.Name).Type(rec.Type).MaxResults(1).Do()
	if err != nil {
		klog.Errorln(err)
		return nil
	}

	if len(list.Rrsets) < 1 {
		return nil
	}

	return list.Rrsets[0]
}

func (c *CloudDNS) NewRequest() *DNSRequest {
	return &DNSRequest{
		client: c,
		change: &dns.Change{},
	}
}

type DNSRequest struct {
	client *CloudDNS
	change *dns.Change
}

func (d *DNSRequest) Do() {
	d.client.applyChange(d.change)
}

func (d *DNSRequest) deletion(rec *dns.ResourceRecordSet) {
	d.change.Deletions = append(d.change.Deletions, rec)
}

func (d *DNSRequest) addition(rec *dns.ResourceRecordSet) {
	d.change.Additions = append(d.change.Additions, rec)
}

func (d *DNSRequest) CreateRecord(domain, ip string) *DNSRequest {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", domain),
		Rrdatas: []string{ip},
		Ttl:     int64(60),
		Type:    "A",
	}
	klog.Infof("%+v", rec)
	oldRec := d.client.checkForRec(rec)

	if oldRec != nil && rec.Rrdatas[0] == oldRec.Rrdatas[0] {
		klog.V(2).Infof("Record exists: %+v\n", rec)
		return nil
	}

	// Just a safeguard for case there is some stale record
	// as it would fail the API request
	if oldRec != nil && rec.Rrdatas[0] != oldRec.Rrdatas[0] {
		klog.V(2).Infof("Stale record found: %+v\n", oldRec)
		d.deletion(rec)
	}
	d.addition(rec)
	return d
}

// DeleteRecord deletes a record
func (d *DNSRequest) DeleteRecord(domain, ip string) *DNSRequest {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", domain),
		Rrdatas: []string{ip},
		Ttl:     int64(60),
		Type:    "A",
	}

	// We get the existing record from the DNS zone to check if it exists
	list, err := d.client.api.ResourceRecordSets.List(
		d.client.project, d.client.zone).Name(rec.Name).Type(rec.Type).MaxResults(1).Do()

	if err != nil {
		panic(err)
	}

	if len(list.Rrsets) == 0 {
		klog.V(2).Infof("No DNS record found for %s/%s", rec.Name, ip)
		return nil
	}

	// If records and pods have somehow got into inconsistent state
	// we avoid deleting records that don't match the event.
	if ip != list.Rrsets[0].Rrdatas[0] {
		klog.V(2).Infof("No DNS record found for %s with the same IP (%s)", rec.Name, ip)
		return nil
	}
	d.deletion(rec)
	return d
}

// AddToService adds the given IP to A record with multiple IPs
func (d *DNSRequest) AddToService(domain, ip string) *DNSRequest {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", domain),
		Rrdatas: []string{ip},
		Ttl:     int64(60),
		Type:    "A",
	}

	oldRec := d.client.checkForRec(rec)

	if oldRec != nil && dataContains(oldRec, ip) {
		klog.V(2).Infof("Record exists: %+v\n", oldRec)
		return nil
	}

	// Service exists and we need to add the IP
	if oldRec != nil {
		rec.Rrdatas = append(rec.Rrdatas, oldRec.Rrdatas...)
		d.deletion(oldRec)
	}
	d.addition(rec)
	return d
}

// RemoveFromService removes given IP from an A record with multiple IPs
func (d *DNSRequest) RemoveFromService(domain, ip string) *DNSRequest {
	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", domain),
		Rrdatas: nil,
		Ttl:     int64(60),
		Type:    "A",
	}

	oldRec := d.client.checkForRec(rec)
	if oldRec == nil {
		klog.Infof("No record exists for %s\n", domain)
		return nil
	}

	copy(rec.Rrdatas, oldRec.Rrdatas)

	if removeData(rec, ip) {
		d.addition(rec)
		d.deletion(oldRec)
		return d
	}
	klog.Infof("%s service doesn't include  %s\n", domain, ip)
	return d
}

// AddToSRV adds domain to SRV record
func (d *DNSRequest) AddToSRV(srv, domain string, priority int) *DNSRequest {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", srv),
		Rrdatas: []string{domain},
		Ttl:     int64(60),
		Type:    "SRV",
	}

	oldRec := d.client.checkForRec(rec)

	if oldRec != nil {
		// Failsafe
		if rec.Name == oldRec.Name && dataContains(oldRec, domain) {
			klog.V(2).Infof("Record exists: %+v\n", oldRec)
			return nil
		}

		// We need to add the new endpoint
		if rec.Name == oldRec.Name {
			rec.Rrdatas = append(rec.Rrdatas, oldRec.Rrdatas...)
			d.deletion(oldRec)
		}
	}
	d.addition(rec)
	return d
}

// RemoveFromSRV removes domain from SRV record
func (d *DNSRequest) RemoveFromSRV(srv, domain string) *DNSRequest {
	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", srv),
		Rrdatas: nil,
		Ttl:     int64(60),
		Type:    "SRV",
	}

	oldRec := d.client.checkForRec(rec)
	if oldRec == nil {
		klog.Infof("No record exists for %s\n", srv)
		return nil
	}

	copy(rec.Rrdatas, oldRec.Rrdatas)

	if removeData(rec, domain) {
		d.addition(rec)
		d.deletion(oldRec)
		return d
	}
	klog.Infof("%s doesn't include  %s\n", srv, domain)
	return d
}

// UTILS

func dataContains(rec *dns.ResourceRecordSet, data string) bool {
	for _, d := range rec.Rrdatas {
		if d == data {
			return true
		}
	}
	return false
}

func removeData(rec *dns.ResourceRecordSet, data string) bool {
	for i, v := range rec.Rrdatas {
		if v == data {
			rec.Rrdatas = append(rec.Rrdatas[:i], rec.Rrdatas[i+1:]...)
			return true
		}
	}
	return false
}

// BulkSync struct exists to reducse GCP API requests
// when running fallback job to check that DNS records
// exists for all the pods that are supposed to have them

/*
type BulkSync struct {
	client *CloudDNS
	list   map[string]*dns.ResourceRecordSet
}

// GetBulker returns BulkSync instance with loaded DNS list
func GetBulker(client *CloudDNS) *BulkSync {
	bulker := BulkSync{client: client}
	bulker.loadList()
	return &bulker
}

// DeleteRemaining deletes all stale records
// Assumes that bulk.list only has stale records
func (bulk BulkSync) DeleteRemaining() {
	if len(bulk.list) == 0 {
		return
	}

	var deletions []*dns.ResourceRecordSet
	for _, rec := range bulk.list {
		deletions = append(deletions, rec)
	}

	klog.V(2).Infof("%d stale records found\n", len(deletions))

	change := &dns.Change{
		Deletions: deletions,
	}

	_, err := bulk.client.dnsSvc.Changes.Create(bulk.client.project, bulk.client.zone, change).Do()
	if err != nil {
		panic(err)
	}
}

// CheckNext checks next item from loaded DNS records
func (bulk BulkSync) CheckNext(name, owner, ip string) {
	// Check that record exists for the given pod with given IP
	rec, found := bulk.list[name]
	if found && rec.Rrdatas[0] == ip {
		klog.V(2).Infof("Record found for %s:%v\n", name, rec)
		delete(bulk.list, name)
	} else {
		bulk.client.CreateRecord(name, owner, ip)
	}
}

func (bulk BulkSync) loadList() {
	wholeZoneResponse, err := bulk.client.dnsSvc.ResourceRecordSets.List(bulk.client.project, bulk.client.zone).Do()
	if err != nil {
		panic(err)
	}

	list := make(map[string]*dns.ResourceRecordSet)
	for _, rec := range wholeZoneResponse.Rrsets {
		if strings.HasSuffix(rec.Name, bulk.client.domain+".") {
			name := rec.Name[:strings.IndexByte(rec.Name, '.')]
			list[name] = rec
			klog.V(2).Infof("Found DNS record for %s:%s", name, rec.Name)
		}
	}
	bulk.list = list
}
*/
