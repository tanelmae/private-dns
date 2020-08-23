package gcp

import (
	"context"
	"fmt"
	"time"

	"github.com/tanelmae/private-dns/pkg/pdns"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/option"
	"k8s.io/klog/v2"
)

const (
	typeA   = "A"
	typeSRV = "SRV"
	typePTR = "PTR"

	defaultTTL int64 = 60

	statusPending = "pending"
)

// CloudDNS is a wrapper for GCP SDK api to hold relevant conf
type CloudDNS struct {
	api         *dns.Service
	zone        string
	reverseZone string
	project     string
}

// FromJSON creaties DNS client instance with JSON key file
func FromJSON(filePath, zone, reverseZone, project string) *CloudDNS {
	dnsSvc, err := dns.NewService(context.Background(), option.WithCredentialsFile(filePath))
	if err != nil {
		klog.Fatalln(err)
	}

	return &CloudDNS{
		api:         dnsSvc,
		zone:        zone,
		reverseZone: reverseZone,
		project:     project,
	}
}

func (c *CloudDNS) applyChange(changes *dns.Change) error {
	chg, err := c.api.Changes.Create(c.project, c.zone, changes).Do()
	if err != nil {
		return err
	}

	// wait for change to be acknowledged
	for chg.Status == statusPending {
		time.Sleep(time.Second)

		chg, err = c.api.Changes.Get(c.project, c.zone, chg.Id).Do()
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *CloudDNS) applyRevChange(changes *dns.Change) error {
	chg, err := c.api.Changes.Create(c.project, c.reverseZone, changes).Do()
	if err != nil {
		return err
	}

	// wait for change to be acknowledged
	for chg.Status == statusPending {
		time.Sleep(time.Second)

		chg, err = c.api.Changes.Get(c.project, c.reverseZone, chg.Id).Do()
		if err != nil {
			return err
		}
	}
	return nil
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

func (c *CloudDNS) NewRequest() pdns.DNSRequest {
	return &DNSRequest{
		client:    c,
		change:    &dns.Change{},
		revChange: &dns.Change{},
	}
}

type DNSRequest struct {
	client    *CloudDNS
	change    *dns.Change
	revChange *dns.Change
}

// Do makes the request with all the attached changes
// No error would be returned when no changes have been added
func (d *DNSRequest) Do() error {
	var err error

	if len(d.change.Deletions) > 1 || len(d.change.Additions) > 1 {
		err = d.client.applyChange(d.change)
		if err != nil {
			return err
		}
	}

	if len(d.revChange.Deletions) > 1 || len(d.revChange.Additions) > 1 {
		err = d.client.applyRevChange(d.revChange)
		if err != nil {
			return err
		}
	}
	return err
}

func (d *DNSRequest) deletion(rec *dns.ResourceRecordSet) {
	d.change.Deletions = append(d.change.Deletions, rec)
}

func (d *DNSRequest) addition(rec *dns.ResourceRecordSet) {
	d.change.Additions = append(d.change.Additions, rec)
}

func (d *DNSRequest) revDeletion(rec *dns.ResourceRecordSet) {
	d.revChange.Deletions = append(d.revChange.Deletions, rec)
}

func (d *DNSRequest) revAddition(rec *dns.ResourceRecordSet) {
	d.revChange.Additions = append(d.revChange.Additions, rec)
}

// AddRecord adds A record with single IP
func (d *DNSRequest) AddRecord(domain, ip string) {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", domain),
		Rrdatas: []string{ip},
		Ttl:     defaultTTL,
		Type:    typeA,
	}

	oldRec := d.client.checkForRec(rec)

	if oldRec != nil && rec.Rrdatas[0] == oldRec.Rrdatas[0] {
		klog.V(2).Infof("Record exists: %+v\n", rec)
		return
	}

	// Just a safeguard for case there is some stale record
	// as it would fail the API request
	if oldRec != nil && rec.Rrdatas[0] != oldRec.Rrdatas[0] {
		klog.V(2).Infof("Stale record found: %+v\n", oldRec)
		d.deletion(rec)
	}
	d.addition(rec)
	if d.client.reverseZone != "" {
		d.AddReverseRecord(domain, ip)
	}
}

// RemoveRecord deletes A record with a single IP
func (d *DNSRequest) RemoveRecord(domain, ip string) {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", domain),
		Rrdatas: []string{ip},
		Ttl:     defaultTTL,
		Type:    typeA,
	}

	// We get the existing record from the DNS zone to check if it exists
	list, err := d.client.api.ResourceRecordSets.List(
		d.client.project, d.client.zone).Name(rec.Name).Type(rec.Type).MaxResults(1).Do()

	if err != nil {
		klog.V(2).Infoln(err)
		return
	}

	if len(list.Rrsets) == 0 {
		klog.V(2).Infof("No DNS record found for %s/%s", rec.Name, ip)
		return
	}

	// If records and pods have somehow got into inconsistent state
	// we avoid deleting records that don't match the event.
	if ip != list.Rrsets[0].Rrdatas[0] {
		klog.V(2).Infof("No DNS record found for %s with the same IP (%s)", rec.Name, ip)
		return
	}
	d.deletion(rec)

	if d.client.reverseZone != "" {
		d.RemoveReverseRecord(domain, ip)
	}
}

// AddReverseRecord adds a PTR record for the reverse lookup
func (d *DNSRequest) AddReverseRecord(domain, ip string) {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.in-addr.arpa.", ip),
		Rrdatas: []string{domain},
		Ttl:     defaultTTL,
		Type:    typePTR,
	}

	oldRec := d.client.checkForRec(rec)

	if oldRec != nil && rec.Rrdatas[0] == oldRec.Rrdatas[0] {
		klog.V(2).Infof("Record exists: %+v\n", rec)
		return
	}

	// Just a safeguard for case there is some stale record
	// as it would fail the API request
	if oldRec != nil && rec.Rrdatas[0] != oldRec.Rrdatas[0] {
		klog.V(2).Infof("Stale record found: %+v\n", oldRec)
		d.deletion(rec)
	}
	d.revAddition(rec)
}

// RemoveReverseRecord removes a PTR record from the reverse lookup zone
func (d *DNSRequest) RemoveReverseRecord(domain, ip string) {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.in-addr.arpa.", ip),
		Rrdatas: []string{domain},
		Ttl:     defaultTTL,
		Type:    typePTR,
	}

	// We get the existing record from the DNS zone to check if it exists
	list, err := d.client.api.ResourceRecordSets.List(
		d.client.project, d.client.reverseZone).Name(rec.Name).Type(rec.Type).MaxResults(1).Do()

	if err != nil {
		klog.V(2).Infoln(err)
		return
	}

	if len(list.Rrsets) == 0 {
		klog.V(2).Infof("No PTR record found for %s/%s", rec.Name, ip)
		return
	}

	// If records and pods have somehow got into inconsistent state
	// we avoid deleting records that don't match the event.
	if domain != list.Rrsets[0].Rrdatas[0] {
		klog.V(2).Infof("No PTR record found for %s with the same domain (%s)", rec.Name, domain)
		return
	}
	d.revDeletion(rec)
}

// AddToService adds the given IP to A record with multiple IPs
func (d *DNSRequest) AddToService(domain, ip string) {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", domain),
		Rrdatas: []string{ip},
		Ttl:     defaultTTL,
		Type:    typeA,
	}

	oldRec := d.client.checkForRec(rec)

	if oldRec != nil && dataContains(oldRec, ip) {
		klog.V(2).Infof("Service %s record exists and contains %s\n", domain, ip)
		return
	}

	// Service exists and we need to add the IP
	if oldRec != nil {
		rec.Rrdatas = append(rec.Rrdatas, oldRec.Rrdatas...)
		d.deletion(oldRec)
	}
	d.addition(rec)
}

// RemoveFromService removes given IP from an A record with multiple IPs
func (d *DNSRequest) RemoveFromService(domain, ip string) {
	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", domain),
		Rrdatas: nil,
		Ttl:     defaultTTL,
		Type:    typeA,
	}

	oldRec := d.client.checkForRec(rec)
	if oldRec == nil {
		klog.V(2).Infof("No record exists for %s\n", domain)
		return
	}

	if newRec, ok := removeData(oldRec, ip); ok {
		d.addition(newRec)
	} else {
		klog.V(2).Infof("%s service doesn't include %s\n", domain, ip)
	}

	d.deletion(oldRec)
}

// AddToSRV adds domain to SRV record
func (d *DNSRequest) AddToSRV(srv, domain string, priority int) {

	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", srv),
		Rrdatas: []string{domain},
		Ttl:     defaultTTL,
		Type:    typeSRV,
	}

	oldRec := d.client.checkForRec(rec)

	if oldRec != nil {
		// Failsafe
		if rec.Name == oldRec.Name && dataContains(oldRec, domain) {
			klog.V(2).Infof("Record exists: %+v\n", oldRec)
			return
		}

		// We need to add the new endpoint
		if rec.Name == oldRec.Name {
			rec.Rrdatas = append(rec.Rrdatas, oldRec.Rrdatas...)
			d.deletion(oldRec)
		}
	}
	d.addition(rec)
}

// RemoveFromSRV removes domain from SRV record
func (d *DNSRequest) RemoveFromSRV(srv, domain string) {
	rec := &dns.ResourceRecordSet{
		Name:    fmt.Sprintf("%s.", srv),
		Rrdatas: nil,
		Ttl:     defaultTTL,
		Type:    typeSRV,
	}

	oldRec := d.client.checkForRec(rec)
	if oldRec == nil {
		klog.V(2).Infof("No record exists for %s\n", srv)
		return
	}

	if newRec, ok := removeData(rec, domain); ok {
		d.addition(newRec)
	} else {
		klog.V(2).Infof("%s doesn't include  %s\n", srv, domain)
	}

	d.deletion(oldRec)
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

func removeData(rec *dns.ResourceRecordSet, data string) (*dns.ResourceRecordSet, bool) {
	newRec := *rec
	newRec.Rrdatas = []string{}

	for _, v := range rec.Rrdatas {
		if v != data {
			newRec.Rrdatas = append(newRec.Rrdatas, v)
			return &newRec, true
		}
	}
	return nil, false
}
