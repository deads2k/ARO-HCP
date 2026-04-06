using '../templates/entra-app-lookup.bicep'

param applicationName = '{{ .geneva.actions.application.name }}'
param manage = {{ .geneva.actions.application.manage }}
