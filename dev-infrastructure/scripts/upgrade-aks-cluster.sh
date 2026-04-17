#!/bin/bash
set -euo pipefail

# Inputs via environment variables:
#   RESOURCE_GROUP - Resource group containing the cluster
#   CLUSTER_NAME   - AKS cluster name
#   KUBERNETES_VERSION - Kubernetes Version

version_greater_than() {
    [ "$(printf '%s\n' "$1" "$2" | sort -V | head -n1)" != "$1" ]
}

echo "Checking if cluster '${CLUSTER_NAME}' in RG '${RESOURCE_GROUP}' needs upgrade to '${KUBERNETES_VERSION}'..."

# Get current control plane version
CURRENT_CP_VERSION=$(az aks show \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --query currentKubernetesVersion \
    --output tsv)

echo "Current control plane version: ${CURRENT_CP_VERSION}"
echo "Target version: ${KUBERNETES_VERSION}"

# Check if control plane needs upgrade
NEEDS_UPGRADE=false
if version_greater_than "${KUBERNETES_VERSION}" "${CURRENT_CP_VERSION}"; then
    echo "Control plane needs upgrade from ${CURRENT_CP_VERSION} to ${KUBERNETES_VERSION}"
    NEEDS_UPGRADE=true
else
    echo "Control plane is at ${CURRENT_CP_VERSION}, target is ${KUBERNETES_VERSION} - no upgrade needed"
fi

# Get node pool versions and check if any need upgrade
echo "Checking node pool versions..."
NODE_POOLS=$(az aks nodepool list \
    --resource-group "${RESOURCE_GROUP}" \
    --cluster-name "${CLUSTER_NAME}" \
    --query '[].{name:name,version:orchestratorVersion}' \
    --output tsv)

if [ -n "${NODE_POOLS}" ]; then
    while IFS=$'\t' read -r POOL_NAME POOL_VERSION; do
        echo "  Node pool '${POOL_NAME}': ${POOL_VERSION}"

        if version_greater_than "${KUBERNETES_VERSION}" "${POOL_VERSION}"; then
            echo "  Node pool '${POOL_NAME}' needs upgrade from ${POOL_VERSION} to ${KUBERNETES_VERSION}"
            NEEDS_UPGRADE=true
        else
            echo "  Node pool '${POOL_NAME}' is at ${POOL_VERSION}, target is ${KUBERNETES_VERSION} - no upgrade needed"
        fi
    done <<< "${NODE_POOLS}"
fi

if [ "${NEEDS_UPGRADE}" = "false" ]; then
    echo "Cluster does not need upgrade - all components are at or above target version ${KUBERNETES_VERSION}."
    exit 0
fi

echo "Upgrading cluster '${CLUSTER_NAME}' in RG '${RESOURCE_GROUP}' to '${KUBERNETES_VERSION}'..."

az aks upgrade \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --kubernetes-version "${KUBERNETES_VERSION}" \
    --yes

echo "Waiting for upgrade to complete..."
az aks wait \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --updated \
    --timeout 3600

echo "Upgrade completed successfully."

