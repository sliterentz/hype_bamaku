#!/bin/bash
set -e

CP_IP_START=$1
VIP_DOMAIN=$2
VIP=$3
POD_CIDR=$4

echo "Initializing HA Kubernetes Control Plane at VIP: $VIP"

kubeadm init \
  --control-plane-endpoint "$CP_IP_START:6443" \
  --apiserver-cert-extra-sans="$VIP_DOMAIN,$VIP,$CP_IP_START" \
  --upload-certs \
  --pod-network-cidr="$POD_CIDR"

mkdir -p $HOME/.kube
sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
sudo chown $(id -u):$(id -g) $HOME/.kube/config

echo "Initialization Complete."
