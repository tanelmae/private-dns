package pdns

type DNSProvider interface {
	NewRequest() DNSRequest
}

type DNSRequest interface {
	CreateRecord(domain, ip string)
	DeleteRecord(domain, ip string)
	AddToService(domain, ip string)
	RemoveFromService(domain, ip string)
	AddToSRV(srv, domain string, priority int)
	RemoveFromSRV(srv, domain string)
	Do() error
}
