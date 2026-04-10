#!/bin/bash
set -euo pipefail

# Inputs via environment variables:
#   CLUSTER_NAME   - AKS cluster name
#   KUBERNETES_VERSION - Kubernetes Version
#   RESOURCE_GROUP - Resource group containing the cluster

echo " Upgrading cluster '${CLUSTER_NAME}' in RG '${RESOURCE_GROUP}' to '${KUBERNETES_VERSION}'..."

az aks upgrade \
    --resource-group ${CLUSTER_NAME} \
    --name ${RESOURCE_GROUP} \
    --kubernetes-version ${KUBERNETES_VERSION} \
    --yes

