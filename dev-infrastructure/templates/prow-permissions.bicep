targetScope = 'subscription'

@description('The principal ID of the Prow OpenShift Release Bot')
param prowPrincipalId string

var contributorRole = 'b24988ac-6180-42a0-ab88-20f7382dd24c'
var userAccessAdminRole = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'

// Privileged roles that must never be assigned
var ownerRole = '8e3af657-a8ff-443c-a75c-2fe8c4bcb635'
var rbacAdminRole = 'f58310d9-a9f6-439a-9e8d-f62e7b41a168'

resource contributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, prowPrincipalId, contributorRole)
  scope: subscription()
  properties: {
    principalId: prowPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', contributorRole)
  }
}

resource userAccessAdminRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, prowPrincipalId, userAccessAdminRole)
  scope: subscription()
  properties: {
    principalId: prowPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', userAccessAdminRole)
    condition: '((!(ActionMatches{\'Microsoft.Authorization/roleAssignments/write\'})) OR (@Request[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {${ownerRole}, ${userAccessAdminRole}, ${rbacAdminRole}})) AND ((!(ActionMatches{\'Microsoft.Authorization/roleAssignments/delete\'})) OR (@Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {${ownerRole}, ${userAccessAdminRole}, ${rbacAdminRole}}))'
    conditionVersion: '2.0'
  }
}
