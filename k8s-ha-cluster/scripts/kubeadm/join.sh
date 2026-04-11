#!/bin/bash
set -e

# These values should be provided dynamically by Pulumi execution
JOIN_CMD=$1

echo "Joining HA Cluster as a secondary Control Plane..."
sudo $JOIN_CMD --control-plane

mkdir -p $HOME/.kube
sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
sudo chown $(id -u):$(id -g) $HOME/.kube/config

echo "Node joined successfully."
