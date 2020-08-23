# PrivateDNS
Private DNS controller provides DNS records across Kubernetes clusters using cloud provider private DNS service. Useful for cases where you need DNS for pod-to-pod traffic between different clusters.

Currently only Google CloudDNS is supported.

Supported records:
- A record with a single pod IP
- SRV revords for service discovery
- A record with multiple pod IPs ("service record")
- PTR records for reverse lookup

Example DNS resource:
```
apiVersion: "tanelmae.com/v1"
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

This would create DNS records for pods with label "app=nats" in the `supernats` namespace. If it is on a cluster called "sauna" in europe-north1-a and [NATS](https://nats.io/) is run as statefulset called "nats-cluster":
- A records like `nats-0.nats-cluster.sauna.europe-north1-a.gcp.global`
- PTR record `<ip>.in-addr.arpa.` to allow resolving DNS addresses from IPs
- Service A record `nats-cluster.sauna.europe-north1-a.gcp.global`
- SRV record `_<port-name>._tcp.nats-cluster.sauna.europe-north1-a.gcp.global`


#### NOTE: this is work in progress

TODO:
- [ ] AWS Route53 support
- [ ] Azure support
- [ ] Multi-cloud setup support
- [ ] Multi-node deployment with leader election to improve reliability