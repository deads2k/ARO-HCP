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

package rollout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ControlPlaneRolloutPinnedVersionSeedingControllerName is the single source of the
// controller name used for metrics, logging, and controller docs.
const ControlPlaneRolloutPinnedVersionSeedingControllerName = "ControlPlaneRolloutPinnedVersionSeeding"

// rolloutPinnedVersionSeedingSyncer copies an active ControlPlaneVersionRollout
// pin onto each channel member's ServiceProviderCluster.PinnedVersion so the
// Forced Cluster Desired Version Assignment controller can drive every cluster
// to the pin immediately. It never clears pins; forced assignment releases them
// once BestExactVersion exceeds UntilExactVersion.
type rolloutPinnedVersionSeedingSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clusterLister                corelisters.ClusterLister
}

// NewControlPlaneRolloutPinnedVersionSeedingController watches rollout channels
// and seeds matching ServiceProviderCluster pins while the control-plane rollout
// pin remains active.
func NewControlPlaneRolloutPinnedVersionSeedingController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	fleetInformers fleetinformers.FleetInformers,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	clusterInformer, clusterLister := informers.Clusters()
	serviceProviderClusterInformer, serviceProviderClusterLister := informers.ServiceProviderClusters()
	syncer := &rolloutPinnedVersionSeedingSyncer{
		resourcesDBClient:            resourcesDBClient,
		rolloutLister:                rolloutLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
	}
	controller := controllerutils.NewControlPlaneVersionRolloutWatchingController(
		ControlPlaneRolloutPinnedVersionSeedingControllerName, fleetInformers, 5*time.Minute, syncer)
	controller.AddInterestingResource(fleetapi.ControlPlaneVersionRolloutResourceType, "*")
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)

	if err := syncer.watchPinSeedingCandidates(clusterInformer, serviceProviderClusterInformer, controller); err != nil {
		panic(err) // coding error
	}
	return controller
}

func (c *rolloutPinnedVersionSeedingSyncer) needsWork(rollout *fleetapi.ControlPlaneVersionRollout) bool {
	return hasActiveRolloutPin(&rollout.Spec)
}

// SyncOnce seeds ServiceProviderCluster pins for one rollout channel when the
// control-plane rollout pin is still active.
func (c *rolloutPinnedVersionSeedingSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	logger := utils.LoggerFromContext(ctx)

	rollout, err := c.rolloutLister.Get(ctx, key.YStreamChannel)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Skipping sync because watched resource was not found")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}
	if !c.needsWork(rollout) {
		logger.Info("No active control plane rollout pin; nothing to seed")
		return nil
	}

	serviceProviderClusters, err := serviceProviderClustersForChannel(ctx, c.serviceProviderClusterLister, c.clusterLister, key.YStreamChannel)
	if err != nil {
		return utils.TrackError(err)
	}
	desiredPin := serviceProviderClusterPinnedVersionFromRollout(rollout.Spec.PinnedVersion)
	logger.Info("Seeding ServiceProviderCluster pins from control plane rollout pin",
		"channelMemberCount", len(serviceProviderClusters),
		"pinnedVersion", versionString(desiredPin.ExactVersion),
		"untilVersion", versionString(desiredPin.UntilExactVersion))

	var seedErrs []error
	for _, serviceProviderCluster := range serviceProviderClusters {
		if equality.Semantic.DeepEqual(serviceProviderCluster.Spec.PinnedVersion, desiredPin) {
			continue
		}
		if err := c.seedPinnedVersion(ctx, serviceProviderCluster, desiredPin); err != nil {
			logger.Error(err, "Failed to seed ServiceProviderCluster pin; continuing with remaining members",
				"resourceID", serviceProviderCluster.ResourceID)
			seedErrs = append(seedErrs, err)
		}
	}
	return utils.TrackError(errors.Join(seedErrs...))
}

func (c *rolloutPinnedVersionSeedingSyncer) seedPinnedVersion(ctx context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster, desiredPin coreapi.ServiceProviderClusterPinnedVersion) error {
	logger := utils.LoggerFromContext(ctx).WithValues("resourceID", serviceProviderCluster.ResourceID)
	subscription, resourceGroup, cluster, ok := serviceProviderClusterCoords(serviceProviderCluster)
	if !ok {
		return fmt.Errorf("ServiceProviderCluster is missing a cluster-scoped resource ID")
	}
	replacement := serviceProviderCluster.DeepCopy()
	replacement.Spec.PinnedVersion = desiredPin
	logger.Info("Seeding ServiceProviderCluster pin from control plane rollout pin",
		"previousPinnedVersion", versionString(serviceProviderCluster.Spec.PinnedVersion.ExactVersion),
		"previousUntilVersion", versionString(serviceProviderCluster.Spec.PinnedVersion.UntilExactVersion),
		"pinnedVersion", versionString(desiredPin.ExactVersion),
		"untilVersion", versionString(desiredPin.UntilExactVersion))
	if _, err := c.resourcesDBClient.ServiceProviderClusters(subscription, resourceGroup, cluster).Replace(ctx, replacement, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		logger.Info("Write conflicted; waiting for informer to provide current resource")
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to replace ServiceProviderCluster %s/%s/%s: %w", subscription, resourceGroup, cluster, err)
	}
	logger.Info("Persisted ServiceProviderCluster pin from control plane rollout pin")
	return nil
}

// serviceProviderClusterPinnedVersionFromRollout copies a fleet pin onto the
// per-cluster pin shape without sharing pointers with the rollout document.
func serviceProviderClusterPinnedVersionFromRollout(pin fleetapi.ServiceProviderClusterPinnedVersion) coreapi.ServiceProviderClusterPinnedVersion {
	out := coreapi.ServiceProviderClusterPinnedVersion{}
	if pin.ExactVersion != nil {
		exact := *pin.ExactVersion
		out.ExactVersion = &exact
	}
	if pin.UntilExactVersion != nil {
		until := *pin.UntilExactVersion
		out.UntilExactVersion = &until
	}
	return out
}

// watchPinSeedingCandidates wakes pin seeding when clusters join or move channels
// so an active fleet pin reaches new members promptly.
func (c *rolloutPinnedVersionSeedingSyncer) watchPinSeedingCandidates(clusters, serviceProviderClusters controllerutils.Notifier, queue controllerutils.Enqueuer) error {
	clusterHandler, serviceProviderClusterHandler := c.pinSeedingHandlers(queue)
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(ControlPlaneRolloutPinnedVersionSeedingControllerName)...)
	options := cache.HandlerOptions{Logger: &logger, ResyncPeriod: ptr.To(time.Duration(0))}
	_, clusterErr := clusters.AddEventHandlerWithOptions(clusterHandler, options)
	_, serviceProviderClusterErr := serviceProviderClusters.AddEventHandlerWithOptions(serviceProviderClusterHandler, options)
	return errors.Join(clusterErr, serviceProviderClusterErr)
}

func (c *rolloutPinnedVersionSeedingSyncer) pinSeedingHandlers(queue controllerutils.Enqueuer) (cache.ResourceEventHandlerFuncs, cache.ResourceEventHandlerFuncs) {
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(ControlPlaneRolloutPinnedVersionSeedingControllerName)...)
	enqueue := func(cluster *coreapi.Cluster, reason string) {
		yStreamChannel, ok := clusterYStreamChannel(cluster)
		if !ok {
			logger.Info("Cannot enqueue pin seeding: candidate channel is unknown", "resourceID", cluster.ID, "requestedVersion", cluster.CustomerProperties.Version.ID, "channelGroup", cluster.CustomerProperties.Version.ChannelGroup)
			return
		}
		logger.Info("Enqueuing pin seeding for candidate channel", "resourceID", cluster.ID, "ystreamChannel", yStreamChannel, "reason", reason)
		queue.Enqueue(controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: yStreamChannel})
	}
	onClusterAdd := func(obj any) {
		if cluster, ok := obj.(*coreapi.Cluster); ok {
			enqueue(cluster, "cluster added")
		}
	}
	onServiceProviderCluster := func(obj any) {
		serviceProviderCluster, ok := obj.(*coreapi.ServiceProviderCluster)
		if !ok {
			return
		}
		subscription, resourceGroup, name, ok := serviceProviderClusterCoords(serviceProviderCluster)
		if !ok {
			logger.Info("Cannot enqueue pin seeding: ServiceProviderCluster has no cluster resource ID", "resourceID", serviceProviderCluster.ResourceID)
			return
		}
		cluster, err := c.clusterLister.Get(context.Background(), subscription, resourceGroup, name)
		if err != nil {
			logger.Error(err, "Cannot resolve candidate channel for ServiceProviderCluster; cluster events or rollout resync will retry", "resourceID", serviceProviderCluster.ResourceID)
			return
		}
		enqueue(cluster, "ServiceProviderCluster observed")
	}
	return cache.ResourceEventHandlerFuncs{
			AddFunc: onClusterAdd,
			UpdateFunc: func(oldObj, newObj any) {
				oldCluster, oldOK := oldObj.(*coreapi.Cluster)
				newCluster, newOK := newObj.(*coreapi.Cluster)
				if !oldOK || !newOK {
					return
				}
				oldChannel, oldValid := clusterYStreamChannel(oldCluster)
				newChannel, newValid := clusterYStreamChannel(newCluster)
				if newValid && (!oldValid || oldChannel != newChannel) {
					enqueue(newCluster, "cluster candidate channel changed")
				}
			},
		}, cache.ResourceEventHandlerFuncs{
			AddFunc:    onServiceProviderCluster,
			UpdateFunc: func(_, newObj any) { onServiceProviderCluster(newObj) },
		}
}
