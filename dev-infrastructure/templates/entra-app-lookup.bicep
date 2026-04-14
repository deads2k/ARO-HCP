param applicationName string
param manage bool

extension microsoftGraphBeta

resource entraApp 'Microsoft.Graph/applications@beta' existing = if (manage) {
  uniqueName: applicationName
}

output appId string = manage ? entraApp.appId : ''
output tenantId string = tenant().tenantId
