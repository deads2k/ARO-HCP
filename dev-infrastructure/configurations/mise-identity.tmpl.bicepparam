using '../templates/mise-identity.bicep'

param miseApplicationName = '{{ .mise.applicationName }}'
param entraAppOwnerIds = '{{ .entraAppOwnerIds }}'
param genevaActionApplicationOwnerIds = '{{ .geneva.actions.application.ownerIds }}'
param miseApplicationDeploy = {{ .mise.deploy }}
