// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// errorInjectingDBClientForNodePoolReadPersist wraps MockDBClient to return error-injecting CRUDs.
type errorInjectingDBClientForNodePoolReadPersist struct {
	*databasetesting.MockDBClient
	spnpCRUD database.ServiceProviderNodePoolCRUD
}

func (e *errorInjectingDBClientForNodePoolReadPersist) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ServiceProviderNodePoolCRUD {
	if e.spnpCRUD != nil {
		return e.spnpCRUD
	}
	return e.MockDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName)
}

var _ database.DBClient = &errorInjectingDBClientForNodePoolReadPersist{}

// errorInjectingSPNPCRUD wraps ServiceProviderNodePoolCRUD to allow error injection.
type errorInjectingSPNPCRUD struct {
	database.ServiceProviderNodePoolCRUD
	getErr error
}

func (e *errorInjectingSPNPCRUD) Get(ctx context.Context, resourceID string) (*api.ServiceProviderNodePool, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.ServiceProviderNodePoolCRUD.Get(ctx, resourceID)
}

// errorInjectingMCCCRUDForNodePool wraps ManagementClusterContentCRUD to allow error injection.
type errorInjectingMCCCRUDForNodePool struct {
	database.ManagementClusterContentCRUD
	getResult  *api.ManagementClusterContent
	getErr     error
	replaceErr error
}

func (e *errorInjectingMCCCRUDForNodePool) Get(ctx context.Context, resourceID string) (*api.ManagementClusterContent, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	if e.getResult != nil {
		return e.getResult, nil
	}
	return e.ManagementClusterContentCRUD.Get(ctx, resourceID)
}

func (e *errorInjectingMCCCRUDForNodePool) Replace(ctx context.Context, obj *api.ManagementClusterContent, opts *azcosmos.ItemOptions) (*api.ManagementClusterContent, error) {
	if e.replaceErr != nil {
		return nil, e.replaceErr
	}
	return e.ManagementClusterContentCRUD.Replace(ctx, obj, opts)
}

var _ database.ManagementClusterContentCRUD = &errorInjectingMCCCRUDForNodePool{}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_calculateManagementClusterContentFromMaestroBundle(t *testing.T) {
	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
	}
	ref := &api.MaestroBundleReference{
		Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
		MaestroAPIMaestroBundleName: "bundle-name",
	}

	np := hsv1beta1.NodePool{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "NodePool"}, ObjectMeta: metav1.ObjectMeta{Name: "np1", Namespace: "ns1"}}
	npJSONBytes, err := json.Marshal(np)
	require.NoError(t, err)
	validNPJSON := string(npJSONBytes)

	tests := []struct {
		name            string
		maestroGet      func(*maestro.MockClient)
		wantDegraded    bool
		wantKubeContent bool
		wantErr         bool
		errSub          string
	}{
		{
			name: "bundle not found - desired with degraded condition",
			maestroGet: func(m *maestro.MockClient) {
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "bundle-name"))
			},
			wantDegraded:    true,
			wantKubeContent: false,
		},
		{
			name: "maestro bundle get error - returns error",
			maestroGet: func(m *maestro.MockClient) {
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(nil, fmt.Errorf("connection error"))
			},
			wantErr: true,
			errSub:  "failed to get Maestro Bundle",
		},
		{
			name: "bundle has invalid status feedback - desired with degraded",
			maestroGet: func(m *maestro.MockClient) {
				b := &workv1.ManifestWork{
					Status: workv1.ManifestWorkStatus{
						ResourceStatus: workv1.ManifestResourceStatus{
							Manifests: []workv1.ManifestCondition{
								{ResourceMeta: workv1.ManifestResourceMeta{}, StatusFeedbacks: workv1.StatusFeedbackResult{Values: []workv1.FeedbackValue{}}, Conditions: []metav1.Condition{}},
							},
						},
					},
				}
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)
			},
			wantDegraded:    true,
			wantKubeContent: false,
		},
		{
			name: "success - desired with kube content",
			maestroGet: func(m *maestro.MockClient) {
				b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)
			},
			wantDegraded:    false,
			wantKubeContent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := maestro.NewMockClient(ctrl)
			tt.maestroGet(mockMaestro)

			got, err := calculateManagementClusterContentFromMaestroBundle(context.Background(), nodepool.ID, ref, mockMaestro)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSub)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, tt.wantKubeContent, got.Status.KubeContent != nil && len(got.Status.KubeContent.Items) > 0)
				hasDegradedTrue := controllerutils.IsConditionTrue(got.Status.Conditions, "Degraded")
				assert.Equal(t, tt.wantDegraded, hasDegradedTrue)
			}
		})
	}
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_readAndPersistMaestroBundleContent(t *testing.T) {
	ctx := context.Background()
	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
	}
	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	ref := &api.MaestroBundleReference{
		Name:                        bundleInternalName,
		MaestroAPIMaestroBundleName: "bundle-name",
	}

	np := hsv1beta1.NodePool{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "NodePool"}, ObjectMeta: metav1.ObjectMeta{Name: "np1", Namespace: "ns1"}}
	npJSONBytes, err := json.Marshal(np)
	require.NoError(t, err)
	validNPJSON := string(npJSONBytes)

	// Node-pool–scoped ManagementClusterContents are nested under the node pool ARM resource.
	const mccContainerSub = "sub"
	const mccContainerRG = "rg"
	const mccContainerCluster = "cluster"
	const mccNodePoolName = "test-nodepool"
	existingMCCResourceIDStr := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/nodePools/%s/managementClusterContents/%s",
		mccContainerSub, mccContainerRG, mccContainerCluster, mccNodePoolName, bundleInternalName)

	t.Run("creates new ManagementClusterContent when not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.HCPClusters(mccContainerSub, mccContainerRG).NodePools(mccContainerCluster).ManagementClusterContents(mccNodePoolName)

		err := readAndPersistMaestroReadonlyBundleContent(ctx, nodepool.ID, ref, mockMaestro, mccCRUD)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(bundleInternalName))
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("replaces existing when content changed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.HCPClusters(mccContainerSub, mccContainerRG).NodePools(mccContainerCluster).ManagementClusterContents(mccNodePoolName)
		existingRID := api.Must(azcorearm.ParseResourceID(existingMCCResourceIDStr))
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{}}},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, nodepool.ID, ref, mockMaestro, mccCRUD)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(bundleInternalName))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("keeps existing kube content when desired has no content (degraded)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		// Bundle with no valid status feedback -> desired has no KubeContent
		b := &workv1.ManifestWork{
			Status: workv1.ManifestWorkStatus{
				ResourceStatus: workv1.ManifestResourceStatus{
					Manifests: []workv1.ManifestCondition{
						{ResourceMeta: workv1.ManifestResourceMeta{}, StatusFeedbacks: workv1.StatusFeedbackResult{Values: []workv1.FeedbackValue{}}, Conditions: []metav1.Condition{}},
					},
				},
			},
		}
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.HCPClusters(mccContainerSub, mccContainerRG).NodePools(mccContainerCluster).ManagementClusterContents(mccNodePoolName)
		existingRID := api.Must(azcorearm.ParseResourceID(existingMCCResourceIDStr))
		existingContent := &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(`{}`)}}}
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: existingContent},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, nodepool.ID, ref, mockMaestro, mccCRUD)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(bundleInternalName))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		assert.Equal(t, existingContent.Items[0].Raw, got.Status.KubeContent.Items[0].Raw)
	})

	t.Run("no replace occurs when content has not changed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil).Times(2)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.HCPClusters(mccContainerSub, mccContainerRG).NodePools(mccContainerCluster).ManagementClusterContents(mccNodePoolName)
		existingRID := api.Must(azcorearm.ParseResourceID(existingMCCResourceIDStr))
		desired, err := calculateManagementClusterContentFromMaestroBundle(ctx, nodepool.ID, ref, mockMaestro)
		require.NoError(t, err)
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         desired.Status,
		}
		_, err = mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, nodepool.ID, ref, mockMaestro, mccCRUD)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(bundleInternalName))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("error occurs when object has been modified in Cosmos since we retrieved it", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		existingRID := api.Must(azcorearm.ParseResourceID(existingMCCResourceIDStr))
		existingDoc := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(validNPJSON)}}}},
		}

		mockDB := databasetesting.NewMockDBClient()
		baseMccCRUD := mockDB.HCPClusters(mccContainerSub, mccContainerRG).NodePools(mccContainerCluster).ManagementClusterContents(mccNodePoolName)
		mccCRUD := &errorInjectingMCCCRUDForNodePool{
			ManagementClusterContentCRUD: baseMccCRUD,
			getResult:                    existingDoc,
			replaceErr:                   databasetesting.NewPreconditionFailedError(),
		}

		err := readAndPersistMaestroReadonlyBundleContent(ctx, nodepool.ID, ref, mockMaestro, mccCRUD)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to replace ManagementClusterContent")
		assert.True(t, database.IsResponseError(err, http.StatusPreconditionFailed), "expected 412 Precondition Failed")
	})

	t.Run("error occurs when managementClusterContentsDBClient.Get fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		baseMccCRUD := mockDB.HCPClusters(mccContainerSub, mccContainerRG).NodePools(mccContainerCluster).ManagementClusterContents(mccNodePoolName)
		mccCRUD := &errorInjectingMCCCRUDForNodePool{
			ManagementClusterContentCRUD: baseMccCRUD,
			getErr:                       fmt.Errorf("cosmos connection error"),
		}

		err := readAndPersistMaestroReadonlyBundleContent(ctx, nodepool.ID, ref, mockMaestro, mccCRUD)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get ManagementClusterContent")
		assert.Contains(t, err.Error(), "cosmos connection error")
	})
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NodePoolNotFound(t *testing.T) {
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	// No nodepool in DB -> Get returns NotFound -> SyncOnce returns nil (no work to do)
	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetServiceProviderNodePoolError(t *testing.T) {
	ctx := context.Background()

	baseMockDB := databasetesting.NewMockDBClient()

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := baseMockDB.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	expectedError := fmt.Errorf("database error")
	mockDBClient := &errorInjectingDBClientForNodePoolReadPersist{
		MockDBClient: baseMockDB,
		spnpCRUD: &errorInjectingSPNPCRUD{
			getErr: expectedError,
		},
	}

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderNodePool")
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NoMaestroReadonlyBundlesRefs(t *testing.T) {
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	// SPNP with no bundle references -> SyncOnce returns nil (nothing to process)
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
	}
	spnpCRUD := mockDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetProvisionShardError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		cosmosClient:         mockDBClient,
		clusterServiceClient: mockClusterService,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: bundleInternalName, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spnpCRUD := mockDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(nil, fmt.Errorf("provision shard error"))

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get Cluster Provision Shard")
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ReadAndPersistFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroClientBuilder:               mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier: "test-env",
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftNodePool, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spnpCRUD := mockDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	validNPJSON := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"np1","namespace":"ns1"}}`
	bundle := buildTestMaestroBundleWithStatusFeedback("bundle-name", "test-consumer", validNPJSON)
	mockMaestroClient.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(bundle, nil)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	mccCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).ManagementClusterContents(key.HCPNodePoolName)
	got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Status.KubeContent)
	require.Len(t, got.Status.KubeContent.Items, 1)

	var u unstructured.Unstructured
	err = json.Unmarshal(got.Status.KubeContent.Items[0].Raw, &u)
	require.NoError(t, err)
	assert.Equal(t, "NodePool", u.GetKind())
	assert.Equal(t, "np1", u.GetName())
}
