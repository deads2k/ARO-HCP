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

// errorInjectingMCCCRUD wraps ManagementClusterContentCRUD to allow error injection for testing.
type errorInjectingMCCCRUD struct {
	database.ManagementClusterContentCRUD
	getResult  *api.ManagementClusterContent
	getErr     error
	replaceErr error
}

func (e *errorInjectingMCCCRUD) Get(ctx context.Context, resourceID string) (*api.ManagementClusterContent, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	if e.getResult != nil {
		return e.getResult, nil
	}
	return e.ManagementClusterContentCRUD.Get(ctx, resourceID)
}

func (e *errorInjectingMCCCRUD) Replace(ctx context.Context, obj *api.ManagementClusterContent, opts *azcosmos.ItemOptions) (*api.ManagementClusterContent, error) {
	if e.replaceErr != nil {
		return nil, e.replaceErr
	}
	return e.ManagementClusterContentCRUD.Replace(ctx, obj, opts)
}

var _ database.ManagementClusterContentCRUD = &errorInjectingMCCCRUD{}

// errorInjectingDBClient wraps MockDBClient to return error-injecting CRUDs.
type errorInjectingDBClient struct {
	*databasetesting.MockDBClient
	mccCRUD      database.ManagementClusterContentCRUD
	clustersCRUD database.HCPClusterCRUD
	spcCRUD      database.ServiceProviderClusterCRUD
}

func (e *errorInjectingDBClient) ManagementClusterContents(subscriptionID, resourceGroupName, clusterName string) database.ManagementClusterContentCRUD {
	if e.mccCRUD != nil {
		return e.mccCRUD
	}
	return e.MockDBClient.ManagementClusterContents(subscriptionID, resourceGroupName, clusterName)
}

func (e *errorInjectingDBClient) HCPClusters(subscriptionID, resourceGroupName string) database.HCPClusterCRUD {
	if e.clustersCRUD != nil {
		return e.clustersCRUD
	}
	return e.MockDBClient.HCPClusters(subscriptionID, resourceGroupName)
}

func (e *errorInjectingDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) database.ServiceProviderClusterCRUD {
	if e.spcCRUD != nil {
		return e.spcCRUD
	}
	return e.MockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName)
}

var _ database.DBClient = &errorInjectingDBClient{}

// errorInjectingHCPClusterCRUD wraps HCPClusterCRUD to allow error injection.
type errorInjectingHCPClusterCRUD struct {
	database.HCPClusterCRUD
	getResult *api.HCPOpenShiftCluster
	getErr    error
}

func (e *errorInjectingHCPClusterCRUD) Get(ctx context.Context, resourceID string) (*api.HCPOpenShiftCluster, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	if e.getResult != nil {
		return e.getResult, nil
	}
	return e.HCPClusterCRUD.Get(ctx, resourceID)
}

var _ database.HCPClusterCRUD = &errorInjectingHCPClusterCRUD{}

// errorInjectingSPCCRUD wraps ServiceProviderClusterCRUD to allow error injection.
type errorInjectingSPCCRUD struct {
	database.ServiceProviderClusterCRUD
	getErr error
}

func (e *errorInjectingSPCCRUD) Get(ctx context.Context, resourceID string) (*api.ServiceProviderCluster, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.ServiceProviderClusterCRUD.Get(ctx, resourceID)
}

var _ database.ServiceProviderClusterCRUD = &errorInjectingSPCCRUD{}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_calculateManagementClusterContentFromMaestroBundle(t *testing.T) {
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
	}
	ref := &api.MaestroBundleReference{
		Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
		MaestroAPIMaestroBundleName: "bundle-name",
	}

	hc := hsv1beta1.HostedCluster{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "HostedCluster"}, ObjectMeta: metav1.ObjectMeta{Name: "hc1", Namespace: "ns1"}}
	hcJSONBytes, err := json.Marshal(hc)
	require.NoError(t, err)
	validHCJSON := string(hcJSONBytes)

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
			name: "maestro api maestro bundleget error - returns error",
			maestroGet: func(m *maestro.MockClient) {
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(nil, fmt.Errorf("connection error"))
			},
			wantErr: true,
			errSub:  "failed to get Maestro Bundle",
		},
		{
			name: "bundle has invalid status feedback - desired with degraded",
			maestroGet: func(m *maestro.MockClient) {
				// Bundle with no status feedback values
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
				b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
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

			got, err := calculateManagementClusterContentFromMaestroBundle(context.Background(), cluster.ID, ref, mockMaestro)
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

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_readAndPersistMaestroBundleContent(t *testing.T) {
	ctx := context.Background()
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
	}
	ref := &api.MaestroBundleReference{
		Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
		MaestroAPIMaestroBundleName: "bundle-name",
	}
	hc := hsv1beta1.HostedCluster{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "HostedCluster"}, ObjectMeta: metav1.ObjectMeta{Name: "hc1", Namespace: "ns1"}}
	hcJSONBytes, err := json.Marshal(hc)
	require.NoError(t, err)
	validHCJSON := string(hcJSONBytes)

	t.Run("creates new ManagementClusterContent when not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.ManagementClusterContents("sub", "rg", "cluster")

		err := readAndPersistMaestroReadonlyBundleContent(ctx, mockDB, cluster.ID, ref, mockMaestro)
		require.NoError(t, err)

		// Content should have been created (name = bundle internal name)
		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("replaces existing when content changed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.ManagementClusterContents("sub", "rg", "cluster")
		// Pre-create existing content with different payload
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{}}},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, mockDB, cluster.ID, ref, mockMaestro)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("keeps existing kube content when desired has no content (degraded)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		// Return bundle that has no valid status feedback so desired has no KubeContent
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
		mccCRUD := mockDB.ManagementClusterContents("sub", "rg", "cluster")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		existingContent := &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(`{}`)}}}
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: existingContent},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, mockDB, cluster.ID, ref, mockMaestro)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		// Should have kept existing content
		require.NotNil(t, got.Status.KubeContent)
		assert.Equal(t, existingContent.Items[0].Raw, got.Status.KubeContent.Items[0].Raw)
	})

	t.Run("no replace occurs when content has not changed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		// Get is called once when building desired for pre-create, and once inside readAndPersistMaestroReadonlyBundleContent.
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil).Times(2)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.ManagementClusterContents("sub", "rg", "cluster")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		// Pre-create content that matches exactly what the syncer would compute (same KubeContent and Degraded=False condition)
		// so that DeepEqual(existing, desired) is true and Replace is not called.
		desired, err := calculateManagementClusterContentFromMaestroBundle(ctx, cluster.ID, ref, mockMaestro)
		require.NoError(t, err)
		require.NotNil(t, desired)
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         desired.Status,
		}
		_, err = mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, mockDB, cluster.ID, ref, mockMaestro)
		require.NoError(t, err)

		// Document should still exist with same content (no Replace was needed)
		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("error occurs when object has been modified in Cosmos since we retrieved it", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		existingDoc := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(validHCJSON)}}}},
		}

		// Use error-injecting wrapper to simulate 412 Precondition Failed on Replace
		mockDB := &errorInjectingDBClient{
			MockDBClient: databasetesting.NewMockDBClient(),
			mccCRUD: &errorInjectingMCCCRUD{
				getResult:  existingDoc,
				replaceErr: databasetesting.NewPreconditionFailedError(),
			},
		}

		err := readAndPersistMaestroReadonlyBundleContent(ctx, mockDB, cluster.ID, ref, mockMaestro)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to replace ManagementClusterContent")
		assert.True(t, database.IsResponseError(err, http.StatusPreconditionFailed), "expected 412 Precondition Failed")
	})

	t.Run("error occurs when managementClusterContentsDBClient.Get fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		getErr := fmt.Errorf("cosmos connection error")
		// Use error-injecting wrapper to simulate Get error
		mockDB := &errorInjectingDBClient{
			MockDBClient: databasetesting.NewMockDBClient(),
			mccCRUD: &errorInjectingMCCCRUD{
				getErr: getErr,
			},
		}

		err := readAndPersistMaestroReadonlyBundleContent(ctx, mockDB, cluster.ID, ref, mockMaestro)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get ManagementClusterContent")
		assert.Contains(t, err.Error(), "cosmos connection error")
	})
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ClusterNotFound(t *testing.T) {
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetServiceProviderClusterError(t *testing.T) {
	ctx := context.Background()

	baseMockDB := databasetesting.NewMockDBClient()

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}

	// Add the cluster to the database
	clustersCRUD := baseMockDB.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Use error-injecting wrapper to simulate SPC Get error
	expectedError := fmt.Errorf("database error")
	mockDBClient := &errorInjectingDBClient{
		MockDBClient: baseMockDB,
		spcCRUD: &errorInjectingSPCCRUD{
			getErr: expectedError,
		},
	}

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderCluster")
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NoMaestroReadonlyBundlesRefs(t *testing.T) {
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
	}
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetProvisionShardError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		cosmosClient:         mockDBClient,
		clusterServiceClient: mockClusterService,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(nil, fmt.Errorf("provision shard error"))

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get Cluster Provision Shard")
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ReadAndPersistFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroClientBuilder:               mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier: "test-env",
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	validHCJSON := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"hc1","namespace":"ns1"}}`
	bundle := buildTestMaestroBundleWithStatusFeedback("bundle-name", "test-consumer", validHCJSON)
	mockMaestroClient.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(bundle, nil)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	mccCRUD := mockDBClient.ManagementClusterContents(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Status.KubeContent)
	require.Len(t, got.Status.KubeContent.Items, 1)
	// Decode and spot-check
	var u unstructured.Unstructured
	err = json.Unmarshal(got.Status.KubeContent.Items[0].Raw, &u)
	require.NoError(t, err)
	assert.Equal(t, "HostedCluster", u.GetKind())
	assert.Equal(t, "hc1", u.GetName())
}
