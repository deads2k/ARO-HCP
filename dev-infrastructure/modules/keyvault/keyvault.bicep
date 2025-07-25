@description('Location of the keyvault.')
param location string

@description('Name of the key vault.')
param keyVaultName string

@description('Toggle to enable soft delete.')
param enableSoftDelete bool

@description('Toggle to make the keyvault private.')
param private bool

@description('Tag key for the keyvault.')
param tagKey string

@description('Tag value for the keyvault.')
param tagValue string

@description('Log Analytics Workspace ID if logging to Log Analytics')
param logAnalyticsWorkspaceId string = ''

resource keyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' = {
  location: location
  name: keyVaultName
  tags: {
    resourceGroup: resourceGroup().name
    '${tagKey}': tagValue
  }
  properties: {
    enableRbacAuthorization: true
    enabledForDeployment: false
    enabledForDiskEncryption: false
    enabledForTemplateDeployment: false
    enableSoftDelete: enableSoftDelete
    publicNetworkAccess: private ? 'Disabled' : 'Enabled'
    sku: {
      name: 'standard'
      family: 'A'
    }
    tenantId: subscription().tenantId
  }
}

resource keyVaultDiagnosticSettings 'Microsoft.Insights/diagnosticSettings@2017-05-01-preview' = if (logAnalyticsWorkspaceId != '') {
  scope: keyVault
  name: keyVaultName
  properties: {
    logs: [
      {
        category: 'AuditEvent'
        enabled: true
      }
      {
        category: 'AzurePolicyEvaluationDetails'
        enabled: true
      }
    ]
    workspaceId: logAnalyticsWorkspaceId
  }
}

output kvId string = keyVault.id

output kvName string = keyVault.name

output kvUrl string = keyVault.properties.vaultUri
