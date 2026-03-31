#!/bin/bash
#
# Creates and configures service principals for the tenant-quota collector.
#
# This script manages the Azure AD service principals that the tenant-quota
# collector uses to query directory quotas (Graph API) and subscription
# quotas (ARM APIs) across multiple tenants.
#
# Each tenant needs its own service principal with:
# - Directory.Read.All (Graph API) for directory quota monitoring
# - Reader role on each target subscription for compute/network quota monitoring
#   and role assignment counting via Resource Graph
#
# The script is idempotent: it skips creation/assignment if already present.
#
# Prerequisites:
# - Azure CLI logged into the TARGET tenant where the SP should be created
# - Access to the opstool Key Vault (in the dev tenant) for storing secrets
#
# Usage:
#   ./scripts/manage-service-principals.sh --tenant redhat
#   ./scripts/manage-service-principals.sh --tenant test-tenant
#   ./scripts/manage-service-principals.sh --list
#
# After running, redeploy the collector to pick up any new secrets.
#

set -euo pipefail

KEYVAULT_NAME="${OPSTOOL_KEYVAULT_NAME:-opstool-kv-usw3}"

GRAPH_APP_ID="00000003-0000-0000-c000-000000000000"
# Directory.Read.All application permission
DIRECTORY_READ_ALL="7ab1d382-f21e-4acd-a863-ba3e13f7da61"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
print_success() { echo -e "${GREEN}[OK]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }
print_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
header()        { echo ""; echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"; echo -e "${BLUE}$1${NC}"; echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"; echo ""; }

# =============================================================================
# TENANT DEFINITIONS
# =============================================================================
# Each function defines the SP name, permissions, and target subscriptions
# for a specific tenant. Subscriptions are referenced by display name and
# resolved to IDs at runtime.

setup_redhat() {
    local APPLICATION_NAME="tenant-quota-collector-redhat0"
    local KEYVAULT_SECRET_NAME="custom-metrics-collector-redhat0-client-secret"
    local DIRECTORY_QUOTA=true
    local SUBSCRIPTIONS=(
        "ARO Hosted Control Planes (EA Subscription 1)"
        "ARO SRE Team - INT (EA Subscription 3)"
        "ARO HCP E2E"
    )

    create_or_get_sp "${APPLICATION_NAME}"
    grant_graph_permissions "${APP_ID}" "${DIRECTORY_QUOTA}"
    grant_subscription_reader "${APP_ID}" "${SUBSCRIPTIONS[@]}"
    store_secret "${APP_ID}" "${KEYVAULT_SECRET_NAME}"
}

setup_test_tenant() {
    local APPLICATION_NAME="tenant-quota-collector-test-tenant"
    local KEYVAULT_SECRET_NAME="custom-metrics-collector-test-tenant-client-secret"
    local DIRECTORY_QUOTA=false
    local SUBSCRIPTIONS=(
        "ARO HCP E2E"
        "ARO HCP E2E - Staging"
    )

    create_or_get_sp "${APPLICATION_NAME}"
    grant_graph_permissions "${APP_ID}" "${DIRECTORY_QUOTA}"
    grant_subscription_reader "${APP_ID}" "${SUBSCRIPTIONS[@]}"
    store_secret "${APP_ID}" "${KEYVAULT_SECRET_NAME}"
}

# =============================================================================
# HELPER FUNCTIONS
# =============================================================================

create_or_get_sp() {
    local app_name="$1"

    APP_ID=$(az ad app list --display-name "${app_name}" --query '[0].appId' -o tsv 2>/dev/null || true)

    if [[ -n "${APP_ID}" ]]; then
        print_info "Application '${app_name}' already exists: ${APP_ID}"
    else
        header "Creating application: ${app_name}"
        local sp_output
        sp_output=$(az ad sp create-for-rbac \
            --years 10 \
            --display-name "${app_name}" \
            -o json)

        APP_ID=$(echo "${sp_output}" | jq -r '.appId')
        print_success "Created service principal: ${APP_ID}"
    fi
}

grant_graph_permissions() {
    local app_id="$1"
    local directory_quota="$2"

    if [[ "${directory_quota}" != "true" ]]; then
        print_info "Skipping Graph API permissions (directoryQuota=false)"
        return 0
    fi

    header "Granting Graph API permissions"

    local existing
    existing=$(az ad app show --id "${app_id}" \
        --query "requiredResourceAccess[?resourceAppId=='${GRAPH_APP_ID}'].resourceAccess[].id" \
        -o tsv 2>/dev/null || true)

    if echo "${existing}" | grep -q "${DIRECTORY_READ_ALL}"; then
        print_info "Directory.Read.All already granted"
    else
        print_info "Adding Directory.Read.All permission..."
        az ad app permission add \
            --id "${app_id}" \
            --api "${GRAPH_APP_ID}" \
            --api-permissions "${DIRECTORY_READ_ALL}=Role"

        print_info "Requesting admin consent..."
        az ad app permission admin-consent --id "${app_id}"
        print_success "Graph API permissions granted"
    fi
}

grant_subscription_reader() {
    local app_id="$1"
    shift
    local subscriptions=("$@")

    header "Granting Reader role on subscriptions"

    for sub_name in "${subscriptions[@]}"; do
        local sub_id
        sub_id=$(az account list --query "[?name=='${sub_name}'].id" -o tsv 2>/dev/null || true)

        if [[ -z "${sub_id}" ]]; then
            print_warning "Could not find subscription '${sub_name}' — skipping"
            print_info "Make sure you have access to this subscription. You may need to:"
            echo "  az login --tenant <tenant-id>"
            continue
        fi

        print_info "Assigning Reader on '${sub_name}' (${sub_id})..."
        az role assignment create \
            --assignee "${app_id}" \
            --role "Reader" \
            --scope "/subscriptions/${sub_id}" 2>/dev/null \
            && print_success "Reader assigned" \
            || print_info "(already assigned)"
    done
}

store_secret() {
    local app_id="$1"
    local secret_name="$2"

    header "Checking Key Vault secret"

    local existing_secret
    existing_secret=$(az keyvault secret show \
        --vault-name "${KEYVAULT_NAME}" \
        --name "${secret_name}" \
        --query "value" -o tsv 2>/dev/null || true)

    if [[ -n "${existing_secret}" ]]; then
        print_info "Key Vault secret '${secret_name}' already exists"
        print_info "To rotate, use: ./scripts/renew-sp-secret.sh --tenant <name>"
        return 0
    fi

    print_info "Creating new client secret and storing in Key Vault..."
    local new_secret
    new_secret=$(az ad sp credential reset \
        --id "${app_id}" \
        --display-name "tenant-quota-collector-initial" \
        --years 1 \
        --query password \
        -o tsv)

    if [[ -z "${new_secret}" ]]; then
        print_error "Failed to create client secret"
        exit 1
    fi

    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "${secret_name}" \
        --value "${new_secret}" \
        --description "Created $(date +%Y-%m-%d) by manage-service-principals.sh" \
        > /dev/null

    print_success "Secret stored in Key Vault as '${secret_name}'"
}

list_tenants() {
    header "Available tenant configurations"
    echo "  redhat       - RedHat0 tenant (dev/int/e2e subscriptions)"
    echo "  test-tenant  - Test Tenant (stg/prod testing subscriptions)"
    echo ""
    echo "Usage:"
    echo "  $0 --tenant redhat"
    echo "  $0 --tenant test-tenant"
    echo ""
    echo "Prerequisites:"
    echo "  Login to the TARGET tenant before running:"
    echo "    RedHat0:      az login --tenant 64dc69e4-d083-49fc-9569-ebece1dd1408"
    echo "    Test Tenant:  az login --tenant 93b21e64-4824-439a-b893-46c9b2a51082"
    echo ""
    echo "  Then login to dev tenant to access Key Vault for storing secrets:"
    echo "    az login"
}

usage() {
    echo "Usage: $0 [--tenant NAME | --list | --help]"
    echo ""
    echo "Creates and configures service principals for the tenant-quota collector."
    echo ""
    echo "Options:"
    echo "  --tenant NAME    Setup SP for the named tenant (redhat, test-tenant)"
    echo "  --list           List available tenant configurations"
    echo "  --keyvault NAME  Override Key Vault name (default: ${KEYVAULT_NAME})"
    echo "  --help           Show this help message"
}

# =============================================================================
# MAIN
# =============================================================================

TENANT=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --tenant)
            TENANT="$2"
            shift 2
            ;;
        --keyvault)
            KEYVAULT_NAME="$2"
            shift 2
            ;;
        --list)
            list_tenants
            exit 0
            ;;
        --help)
            usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

if [[ -z "${TENANT}" ]]; then
    list_tenants
    exit 0
fi

case "${TENANT}" in
    redhat|RedHat0|redhat0)
        setup_redhat
        ;;
    test-tenant|TestTenant|test)
        setup_test_tenant
        ;;
    *)
        print_error "Unknown tenant: ${TENANT}"
        list_tenants
        exit 1
        ;;
esac

header "Done"
echo "Next steps:"
echo "  1. If this is a new tenant, add it to config/config-opstool.yaml"
echo "  2. Redeploy the collector:"
echo "     ./templatize-bin pipeline run \\"
echo "       --service-group Microsoft.Azure.ARO.HCP.Opstool.TenantQuota \\"
echo "       --topology-file topology-opstool.yaml \\"
echo "       --config-file config/config-opstool.yaml \\"
echo "       --dev-settings-file tooling/templatize/settings.yaml \\"
echo "       --dev-environment opstool \\"
echo "       --step deploy"
