// Copyright 2025 Microsoft Corporation
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
	"errors"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func DumpDataToLogger(ctx context.Context, cosmosClient database.DBClient, resourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	// load the HCP from the cosmos DB
	cosmosCRUD, err := cosmosClient.UntypedCRUD(*resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	startingCosmosRecord, err := cosmosCRUD.Get(ctx, resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info(fmt.Sprintf("dumping resourceID %v", startingCosmosRecord.ResourceID),
		"currentResourceID", startingCosmosRecord.ResourceID.String(),
		"content", startingCosmosRecord,
	)

	allCosmosRecords, err := cosmosCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	for _, typedDocument := range allCosmosRecords.Items(ctx) {
		logger.Info(fmt.Sprintf("dumping resourceID %v", typedDocument.ResourceID),
			"currentResourceID", typedDocument.ResourceID.String(),
			"content", typedDocument,
		)
	}
	if err := allCosmosRecords.GetError(); err != nil {
		errs = append(errs, err)
	}

	// dump all related operations, including the completed ones.
	allOperationsForSubscription, err := cosmosClient.Operations(resourceID.SubscriptionID).List(ctx, nil)
	if err != nil {
		errs = append(errs, err)
	}
	resourceIDString := strings.ToLower(resourceID.String())
	for _, operation := range allOperationsForSubscription.Items(ctx) {
		currOperationTarget := strings.ToLower(operation.ExternalID.String())
		if strings.HasPrefix(currOperationTarget, resourceIDString) {
			logger.Info(fmt.Sprintf("dumping resourceID %v", operation.ResourceID),
				"currentResourceID", operation.ResourceID.String(),
				"content", operation,
			)
		}
	}
	if err := allOperationsForSubscription.GetError(); err != nil {
		errs = append(errs, err)
	}

	return utils.TrackError(errors.Join(errs...))
}

// DumpBillingToLogger dumps billing documents for the given cluster resource ID to the logger.
// Queries the subscription partition and filters in-memory for the specific cluster.
// This includes both active and deleted billing documents for the cluster.
// Follows best-effort semantics - errors are returned but should not fail critical operations.
func DumpBillingToLogger(ctx context.Context, cosmosClient database.DBClient, resourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	// Query billing documents for this subscription (partition-scoped, not cross-partition)
	iter, err := cosmosClient.Billing(resourceID.SubscriptionID).List(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// Find and dump billing documents for this cluster
	resourceIDString := strings.ToLower(resourceID.String())
	found := false
	for _, doc := range iter.Items(ctx) {
		// Check if this billing document belongs to the requested cluster
		if doc.ResourceID != nil && strings.ToLower(doc.ResourceID.String()) == resourceIDString {
			logger.Info(fmt.Sprintf("dumping billing document for resourceID %v", doc.ResourceID),
				"currentResourceID", doc.ResourceID.String(),
				"content", doc,
			)
			found = true
		}
	}

	if err := iter.GetError(); err != nil {
		return utils.TrackError(err)
	}

	if !found {
		logger.Info("no billing document found for cluster", "resourceID", resourceID.String())
	}

	return nil
}

// DumpClusterAndBillingToLogger dumps both cluster data and billing documents for the given cluster resource ID.
// This aggregates errors from both DumpDataToLogger and DumpBillingToLogger.
// Follows best-effort semantics - errors are returned but should not fail critical operations.
func DumpClusterAndBillingToLogger(ctx context.Context, cosmosClient database.DBClient, resourceID *azcorearm.ResourceID) error {
	var errs []error

	// Dump cluster data (cluster + nodepools + externalauth + operations)
	if err := DumpDataToLogger(ctx, cosmosClient, resourceID); err != nil {
		errs = append(errs, err)
	}

	// Dump billing documents
	if err := DumpBillingToLogger(ctx, cosmosClient, resourceID); err != nil {
		errs = append(errs, err)
	}

	return utils.TrackError(errors.Join(errs...))
}
