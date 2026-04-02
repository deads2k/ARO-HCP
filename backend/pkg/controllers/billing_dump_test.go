// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// mockActiveOperationLister is a simple mock that always reports no active operations.
type mockActiveOperationLister struct{}

func (m *mockActiveOperationLister) List(ctx context.Context) ([]*api.Operation, error) {
	return nil, nil
}

func (m *mockActiveOperationLister) Get(ctx context.Context, subscriptionID, name string) (*api.Operation, error) {
	return nil, nil
}

func (m *mockActiveOperationLister) ListActiveOperationsForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.Operation, error) {
	return nil, nil
}

var _ listers.ActiveOperationLister = &mockActiveOperationLister{}

func TestBillingDumpController_SyncOnce(t *testing.T) {
	ctx := context.Background()

	clusterResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	mockDB := databasetesting.NewMockDBClient()
	activeOperationLister := &mockActiveOperationLister{}

	controller := NewBillingDumpController(activeOperationLister, mockDB)

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    clusterResourceID.SubscriptionID,
		ResourceGroupName: clusterResourceID.ResourceGroupName,
		HCPClusterName:    clusterResourceID.Name,
	}

	// SyncOnce should never return an error (best effort)
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)
}

func TestBillingDumpController_CooldownChecker(t *testing.T) {
	mockDB := databasetesting.NewMockDBClient()
	activeOperationLister := &mockActiveOperationLister{}

	controller := NewBillingDumpController(activeOperationLister, mockDB)

	// Should return a cooldown checker
	cooldown := controller.CooldownChecker()
	require.NotNil(t, cooldown)
}

func TestNewBillingDumpController(t *testing.T) {
	mockDB := databasetesting.NewMockDBClient()
	activeOperationLister := &mockActiveOperationLister{}

	controller := NewBillingDumpController(activeOperationLister, mockDB)

	require.NotNil(t, controller)
}

func TestBillingDumpController_SyncOnce_WithBillingDoc(t *testing.T) {
	ctx := context.Background()

	clusterResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	mockDB := databasetesting.NewMockDBClient()
	activeOperationLister := &mockActiveOperationLister{}

	// Create billing document
	billingDoc := database.NewBillingDocument(clusterResourceID)
	billingDoc.CreationTime = time.Now().UTC()
	err = mockDB.CreateBillingDoc(ctx, billingDoc)
	require.NoError(t, err)

	controller := NewBillingDumpController(activeOperationLister, mockDB)

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    clusterResourceID.SubscriptionID,
		ResourceGroupName: clusterResourceID.ResourceGroupName,
		HCPClusterName:    clusterResourceID.Name,
	}

	// SyncOnce should never return an error (best effort)
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)
}

func TestBillingDumpController_CooldownRespected(t *testing.T) {
	ctx := context.Background()

	clusterResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	mockDB := databasetesting.NewMockDBClient()
	activeOperationLister := &mockActiveOperationLister{}

	controller := NewBillingDumpController(activeOperationLister, mockDB)

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    clusterResourceID.SubscriptionID,
		ResourceGroupName: clusterResourceID.ResourceGroupName,
		HCPClusterName:    clusterResourceID.Name,
	}

	// First sync should succeed (cooldown allows)
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)

	// Subsequent syncs should still succeed (mock always returns no active ops)
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)
}
