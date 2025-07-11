// Code generated by Microsoft (R) AutoRest Code Generator (autorest: 3.10.8, generator: @autorest/go@4.0.0-preview.72)
// Changes may cause incorrect behavior and will be lost if the code is regenerated.
// Code generated by @autorest/go. DO NOT EDIT.

package generated

// ExternalAuthsClientCreateOrUpdateResponse contains the response from method ExternalAuthsClient.BeginCreateOrUpdate.
type ExternalAuthsClientCreateOrUpdateResponse struct {
	// ExternalAuth resource
	ExternalAuth
}

// ExternalAuthsClientDeleteResponse contains the response from method ExternalAuthsClient.BeginDelete.
type ExternalAuthsClientDeleteResponse struct {
	// placeholder for future response values
}

// ExternalAuthsClientGetResponse contains the response from method ExternalAuthsClient.Get.
type ExternalAuthsClientGetResponse struct {
	// ExternalAuth resource
	ExternalAuth
}

// ExternalAuthsClientListByParentResponse contains the response from method ExternalAuthsClient.NewListByParentPager.
type ExternalAuthsClientListByParentResponse struct {
	// The response of a ExternalAuth list operation.
	ExternalAuthListResult
}

// ExternalAuthsClientUpdateResponse contains the response from method ExternalAuthsClient.BeginUpdate.
type ExternalAuthsClientUpdateResponse struct {
	// ExternalAuth resource
	ExternalAuth
}

// HcpOpenShiftClustersClientCreateOrUpdateResponse contains the response from method HcpOpenShiftClustersClient.BeginCreateOrUpdate.
type HcpOpenShiftClustersClientCreateOrUpdateResponse struct {
	// HCP cluster resource
	HcpOpenShiftCluster
}

// HcpOpenShiftClustersClientDeleteResponse contains the response from method HcpOpenShiftClustersClient.BeginDelete.
type HcpOpenShiftClustersClientDeleteResponse struct {
	// placeholder for future response values
}

// HcpOpenShiftClustersClientGetResponse contains the response from method HcpOpenShiftClustersClient.Get.
type HcpOpenShiftClustersClientGetResponse struct {
	// HCP cluster resource
	HcpOpenShiftCluster
}

// HcpOpenShiftClustersClientListByResourceGroupResponse contains the response from method HcpOpenShiftClustersClient.NewListByResourceGroupPager.
type HcpOpenShiftClustersClientListByResourceGroupResponse struct {
	// The response of a HcpOpenShiftCluster list operation.
	HcpOpenShiftClusterListResult
}

// HcpOpenShiftClustersClientListBySubscriptionResponse contains the response from method HcpOpenShiftClustersClient.NewListBySubscriptionPager.
type HcpOpenShiftClustersClientListBySubscriptionResponse struct {
	// The response of a HcpOpenShiftCluster list operation.
	HcpOpenShiftClusterListResult
}

// HcpOpenShiftClustersClientRequestAdminCredentialResponse contains the response from method HcpOpenShiftClustersClient.BeginRequestAdminCredential.
type HcpOpenShiftClustersClientRequestAdminCredentialResponse struct {
	// HCP cluster admin credential
	HcpOpenShiftClusterAdminCredential
}

// HcpOpenShiftClustersClientRevokeCredentialsResponse contains the response from method HcpOpenShiftClustersClient.BeginRevokeCredentials.
type HcpOpenShiftClustersClientRevokeCredentialsResponse struct {
	// placeholder for future response values
}

// HcpOpenShiftClustersClientUpdateResponse contains the response from method HcpOpenShiftClustersClient.BeginUpdate.
type HcpOpenShiftClustersClientUpdateResponse struct {
	// HCP cluster resource
	HcpOpenShiftCluster
}

// HcpOpenShiftVersionsClientGetResponse contains the response from method HcpOpenShiftVersionsClient.Get.
type HcpOpenShiftVersionsClientGetResponse struct {
	// HcpOpenShiftVersion represents a location based available HCP OpenShift version
	HcpOpenShiftVersion
}

// HcpOpenShiftVersionsClientListResponse contains the response from method HcpOpenShiftVersionsClient.NewListPager.
type HcpOpenShiftVersionsClientListResponse struct {
	// The response of a HcpOpenShiftVersion list operation.
	HcpOpenShiftVersionListResult
}

// HcpOperatorIdentityRoleSetsClientGetResponse contains the response from method HcpOperatorIdentityRoleSetsClient.Get.
type HcpOperatorIdentityRoleSetsClientGetResponse struct {
	// HcpOperatorIdentityRoles represents a location based representation of
	// the required platform workload identities and their required roles for a given
	// OpenShift version
	HcpOperatorIdentityRoleSet
}

// HcpOperatorIdentityRoleSetsClientListResponse contains the response from method HcpOperatorIdentityRoleSetsClient.NewListPager.
type HcpOperatorIdentityRoleSetsClientListResponse struct {
	// The response of a HcpOperatorIdentityRoleSet list operation.
	HcpOperatorIdentityRoleSetListResult
}

// NodePoolsClientCreateOrUpdateResponse contains the response from method NodePoolsClient.BeginCreateOrUpdate.
type NodePoolsClientCreateOrUpdateResponse struct {
	// Concrete tracked resource types can be created by aliasing this type using a specific property type.
	NodePool
}

// NodePoolsClientDeleteResponse contains the response from method NodePoolsClient.BeginDelete.
type NodePoolsClientDeleteResponse struct {
	// placeholder for future response values
}

// NodePoolsClientGetResponse contains the response from method NodePoolsClient.Get.
type NodePoolsClientGetResponse struct {
	// Concrete tracked resource types can be created by aliasing this type using a specific property type.
	NodePool
}

// NodePoolsClientListByParentResponse contains the response from method NodePoolsClient.NewListByParentPager.
type NodePoolsClientListByParentResponse struct {
	// The response of a NodePool list operation.
	NodePoolListResult
}

// NodePoolsClientUpdateResponse contains the response from method NodePoolsClient.BeginUpdate.
type NodePoolsClientUpdateResponse struct {
	// Concrete tracked resource types can be created by aliasing this type using a specific property type.
	NodePool
}

// OperationsClientListResponse contains the response from method OperationsClient.NewListPager.
type OperationsClientListResponse struct {
	// A list of REST API operations supported by an Azure Resource Provider. It contains an URL link to get the next set of results.
	OperationListResult
}
