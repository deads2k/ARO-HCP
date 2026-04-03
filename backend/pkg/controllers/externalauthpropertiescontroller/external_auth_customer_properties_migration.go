package externalauthpropertiescontroller

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

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// externalAuthCustomerPropertiesMigrationController is a ExternalAuth controller that migrates properties (customer properties)
// from cluster-service to cosmos DB. It uses the .platform.vmSize attribute to know that customerProperties are missing.
// Old records will lack those fields and once we read from cluster-service, we'll have the information we need.
type externalAuthCustomerPropertiesMigrationController struct {
	cooldownChecker controllerutils.CooldownChecker

	externalAuthLister   listers.ExternalAuthLister
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthCustomerPropertiesMigrationController)(nil)

func NewExternalAuthCustomerPropertiesMigrationController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()

	syncer := &externalAuthCustomerPropertiesMigrationController{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		externalAuthLister:   externalAuthLister,
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthCustomerPropertiesMigration",
		cosmosClient,
		informers,
		60*time.Minute, // Check every 60 minutes
		syncer,
	)

	return controller
}

func (c *externalAuthCustomerPropertiesMigrationController) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

func (c *externalAuthCustomerPropertiesMigrationController) NeedsWork(ctx context.Context, existingExternalAuth *api.HCPOpenShiftClusterExternalAuth) bool {
	// Check if we have a Clusters Service's ExternalAuth service ID to query. We will lack this information for newly created records when we
	// transition to async Clusters Service's ExternalAuth creation.
	if len(existingExternalAuth.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return false
	}

	// We use .properties.issuer.url as the marker to know if customer properties
	// need to be migrated for the ExternalAuth being processed.
	// .properties.issuer.url is a required attribute at ARM API level, so its
	// absence in Cosmos signals that the customer properties of the ExternalAuth are not
	// migrated into Cosmos yet and we need to migrate them.
	needsIssuer := len(existingExternalAuth.Properties.Issuer.URL) == 0
	return needsIssuer
}

func (c *externalAuthCustomerPropertiesMigrationController) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	logger := utils.LoggerFromContext(ctx)

	// do the super cheap cache check first
	cachedExternalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		// we'll be re-fired if it is created again
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get externalAuth from cache: %w", err))
	}
	if !c.NeedsWork(ctx, cachedExternalAuth) {
		// if the cache doesn't need work, then we'll be retriggered if those values change when the cache updates.
		// if the values don't change, then we still have no work to do.
		return nil
	}

	// Get the externalAuth from Cosmos
	externalAuthCRUD := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName)
	existingExternalAuth, err := externalAuthCRUD.Get(ctx, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return nil // externalAuth doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get externalAuth: %w", err))
	}
	// check if we need to do work again. Sometimes the live data is more fresh than the cache and obviates the need to any work
	if !c.NeedsWork(ctx, existingExternalAuth) {
		return nil
	}

	// Fetch the ExternalAuth from Cluster Service
	csExternalAuth, err := c.clusterServiceClient.GetExternalAuth(ctx, existingExternalAuth.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get externalAuth from Cluster Service: %w", err))
	}

	// Use ConvertCStoExternalAuth to convert the externalAuth and extract the Properties (customer properties)
	convertedExternalAuth, err := ocm.ConvertCStoExternalAuth(existingExternalAuth.ID, csExternalAuth)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to convert externalAuth from Cluster Service: %w", err))
	}

	// Update only the Properties from the converted externalAuth
	existingExternalAuth.Properties = convertedExternalAuth.Properties

	// Write the updated externalAuth back to Cosmos
	if _, err := externalAuthCRUD.Replace(ctx, existingExternalAuth, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace externalAuth: %w", err))
	}

	logger.Info("migrated externalAuth properties from Cluster Service to Cosmos")

	return nil
}
