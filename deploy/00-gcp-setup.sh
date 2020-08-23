#!/usr/bin/env bash

CURRENT_PROJECT=$(gcloud config get-value project)

# Name of the network DNS zones needs to be attached to
VPC_NAME="${1:-default}"
# DNS zone for A and SRV records
DNS_ZONE="k8s-dns"
# DNS zone for reverse lookup (PTR records)
DNS_REV_ZONE="k8s-reverse-dns"

# Service account the controller will use to manage the records
SA_NAME="private-dns"
FULL_SA="${SA_NAME}@${CURRENT_PROJECT}.iam.gserviceaccount.com"

echo "Current active project: ${CURRENT_PROJECT}"

echo "Will create following resources:"
echo "VPC: ${VPC_NAME}"
echo "DNS zone: ${DNS_ZONE}"
echo "DNS reverse lookup zone: ${DNS_REV_ZONE}"
echo "Service account: ${FULL_SA}"

echo "If you want to use existng VPC:"
echo "	./00-gcp-setup.sh <vpc-name>"
read -p "Press any key to continue or Ctrl+C to cancel"
set -e

# If VPC already exists it should be the one with clustes that will be running the controller.
# If new VPC is to be created Kubernetes cluster(s) should be created in it.
if [ -z "$(gcloud compute networks list --filter=name=${VPC_NAME} --format='value(name)')" ]; then
	gcloud compute networks create "${VPC_NAME}" --subnet-mode auto
	echo "NOTE: Make sure you create your cluster in ${VPC_NAME} network"
else
	echo "${VPC_NAME} network already exists"
fi

# Zone for A and SRV records
if [ -z "$(gcloud dns managed-zones list --filter=name=${DNS_ZONE} --format='value(name)')" ]; then
	gcloud dns managed-zones create "${DNS_ZONE}" \
		--dns-name="gcp.global." --description="Private DNS for Kubernetes pods" \
		--networks="${VPC_NAME}" --visibility=private
else
	echo "DNS zone ${DNS_ZONE} aready exists"
fi

# Zone for PTR records - reverse lookup
if [ -z "$(gcloud dns managed-zones list --filter=name=${DNS_REV_ZONE} --format='value(name)')" ]; then
	gcloud dns managed-zones create "${DNS_REV_ZONE}" \
		--dns-name="in-addr.arpa." --description="Private reverse lookup DNS for Kubernetes pods" \
		--networks="${VPC_NAME}" --visibility=private
else
	echo "DNS zone ${DNS_REV_ZONE} aready exists"
fi

# Service account to manage DNS zones
if [ -z "$(gcloud iam service-accounts list --filter=email=${FULL_SA} --format='value(email)')" ]; then
	gcloud iam service-accounts create ${SA_NAME} --display-name=${SA_NAME}
	gcloud projects add-iam-policy-binding ${CURRENT_PROJECT} \
		--member "serviceAccount:${FULL_SA}" --role roles/dns.admin

	gcloud iam service-accounts keys create \
		--iam-account ${FULL_SA} --key-file-type=json dns.json
	echo "Key file to be passed in as gcp-creds: dns.json"

	echo "To upload it as a secret to correct Kubernetes cluster and namespace:"
	echo "kubectl create secret generic dns-account --from-file=dns.json"
fi
