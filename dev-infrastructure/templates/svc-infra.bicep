@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resourcegroup for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('The location of the resourcegroup for the service keyvault')
param serviceKeyVaultLocation string = resourceGroup().location

@description('Soft delete setting for service keyvault')
param serviceKeyVaultSoftDelete bool = true

@description('If true, make the service keyvault private and only accessible by the svc cluster via private link.')
param serviceKeyVaultPrivate bool = true

// KV tagging
param serviceKeyVaultTagName string
param serviceKeyVaultTagValue string

@description('KV certificate officer principal ID')
param kvCertOfficerPrincipalId string

@description('MSI that will be used during pipeline runs')
param globalMSIId string

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId string = ''

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// service deployments running as the aroDevopsMsi need to lookup metadata about all kinds
// of resources, e.g. AKS metadata, database metadata, MI metadata, etc.
resource aroDevopsMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, globalMSIId, readerRoleId)
  properties: {
    principalId: reference(globalMSIId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
//   K E Y V A U L T S
//

// this is mostly a situation where multiple svc-infra pipelines run towards
// a shared svc keyvault resource group ${serviceKeyVaultResourceGroup}. while
// the individual modules will not conflict with each other, the existance
// of same-named deployments fails one pipeline.
var deploymentNameSuffix = uniqueString(resourceGroup().id)

module serviceKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'svc-kv-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    location: serviceKeyVaultLocation
    keyVaultName: serviceKeyVaultName
    private: serviceKeyVaultPrivate
    enableSoftDelete: serviceKeyVaultSoftDelete
    tagKey: serviceKeyVaultTagName
    tagValue: serviceKeyVaultTagValue
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
  }
}

module serviceKeyVaultCertOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'svc-kv-cert-officer-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Certificates Officer'
    managedIdentityPrincipalId: kvCertOfficerPrincipalId
  }
  dependsOn: [
    serviceKeyVault
  ]
}

module serviceKeyVaultSecretsOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'svc-kv-secret-officer-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets Officer'
    managedIdentityPrincipalId: kvCertOfficerPrincipalId
  }
  dependsOn: [
    serviceKeyVault
  ]
}

module serviceKeyVaultDevopsSecretsOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'svc-kv-devops-secret-officer-${deploymentNameSuffix}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets Officer'
    managedIdentityPrincipalId: reference(globalMSIId, '2023-01-31').principalId
  }
  dependsOn: [
    serviceKeyVault
  ]
}

output svcKeyVaultName string = serviceKeyVault.outputs.kvName
output svcKeyVaultUrl string = serviceKeyVault.outputs.kvUrl
