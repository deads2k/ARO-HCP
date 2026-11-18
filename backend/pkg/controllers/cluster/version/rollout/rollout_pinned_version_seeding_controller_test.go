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
	"testing"

	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestRolloutPinnedVersionSeeding(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		exact, until, best           string
		existingPin                  *coreapi.ServiceProviderClusterPinnedVersion
		wantExact, wantUntil         string
		wantUnchanged, expectNoExact bool
	}{
		{name: "seeds active pin", exact: "4.20.34", until: "4.20.40", best: "4.20.36", wantExact: "4.20.34", wantUntil: "4.20.40"},
		{name: "updates differing pin", exact: "4.20.34", until: "4.20.40", best: "4.20.36", existingPin: pin("4.20.30", "4.20.35"), wantExact: "4.20.34", wantUntil: "4.20.40"},
		{name: "skips matching pin", exact: "4.20.34", until: "4.20.40", best: "4.20.36", existingPin: pin("4.20.34", "4.20.40"), wantExact: "4.20.34", wantUntil: "4.20.40", wantUnchanged: true},
		{name: "inactive when best exceeds until", exact: "4.20.34", until: "4.20.34", best: "4.20.35", existingPin: pin("4.20.30", "4.20.33"), wantExact: "4.20.30", wantUntil: "4.20.33", wantUnchanged: true},
		{name: "no pin does nothing", best: "4.20.36", expectNoExact: true, wantUnchanged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cluster := newTestCluster("seed", "stable", "4.20")
			provider := newTestServiceProviderCluster("seed", v("4.20.36"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.20.36")}, tc.existingPin)
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, provider})
			require.NoError(t, err)
			before, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, "seed").Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			rollout := newTestRollout("stable-4.20", v(tc.best), fleetapi.ControlPlaneVersionRolloutStatus{})
			if tc.exact != "" {
				rollout.Spec.PinnedVersion.ExactVersion = v(tc.exact)
			}
			if tc.until != "" {
				rollout.Spec.PinnedVersion.UntilExactVersion = v(tc.until)
			}
			_, rolloutLister := newTestRolloutStore(t, rollout)
			syncer := &rolloutPinnedVersionSeedingSyncer{
				resourcesDBClient:            db,
				rolloutLister:                rolloutLister,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: db},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db},
			}
			require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.20"}))
			after, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, "seed").Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			if tc.expectNoExact {
				require.Nil(t, after.Spec.PinnedVersion.ExactVersion)
			} else {
				require.Equal(t, v(tc.wantExact), after.Spec.PinnedVersion.ExactVersion)
				if tc.wantUntil == "" {
					require.Nil(t, after.Spec.PinnedVersion.UntilExactVersion)
				} else {
					require.Equal(t, v(tc.wantUntil), after.Spec.PinnedVersion.UntilExactVersion)
				}
			}
			if tc.wantUnchanged {
				require.Equal(t, before, after)
			} else {
				require.NotEqual(t, before.Spec.PinnedVersion, after.Spec.PinnedVersion)
			}
		})
	}
}

func TestFleetPinSeedsThenForcedAssigns(t *testing.T) {
	ctx := context.Background()
	cluster := newTestCluster("fleet-pin", "stable", "4.20")
	provider := newTestServiceProviderCluster("fleet-pin", v("4.20.36"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.20.36")}, nil)
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, provider})
	require.NoError(t, err)
	rollout := newTestRollout("stable-4.20", v("4.20.36"), fleetapi.ControlPlaneVersionRolloutStatus{})
	rollout.Spec.PinnedVersion.ExactVersion = v("4.20.34")
	rollout.Spec.PinnedVersion.UntilExactVersion = v("4.20.40")
	_, rolloutLister := newTestRolloutStore(t, rollout)
	clusterLister := &corelistertesting.DBClusterLister{ResourcesDBClient: db}
	providerLister := &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db}
	clock := clocktesting.NewFakeClock(statusTestNow)

	seed := &rolloutPinnedVersionSeedingSyncer{
		resourcesDBClient: db, rolloutLister: rolloutLister,
		clusterLister: clusterLister, serviceProviderClusterLister: providerLister,
	}
	require.NoError(t, seed.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.20"}))

	forced := &forcedClusterDesiredVersionSyncer{
		clock: clock, resourcesDBClient: db, clusterLister: clusterLister,
		serviceProviderClusterLister: providerLister, rolloutLister: rolloutLister,
	}
	require.NoError(t, forced.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: "fleet-pin",
	}))

	after, err := providerLister.Get(ctx, testSubscriptionID, testResourceGroupName, "fleet-pin")
	require.NoError(t, err)
	require.Equal(t, v("4.20.34"), after.Spec.PinnedVersion.ExactVersion)
	require.Equal(t, v("4.20.40"), after.Spec.PinnedVersion.UntilExactVersion)
	require.Equal(t, v("4.20.34"), after.Spec.ControlPlaneVersion.DesiredVersion)
	require.NotNil(t, after.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime)
	require.True(t, statusTestNow.Equal(after.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime.Time))
}
