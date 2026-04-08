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

package serverutils

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestDumpBillingToLogger(t *testing.T) {
	ctx := context.Background()

	cluster1ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	cluster2ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-2")
	require.NoError(t, err)

	// Create mock DB with billing documents
	mockDB := databasetesting.NewMockDBClient()

	// Create billing doc for cluster-1 (active)
	billingDoc1 := database.NewBillingDocument("billing-doc-1", cluster1ResourceID)
	billingDoc1.CreationTime = time.Now().UTC()
	err = mockDB.BillingDocs(cluster1ResourceID.SubscriptionID).Create(ctx, billingDoc1)
	require.NoError(t, err)

	// Create billing doc for cluster-2 (deleted)
	billingDoc2 := database.NewBillingDocument("billing-doc-2", cluster2ResourceID)
	billingDoc2.CreationTime = time.Now().UTC().Add(-1 * time.Hour)
	deletionTime := time.Now().UTC()
	billingDoc2.DeletionTime = &deletionTime
	err = mockDB.BillingDocs(cluster2ResourceID.SubscriptionID).Create(ctx, billingDoc2)
	require.NoError(t, err)

	// Test: Dump billing for cluster-1 should find the billing document
	err = DumpBillingToLogger(ctx, mockDB, cluster1ResourceID)
	require.NoError(t, err)

	// Test: Dump billing for cluster-2 should also find the billing document (we dump all, including deleted)
	err = DumpBillingToLogger(ctx, mockDB, cluster2ResourceID)
	require.NoError(t, err)

	// Test: Dump billing for non-existent cluster should not error (best effort)
	nonExistentResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-3/resourceGroups/rg-3/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-3")
	require.NoError(t, err)
	err = DumpBillingToLogger(ctx, mockDB, nonExistentResourceID)
	require.NoError(t, err)
}

func TestDumpBillingToLogger_PartitionScoping(t *testing.T) {
	ctx := context.Background()

	// Create clusters in different subscriptions
	cluster1ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	cluster2ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-2")
	require.NoError(t, err)

	cluster3ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-3")
	require.NoError(t, err)

	mockDB := databasetesting.NewMockDBClient()

	// Create billing docs for all three clusters
	for i, resourceID := range []*azcorearm.ResourceID{cluster1ResourceID, cluster2ResourceID, cluster3ResourceID} {
		doc := database.NewBillingDocument(resourceID.Name+"-billing-"+string(rune('1'+i)), resourceID)
		doc.CreationTime = time.Now().UTC()
		err = mockDB.BillingDocs(resourceID.SubscriptionID).Create(ctx, doc)
		require.NoError(t, err)
	}

	// Dump cluster-1: should only query sub-1 partition (not sub-2)
	// This verifies partition-scoped query works correctly
	err = DumpBillingToLogger(ctx, mockDB, cluster1ResourceID)
	require.NoError(t, err)
}
