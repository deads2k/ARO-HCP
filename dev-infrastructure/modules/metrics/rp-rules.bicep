// Alerts and Recording Rules reserved for future ARO-RP-owned user journey alerts.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

param actionGroups array

module generatedAlerts 'rules/generatedRPPrometheusAlertingRules.bicep' = {
  name: 'generatedRPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
  }
}
