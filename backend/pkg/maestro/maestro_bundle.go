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

package maestro

import (
	"context"
	"fmt"

	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// getOrCreateMaestroBundle gets (or creates if it does not exist) a Maestro Bundle for a given Maestro Bundle namespaced name.
func GetOrCreateMaestroBundle(ctx context.Context, maestroClient Client, maestroBundle *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	logger := utils.LoggerFromContext(ctx)
	existingMaestroBundle, err := maestroClient.Get(ctx, maestroBundle.Name, metav1.GetOptions{})
	if err == nil {
		logger.Info(fmt.Sprintf("retrieved maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
		return existingMaestroBundle, nil
	}
	if !k8serrors.IsNotFound(err) {
		logger.Error(err, "failed to get Maestro Bundle and it is not already exists error")
		return nil, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle: %w", err))
	}

	logger.Info(fmt.Sprintf("attempting to create maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
	existingMaestroBundle, err = maestroClient.Create(ctx, maestroBundle, metav1.CreateOptions{})
	if err == nil {
		logger.Info(fmt.Sprintf("created maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
		return existingMaestroBundle, nil
	}
	if !k8serrors.IsAlreadyExists(err) {
		logger.Error(err, "failed to create Maestro Bundle and it is not already exists error")
		return nil, utils.TrackError(fmt.Errorf("failed to create Maestro Bundle: %w", err))
	}
	logger.Error(err, "failed to create Maestro Bundle because it returned already exists error. Attempting to get it again")
	existingMaestroBundle, err = maestroClient.Get(ctx, maestroBundle.Name, metav1.GetOptions{})
	return existingMaestroBundle, err
}

// ForEachMaestroBundle lists all Maestro Bundles across all pages matching opts using the Maestro client client and calls fn
// for each Maestro Bundle.
// Pagination can be controlled by setting the Limit attribute in opts.
// fn is called for each bundle in page order. If fn returns an error, iteration stops and the error is returned.
func ForEachMaestroBundle(ctx context.Context, client Client, opts metav1.ListOptions, fn func(*workv1.ManifestWork) error) error {
	for {
		bundles, err := client.List(ctx, opts)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to list Maestro Bundles: %w", err))
		}
		for i := range bundles.Items {
			if err := fn(&bundles.Items[i]); err != nil {
				return err
			}
		}
		token := bundles.GetContinue()
		if token == "" {
			break
		}
		opts.Continue = token
	}
	return nil
}
