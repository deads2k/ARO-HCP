package controllers

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

import (
	"context"
	"errors"
	"fmt"
	"time"

	workv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type deleteOrphanedMaestroReadonlyBundles struct {
	name string

	cosmosClient database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroClientBuilder maestro.MaestroClientBuilder

	maestroSourceEnvironmentIdentifier string
}

// NewDeleteOrphanedMaestroReadonlyBundlesController periodically looks for cosmos objs that don't have an owning cluster and deletes them.
func NewDeleteOrphanedMaestroReadonlyBundlesController(cosmosClient database.DBClient, csClient ocm.ClusterServiceClientSpec, maestroClientBuilder maestro.MaestroClientBuilder, maestroSourceEnvironmentIdentifier string) controllerutils.Controller {
	c := &deleteOrphanedMaestroReadonlyBundles{
		name:                               "DeleteOrphanedMaestroReadonlyBundles",
		cosmosClient:                       cosmosClient,
		clusterServiceClient:               csClient,
		maestroClientBuilder:               maestroClientBuilder,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "DeleteOrphanedMaestroReadonlyBundles",
			},
		),
	}

	return c
}

// SyncOnce algorithm:
//  1. Build a map from Cluster Service provision shard ID to Maestro client (one client per registered provision shard).
//  2. For each resource type that contains Maestro readonly bundles that we decide to include here:
//    2.1 List all documents (initial snapshot).
//    2.2 Build a map from Cluster Service provision shard ID to the documents on that shard (from the initial list).
//    2.3 For each shard, list Maestro bundles (paginated, and with a label selector that filters for the readonly managed-by label associated to
//        the specifc resource type we are processing). A bundle is a delete candidate if it passes the resource-scoped readonly managed-by label
//        filter and its name is not referenced by any document on that shard in the initial map. Each candidate records the provision shard
//        id and a pointer to the listed ManifestWork.
//    2.4 List all documents again (fresh snapshot), rebuild the map from it in the same way as 2.2.
//    2.5 For each candidate, if the bundle name is still not referenced on that shard in the fresh snapshot, delete it via Maestro
//        bundle deletion API.
//
// Cross-store: The fresh Cosmos snapshots and per-shard reference sets prevent deleting a bundle that is already referenced
// in committed documents by the time that snapshot is built, so a stale initial list alone does not cause accidental delete.

// IMPORTANT NOTE: This assumes that the maestro server associated to the provision shard
// has resources with always the same source ID. If it turns out we cannot have this assumption this logic would not
// be good enough. In that case it might be necessary to store to what source ID a Maestro Bundle/set of Maestro Bundles
// belongs to but then the instantiation of the Maestro client needs to be done differently as its scoped to
// Maestro Consumer Name + Maestro Source ID. We know for example that in the CSPR environment different CS instances
// have different Maestro source IDs using the same Maestro Server.
func (c *deleteOrphanedMaestroReadonlyBundles) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing orphaned Maestro Readonly Bundles")

	logger.Info("Building Maestro clients per Cluster Service provision shard")
	maestroClientsByShard, err := c.buildMaestroClientsByProvisionShard(ctx)
	// Cancel Maestro clients when the sync is done to avoid leaking resources (map may be partial on error).
	defer cancelMaestroClientsByProvisionShard(maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build Maestro clients by provision shard: %w", err))
	}
	logger.Info(fmt.Sprintf("Built Maestro clients for %d provision shards", len(maestroClientsByShard)))

	initialServiceProviderClusters, err := c.getAllServiceProviderClusters(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get all ServiceProviderClusters: %w", err))
	}
	logger.Info(fmt.Sprintf("Found %d ServiceProviderClusters (initial)", len(initialServiceProviderClusters)))

	logger.Info("Mapping initial ServiceProviderClusters to provision shards")
	initialShardToSPCs, err := c.mapServiceProviderClustersByProvisionShard(ctx, initialServiceProviderClusters, maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to map ServiceProviderClusters to provision shards: %w", err))
	}
	logger.Info(fmt.Sprintf("Initial ServiceProviderClusters mapped to %d provision shards", len(initialShardToSPCs)))

	allServiceProviderNodePools, err := c.getAllServiceProviderNodePools(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get all ServiceProviderNodePools: %w", err))
	}
	logger.Info(fmt.Sprintf("Found %d ServiceProviderNodePools (initial)", len(allServiceProviderNodePools)))

	logger.Info("Mapping initial ServiceProviderNodePools to provision shards")
	initialShardToSPNPs, err := c.mapServiceProviderNodePoolsByProvisionShard(ctx, allServiceProviderNodePools, maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to map ServiceProviderNodePools to provision shards: %w", err))
	}
	logger.Info(fmt.Sprintf("Initial ServiceProviderNodePools mapped to %d provision shards", len(initialShardToSPNPs)))

	var syncErrors []error
	logger.Info("Ensuring orphaned cluster-scoped Maestro Readonly Bundles are deleted")
	if err := c.ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(ctx, maestroClientsByShard, initialShardToSPCs); err != nil {
		syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to ensure orphaned cluster-scoped Maestro Bundles are deleted: %w", err)))
	}

	logger.Info("Ensuring orphaned nodepool-scoped Maestro Readonly Bundles are deleted")
	if err := c.ensureOrphanedNodePoolScopedMaestroReadonlyBundlesAreDeleted(ctx, maestroClientsByShard, initialShardToSPNPs); err != nil {
		syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to ensure orphaned nodepool-scoped Maestro Bundles are deleted: %w", err)))
	}

	logger.Info("End of orphaned Maestro Readonly Bundles sync")
	return errors.Join(syncErrors...)
}

// getAllServiceProviderClusters returns the list of all ServiceProviderClusters in the database.
func (c *deleteOrphanedMaestroReadonlyBundles) getAllServiceProviderClusters(ctx context.Context) ([]*api.ServiceProviderCluster, error) {
	// We list all ServiceProviderClusters in chunks of 500 to avoid putting
	// too much pressure on the Cosmos DB.
	// Any failure to iterate over the ServiceProviderclusters ends the sync process because otherwise
	// we would not have the complete information to evaluate the deletion and we could
	// accidentally delete Maestro Bundles that are still in use.
	listOptions := &database.DBClientListResourceDocsOptions{
		PageSizeHint: ptr.To(int32(500)),
	}
	allServiceProviderClusters := []*api.ServiceProviderCluster{}
	for {
		iterator, err := c.cosmosClient.GlobalListers().ServiceProviderClusters().List(ctx, listOptions)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to list ServiceProviderClusters: %w", err))
		}
		for _, spc := range iterator.Items(ctx) {
			allServiceProviderClusters = append(allServiceProviderClusters, spc)
		}
		err = iterator.GetError()
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed iterating ServiceProviderClusters: %w", err))
		}

		continuationToken := iterator.GetContinuationToken()
		if continuationToken == "" {
			break
		}
		listOptions.ContinuationToken = &continuationToken
	}

	return allServiceProviderClusters, nil
}

// getAllServiceProviderNodePools returns the list of all ServiceProviderNodePools in the database.
func (c *deleteOrphanedMaestroReadonlyBundles) getAllServiceProviderNodePools(ctx context.Context) ([]*api.ServiceProviderNodePool, error) {
	listOptions := &database.DBClientListResourceDocsOptions{
		PageSizeHint: ptr.To(int32(500)),
	}
	allServiceproviderNodePools := []*api.ServiceProviderNodePool{}
	for {
		iterator, err := c.cosmosClient.GlobalListers().ServiceProviderNodePools().List(ctx, listOptions)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to list ServiceProviderNodePools: %w", err))
		}
		for _, spnp := range iterator.Items(ctx) {
			allServiceproviderNodePools = append(allServiceproviderNodePools, spnp)
		}
		err = iterator.GetError()
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed iterating ServiceProviderNodePools: %w", err))
		}

		continuationToken := iterator.GetContinuationToken()
		if continuationToken == "" {
			break
		}
		listOptions.ContinuationToken = &continuationToken
	}

	return allServiceproviderNodePools, nil
}

// shardMaestroClient holds a Maestro API client for one Cluster Service provision shard and its teardown cancel func.
type shardMaestroClient struct {
	maestroClient           maestro.Client
	maestroClientCancelFunc context.CancelFunc
}

// cancelMaestroClientsByProvisionShard runs the cancel function for each Maestro client entry in maestroClientsByProvisionShard.
func cancelMaestroClientsByProvisionShard(maestroClientsByProvisionShard map[string]*shardMaestroClient) {
	for _, entry := range maestroClientsByProvisionShard {
		entry.maestroClientCancelFunc()
	}
}

// buildMaestroClientsByProvisionShard lists registered provision shards from Cluster Service and builds a map of
// provision shard ID to Maestro client. The key of the map is the CS provision shard ID.
//
// On error the returned map may be partial (clients created before the error). The caller must defer cancelMaestroClientsByProvisionShard unconditionally.
func (c *deleteOrphanedMaestroReadonlyBundles) buildMaestroClientsByProvisionShard(ctx context.Context) (map[string]*shardMaestroClient, error) {
	maestroClientsByProvisionShard := map[string]*shardMaestroClient{}

	// TODO we list the provision shards from CS but at some point we should have
	// the information in Cosmos and this should be changed to use that instead.
	// TODO should we take into account the provision shard status on what to consider (active, maintenance, offline, ...)?
	// for now we consider all provision shards independently of their status.
	for provisionShard := range c.clusterServiceClient.ListProvisionShards().Items(ctx) {
		// We create a new context with a cancel function so we can cancel the Maestro client when the sync is done.
		// This is important to avoid leaking resources when the sync is done.
		maestroClientCtx, cancel := context.WithCancel(ctx)
		maestroClient, err := createMaestroClientFromCSProvisionShard(maestroClientCtx, c.maestroSourceEnvironmentIdentifier, c.maestroClientBuilder, provisionShard)
		if err != nil {
			cancel() // on error creating the Maestro client we ensure we cancel the context that we just created too
			return maestroClientsByProvisionShard, utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
		}
		maestroClientsByProvisionShard[provisionShard.ID()] = &shardMaestroClient{
			maestroClient:           maestroClient,
			maestroClientCancelFunc: cancel,
		}
	}

	return maestroClientsByProvisionShard, nil
}

// mapServiceProviderClustersByProvisionShard groups ServiceProviderClusters by Cluster Service provision shard ID.
// Every resolved shard must exist in maestroClientsByShard (registered provision shards).
func (c *deleteOrphanedMaestroReadonlyBundles) mapServiceProviderClustersByProvisionShard(ctx context.Context, spcs []*api.ServiceProviderCluster, maestroClientsByShard map[string]*shardMaestroClient) (map[string][]*api.ServiceProviderCluster, error) {
	res := make(map[string][]*api.ServiceProviderCluster)
	for _, spc := range spcs {
		shardID, err := c.clusterProvisionShardIDForServiceProviderCluster(ctx, spc)
		if err != nil {
			return nil, err
		}
		if _, ok := maestroClientsByShard[shardID]; !ok {
			return nil, utils.TrackError(fmt.Errorf("provision shard %s for ServiceProviderCluster %s is not present in provision shards map", shardID, spc.ResourceID.String()))
		}
		res[shardID] = append(res[shardID], spc)
	}
	return res, nil
}

// mapServiceProviderNodePoolsByProvisionShard groups ServiceProviderNodePools by Cluster Service provision shard ID
// of their owning cluster. Every resolved shard must exist in maestroClientsByShard (registered provision shards).
func (c *deleteOrphanedMaestroReadonlyBundles) mapServiceProviderNodePoolsByProvisionShard(ctx context.Context, spnps []*api.ServiceProviderNodePool, maestroClientsByShard map[string]*shardMaestroClient) (map[string][]*api.ServiceProviderNodePool, error) {
	res := make(map[string][]*api.ServiceProviderNodePool)
	for _, spnp := range spnps {
		shardID, err := c.clusterProvisionShardIDForServiceProviderNodePool(ctx, spnp)
		if err != nil {
			return nil, err
		}
		if _, ok := maestroClientsByShard[shardID]; !ok {
			return nil, utils.TrackError(fmt.Errorf("provision shard %s for ServiceProviderNodePool %s is not present in provision shards map", shardID, spnp.ResourceID.String()))
		}
		res[shardID] = append(res[shardID], spnp)
	}
	return res, nil
}

// orphanReadonlyBundleDeleteCandidate is a Maestro bundle listed on a provision shard that was not referenced by the
// initial snapshot for that shard; delete still requires a fresh snapshot check.
type orphanReadonlyBundleDeleteCandidate struct {
	csShardID string
	bundle    *workv1.ManifestWork
}

// ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted ensures that Maestro readonly bundles managed by the cluster-scoped
// controller are deleted when no ServiceProviderCluster on that provision shard references them.
//
//  1. From initialShardToSPCs, build per-shard sets of referenced Maestro bundle names.
//  2. For each shard with a Maestro client, list bundles (paginated) and add candidates when the bundle is not referenced on that shard.
//  3. List all ServiceProviderClusters again (fresh), map them by shard, rebuild referenced sets.
//  4. Delete each candidate that is still unreferenced on its shard in the fresh snapshot.
func (c *deleteOrphanedMaestroReadonlyBundles) ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(ctx context.Context, maestroClientsByShard map[string]*shardMaestroClient, initialShardToSPCs map[string][]*api.ServiceProviderCluster) error {
	logger := utils.LoggerFromContext(ctx)
	var syncErrors []error

	referencedByShardInitial, err := referencedMaestroAPIMaestroBundleNamesByShard(initialShardToSPCs)
	if err != nil {
		return utils.TrackError(fmt.Errorf("error building referenced Maestro API Maestro bundle names by shard (initial snapshot): %w", err))
	}

	var deleteCandidates []orphanReadonlyBundleDeleteCandidate

	for csShardID, shardEntry := range maestroClientsByShard {
		shardLogger := logger.WithValues("csProvisionShardID", csShardID)
		ctxShard := utils.ContextWithLogger(ctx, shardLogger)
		initialOnShard := initialShardToSPCs[csShardID]
		shardLogger.Info(fmt.Sprintf("listing cluster-scoped Maestro bundles on cluster service provision shard %s (%d ServiceProviderClusters in initial shard map)", csShardID, len(initialOnShard)))
		maestroClient := shardEntry.maestroClient
		listOptions := metav1.ListOptions{Limit: 400, Continue: "", LabelSelector: fmt.Sprintf("%s=%s", readonlyBundleManagedByK8sLabelKey, readonlyBundleManagedByK8sLabelValueClusterScoped)}
		for {
			maestroBundles, err := maestroClient.List(ctxShard, listOptions)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to list Maestro Bundles for shard %s: %w", csShardID, err))
			}
			for i := range maestroBundles.Items {
				maestroBundle := &maestroBundles.Items[i]
				// Even though Maestro should filter by the K8s label we specified we double check it here to be sure
				if maestroBundle.Labels[readonlyBundleManagedByK8sLabelKey] != readonlyBundleManagedByK8sLabelValueClusterScoped {
					continue
				}
				// We check if the Maestro bundle is referenced by any of the ServiceProviderClusters on the shard in the initial snapshot.
				// If it is referenced we skip it as it is not an orphan.
				// The Maestro API Maestro Bundle Name should be unique within a given Maestro Consumer Name and Maestro Source ID.
				if shardRefSet := referencedByShardInitial[csShardID]; shardRefSet != nil {
					if _, referenced := shardRefSet[maestroBundle.Name]; referenced {
						continue
					}
				}
				deleteCandidates = append(deleteCandidates, orphanReadonlyBundleDeleteCandidate{
					csShardID: csShardID,
					bundle:    maestroBundle,
				})
			}
			continuationToken := maestroBundles.GetContinue()
			if continuationToken == "" {
				break
			}
			listOptions.Continue = continuationToken
		}
	}

	freshServiceProviderClusters, err := c.getAllServiceProviderClusters(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("error getting all ServiceProviderClusters (fresh snapshot): %w", err))
	}
	freshShardToSPCs, err := c.mapServiceProviderClustersByProvisionShard(ctx, freshServiceProviderClusters, maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("error mapping fresh ServiceProviderClusters to provision shards (fresh snapshot): %w", err))
	}
	referencedByShardFresh, err := referencedMaestroAPIMaestroBundleNamesByShard(freshShardToSPCs)
	if err != nil {
		return utils.TrackError(fmt.Errorf("error building referenced Maestro API Maestro bundle names by shard (fresh snapshot): %w", err))
	}

	for _, cand := range deleteCandidates {
		csShardID := cand.csShardID
		candidateMaestroBundle := cand.bundle
		shardEntry, ok := maestroClientsByShard[csShardID]
		if !ok {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("no Maestro client for shard %s when deleting bundle %q", csShardID, candidateMaestroBundle.Name)))
			continue
		}
		maestroClient := shardEntry.maestroClient

		shardLogger := utils.LoggerFromContext(ctx).WithValues("csProvisionShardID", csShardID)
		ctxShard := utils.ContextWithLogger(ctx, shardLogger)
		if shardRefSet := referencedByShardFresh[csShardID]; shardRefSet != nil {
			if _, referenced := shardRefSet[candidateMaestroBundle.Name]; referenced {
				// If the Maestro bundle is referenced by any of the ServiceProviderClusters on the shard in the fresh snapshot we skip it as it is not an orphan.
				continue
			}
		}

		shardLogger.Info("Deleting orphaned Maestro readonly Bundle", "maestroConsumerName", candidateMaestroBundle.Namespace, "maestroAPIMaestroBundleName", candidateMaestroBundle.Name, "maestroAPIMaestroBundleID", candidateMaestroBundle.UID)
		err = maestroClient.Delete(ctxShard, candidateMaestroBundle.Name, metav1.DeleteOptions{})
		if err != nil {
			//  Failure to delete does not end the sync process. We log the error and we continue with the processing of other Maestro bundle deletion candidates.
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to delete Maestro Bundle: %w", err)))
		} else {
			shardLogger.Info("Deleted orphaned Maestro readonly Bundle", "maestroConsumerName", candidateMaestroBundle.Namespace, "maestroAPIMaestroBundleName", candidateMaestroBundle.Name, "maestroAPIMaestroBundleID", candidateMaestroBundle.UID)
		}
	}

	return errors.Join(syncErrors...)
}

// ensureOrphanedNodePoolScopedMaestroReadonlyBundlesAreDeleted ensures that Maestro readonly bundles managed by the
// nodepool-scoped controller are deleted when no ServiceProviderNodePool on that provision shard references them.
// It uses the same two-snapshot pattern as ensureOrphanedMaestroReadonlyBundlesAreDeleted.
func (c *deleteOrphanedMaestroReadonlyBundles) ensureOrphanedNodePoolScopedMaestroReadonlyBundlesAreDeleted(ctx context.Context, maestroClientsByShard map[string]*shardMaestroClient, initialShardToSPNPs map[string][]*api.ServiceProviderNodePool) error {
	logger := utils.LoggerFromContext(ctx)
	var syncErrors []error

	referencedByShardInitial, err := referencedMaestroAPIMaestroBundleNamesByShardFromNodePools(initialShardToSPNPs)
	if err != nil {
		return utils.TrackError(fmt.Errorf("error building referenced Maestro API Maestro bundle names by shard from node pools (initial snapshot): %w", err))
	}

	var deleteCandidates []orphanReadonlyBundleDeleteCandidate

	for csShardID, shardEntry := range maestroClientsByShard {
		shardLogger := logger.WithValues("csProvisionShardID", csShardID)
		ctxShard := utils.ContextWithLogger(ctx, shardLogger)
		initialOnShard := initialShardToSPNPs[csShardID]
		shardLogger.Info(fmt.Sprintf("listing nodepool-scoped Maestro bundles on cluster service provision shard %s (%d ServiceProviderNodePools in initial shard map)", csShardID, len(initialOnShard)))
		maestroClient := shardEntry.maestroClient
		listOptions := metav1.ListOptions{Limit: 400, Continue: "", LabelSelector: fmt.Sprintf("%s=%s", readonlyBundleManagedByK8sLabelKey, readonlyBundleManagedByK8sLabelValueNodePoolScoped)}
		for {
			maestroBundles, err := maestroClient.List(ctxShard, listOptions)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to list nodepool-scoped Maestro Bundles for shard %s: %w", csShardID, err))
			}
			for i := range maestroBundles.Items {
				maestroBundle := &maestroBundles.Items[i]
				// Even though Maestro should filter by the K8s label we specified we double check it here to be sure
				if maestroBundle.Labels[readonlyBundleManagedByK8sLabelKey] != readonlyBundleManagedByK8sLabelValueNodePoolScoped {
					continue
				}
				// We check if the Maestro bundle is referenced by any of the ServiceProviderNodePools on the shard in the initial snapshot.
				// If it is referenced we skip it as it is not an orphan.
				// The Maestro API Maestro Bundle Name should be unique within a given Maestro Consumer Name and Maestro Source ID.
				if shardRefSet := referencedByShardInitial[csShardID]; shardRefSet != nil {
					if _, referenced := shardRefSet[maestroBundle.Name]; referenced {
						continue
					}
				}
				deleteCandidates = append(deleteCandidates, orphanReadonlyBundleDeleteCandidate{
					csShardID: csShardID,
					bundle:    maestroBundle,
				})
			}
			continuationToken := maestroBundles.GetContinue()
			if continuationToken == "" {
				break
			}
			listOptions.Continue = continuationToken
		}
	}

	freshServiceProviderNodePools, err := c.getAllServiceProviderNodePools(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("error getting all ServiceProviderNodePools (fresh snapshot): %w", err))
	}
	freshShardToSPNPs, err := c.mapServiceProviderNodePoolsByProvisionShard(ctx, freshServiceProviderNodePools, maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("error mapping fresh ServiceProviderNodePools to provision shards (fresh snapshot): %w", err))
	}
	referencedByShardFresh, err := referencedMaestroAPIMaestroBundleNamesByShardFromNodePools(freshShardToSPNPs)
	if err != nil {
		return utils.TrackError(fmt.Errorf("error building referenced Maestro API Maestro bundle names by shard from node pools (fresh snapshot): %w", err))
	}

	for _, cand := range deleteCandidates {
		csShardID := cand.csShardID
		candidateMaestroBundle := cand.bundle
		shardEntry, ok := maestroClientsByShard[csShardID]
		if !ok {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("no Maestro client for shard %s when deleting nodepool bundle %q", csShardID, candidateMaestroBundle.Name)))
			continue
		}
		maestroClient := shardEntry.maestroClient

		shardLogger := utils.LoggerFromContext(ctx).WithValues("csProvisionShardID", csShardID)
		ctxShard := utils.ContextWithLogger(ctx, shardLogger)
		if shardRefSet := referencedByShardFresh[csShardID]; shardRefSet != nil {
			if _, referenced := shardRefSet[candidateMaestroBundle.Name]; referenced {
				// If the Maestro bundle is referenced by any of the ServiceProviderClusters on the shard in the fresh snapshot we skip it as it is not an orphan.
				continue
			}
		}

		shardLogger.Info("Deleting orphaned nodepool-scoped Maestro readonly Bundle", "maestroConsumerName", candidateMaestroBundle.Namespace, "maestroAPIMaestroBundleName", candidateMaestroBundle.Name, "maestroAPIMaestroBundleID", candidateMaestroBundle.UID)
		err = maestroClient.Delete(ctxShard, candidateMaestroBundle.Name, metav1.DeleteOptions{})
		if err != nil {
			//  Failure to delete does not end the sync process. We log the error and we continue with the processing of other Maestro bundle deletion candidates.
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to delete nodepool-scoped Maestro Bundle: %w", err)))
		} else {
			shardLogger.Info("Deleted orphaned nodepool-scoped Maestro readonly Bundle", "maestroConsumerName", candidateMaestroBundle.Namespace, "maestroAPIMaestroBundleName", candidateMaestroBundle.Name, "maestroAPIMaestroBundleID", candidateMaestroBundle.UID)
		}
	}

	return errors.Join(syncErrors...)
}

// clusterProvisionShardIDForServiceProviderCluster returns the Cluster Service provision shard ID for the cluster that owns the SPC.
func (c *deleteOrphanedMaestroReadonlyBundles) clusterProvisionShardIDForServiceProviderCluster(ctx context.Context, spc *api.ServiceProviderCluster) (string, error) {
	clusterResourceID := spc.ResourceID.Parent
	if clusterResourceID == nil {
		return "", utils.TrackError(fmt.Errorf("ServiceProviderCluster %s has no parent resource ID", spc.ResourceID.String()))
	}
	cluster, err := c.cosmosClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	// TODO We get the provision shard ID from CS but at some point we should have
	// the information in Cosmos and this should be changed to use that instead.
	// TODO should we take into account that at some point in the future we will implement migration between management
	// clusters, where a cluster could have bundles allocated to different provision shards at the same time? For now
	// we assume that the cluster is associated to a single provision shard at a time.
	clusterCSShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard: %w", err))
	}
	return clusterCSShard.ID(), nil
}

// clusterProvisionShardIDForServiceProviderNodePool returns the Cluster Service provision shard ID for the cluster that owns the node pool.
func (c *deleteOrphanedMaestroReadonlyBundles) clusterProvisionShardIDForServiceProviderNodePool(ctx context.Context, spnp *api.ServiceProviderNodePool) (string, error) {
	nodePoolResourceID := spnp.ResourceID.Parent
	if nodePoolResourceID == nil {
		return "", utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no parent resource ID", spnp.ResourceID.String()))
	}
	clusterResourceID := nodePoolResourceID.Parent
	if clusterResourceID == nil {
		return "", utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no grandparent cluster resource ID", spnp.ResourceID.String()))
	}
	cluster, err := c.cosmosClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	// TODO We get the provision shard ID from CS but at some point we should have
	// the information in Cosmos and this should be changed to use that instead.
	// TODO should we take into account that at some point in the future we will implement migration between management
	// clusters, where a cluster could have bundles allocated to different provision shards at the same time? For now
	// we assume that the cluster is associated to a single provision shard at a time.
	clusterCSShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard: %w", err))
	}
	return clusterCSShard.ID(), nil
}

// referencedMaestroAPIMaestroBundleNamesByShard maps provision shard ID to the set of Maestro API bundle names referenced by
// SPCs grouped under that shard (shard assignment is already resolved in spcsByShard). Nil list entries or empty
// maestroAPIMaestroBundleName return an error so the reference set cannot silently omit in-use bundles.
func referencedMaestroAPIMaestroBundleNamesByShard(spcsByShard map[string][]*api.ServiceProviderCluster) (map[string]map[string]struct{}, error) {
	out := make(map[string]map[string]struct{})

	for shardID, spcs := range spcsByShard {
		// If it is the first time we are processing this shard we initialize the map entry for it
		if out[shardID] == nil {
			out[shardID] = make(map[string]struct{})
		}
		// We iterate over the ServiceProviderClusters on the shard and we add the Maestro API Maestro bundle names to the map.
		for _, spc := range spcs {
			if spc == nil {
				return nil, utils.TrackError(fmt.Errorf("nil ServiceProviderCluster under provision shard %s", shardID))
			}
			for i, ref := range spc.Status.MaestroReadonlyBundles {
				if ref == nil {
					return nil, utils.TrackError(fmt.Errorf("serviceProviderCluster %s: MaestroReadonlyBundles[%d] is nil", spc.ResourceID.String(), i))
				}
				if ref.MaestroAPIMaestroBundleName == "" {
					return nil, utils.TrackError(fmt.Errorf("serviceProviderCluster %s: MaestroReadonlyBundles[%d] (internal name %q) has empty maestroAPIMaestroBundleName", spc.ResourceID.String(), i, ref.Name))
				}
				out[shardID][ref.MaestroAPIMaestroBundleName] = struct{}{}
			}
		}
	}

	return out, nil
}

// referencedMaestroAPIMaestroBundleNamesByShardFromNodePools maps provision shard ID to the set of Maestro API bundle names
// referenced by ServiceProviderNodePools on that shard.
func referencedMaestroAPIMaestroBundleNamesByShardFromNodePools(spnpsByShard map[string][]*api.ServiceProviderNodePool) (map[string]map[string]struct{}, error) {
	out := make(map[string]map[string]struct{})

	for shardID, spnps := range spnpsByShard {
		// If it is the first time we are processing this shard we initialize the map entry for it
		if out[shardID] == nil {
			out[shardID] = make(map[string]struct{})
		}
		// We iterate over the ServiceProviderNodePools on the shard and we add the Maestro API Maestro bundle names to the map.
		for _, spnp := range spnps {
			if spnp == nil {
				return nil, utils.TrackError(fmt.Errorf("nil ServiceProviderNodePool under provision shard %s", shardID))
			}
			for i, ref := range spnp.Status.MaestroReadonlyBundles {
				if ref == nil {
					return nil, utils.TrackError(fmt.Errorf("serviceProviderNodePool %s: MaestroReadonlyBundles[%d] is nil", spnp.ResourceID.String(), i))
				}
				if ref.MaestroAPIMaestroBundleName == "" {
					return nil, utils.TrackError(fmt.Errorf("serviceProviderNodePool %s: MaestroReadonlyBundles[%d] (internal name %q) has empty maestroAPIMaestroBundleName", spnp.ResourceID.String(), i, ref.Name))
				}
				out[shardID][ref.MaestroAPIMaestroBundleName] = struct{}{}
			}
		}
	}

	return out, nil
}

func (c *deleteOrphanedMaestroReadonlyBundles) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after one second
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	// We run this periodically enqueuing an arbitrary item named "doWork" to trigger the sync.
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("doWork") }, 10*time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *deleteOrphanedMaestroReadonlyBundles) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *deleteOrphanedMaestroReadonlyBundles) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}
