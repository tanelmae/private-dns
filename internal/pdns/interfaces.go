package pdns

type DNSProvider interface {
	NewRequest() DNSRequest
}

type DNSRequest interface {
	AddRecord(domain, ip string)
	RemoveRecord(domain, ip string)
	AddReverseRecord(domain, ip string)
	RemoveReverseRecord(domain, ip string)
	AddToService(domain, ip string)
	RemoveFromService(domain, ip string)
	AddToSRV(srv, domain string, priority int)
	RemoveFromSRV(srv, domain string)
	Do() error
}
