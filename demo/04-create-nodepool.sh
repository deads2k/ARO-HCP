#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source env_vars
source "$(dirname "$0")"/common.sh

NODEPOOL_TMPL_FILE="node_pool.tmpl.json"
NODEPOOL_FILE="node_pool.json"

SUBNET_ID=$(az network vnet subnet show -g ${CUSTOMER_RG_NAME} --vnet-name ${CUSTOMER_VNET_NAME} --name ${CUSTOMER_VNET_SUBNET1} --query id -o tsv)

jq \
  --arg location "$LOCATION" \
  --arg subnet_id "$SUBNET_ID" \
  '
    .location = $location |
    .properties.platform.subnetId = $subnet_id
  ' "${NODEPOOL_TMPL_FILE}" > ${NODEPOOL_FILE}

(arm_system_data_header; correlation_headers) | curl -sSi -X PUT "localhost:8443/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${CUSTOMER_RG_NAME}/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/${CLUSTER_NAME}/nodePools/${NP_NAME}?api-version=2024-06-10-preview" \
  --header @- \
  --json @${NODEPOOL_FILE}
