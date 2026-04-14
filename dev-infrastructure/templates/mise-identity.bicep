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

module miseApp '../modules/entra/app.bicep' = if (miseApplicationDeploy) {
  name: 'mise-entra-app'
  params: {
    applicationName: miseApplicationName
    ownerIds: ownerIds
    manageSp: false
    serviceManagementReference: 'b8e9ef87-cd63-4085-ab14-1c637806568c'
    isFallbackPublicClient: false
    requestedAccessTokenVersion: 2
  }
}
