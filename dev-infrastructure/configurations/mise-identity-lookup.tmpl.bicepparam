using '../templates/entra-app-lookup.bicep'

param applicationName = '{{ .mise.applicationName }}'
param manage = {{ .mise.deploy }}
