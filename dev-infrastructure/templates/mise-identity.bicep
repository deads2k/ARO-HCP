import { csvToArray } from '../modules/common.bicep'

param miseApplicationName string
param entraAppOwnerIds string
param genevaActionApplicationOwnerIds string
param miseApplicationDeploy bool

// TODO: remove genevaActionApplicationOwnerIds fallback once entraAppOwnerIds is set in sdp-pipelines config
var ownerIds = !empty(entraAppOwnerIds)
  ? entraAppOwnerIds
  : !empty(genevaActionApplicationOwnerIds)
      ? genevaActionApplicationOwnerIds
      : fail('At least one of entraAppOwnerIds or genevaActionApplicationOwnerIds must be provided')

extension microsoftGraphBeta

resource miseApp 'Microsoft.Graph/applications@beta' = if (miseApplicationDeploy) {
  displayName: miseApplicationName
  signInAudience: 'AzureADMyOrg'
  uniqueName: miseApplicationName
  requiredResourceAccess: []
  serviceManagementReference: 'b8e9ef87-cd63-4085-ab14-1c637806568c'
  api: {
    requestedAccessTokenVersion: 2
  }
  owners: {
    relationships: [for ownerId in csvToArray(ownerIds): ownerId]
  }
}
