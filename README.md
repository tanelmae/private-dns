# PrivateDNS
Private DNS controller provides DNS records across Kubernetes clusters. 

Supported records:
- A record with a single pod IP
- SRV revords
- A record with multiple pod IPs (service record)
- PTR records for reverse lookup

Currently only Google CloudDNS is supported. The plan is to add support for both AWS and Azure and solution for multicloud deployments.

Example DNS resource:
```
apiVersion: "tanelmae.github.com/v1"
kind: PrivateDNS
metadata:
  name: nats
  namespace: supernats
spec:
  label: app=nats
  domain: gcp.global
  subdomain: true
  service: true
  srv-port: route
  srv-protocol: tcp
```

This would create DNS records for pods with label "app=nats" in the supernats namespace. If it is on a cluster called "sauna" in europe-north1-a and [NATS](https://nats.io/) is run as statefulset called "nats-cluster":
- A records like `nats-0.nats-cluster.sauna.europe-north1-a.gcp.global`
- PTR record `<ip>.in-addr.arpa.` to allow resolving DNS addresses from IPs
- Service A record `nats-cluster.sauna.europe-north1-a.gcp.global`
- SRV record `_<port-name>._tcp.nats-cluster.sauna.europe-north1-a.gcp.global`


#### NOTE: this is work in progress, wouldn't even called it alpha yet