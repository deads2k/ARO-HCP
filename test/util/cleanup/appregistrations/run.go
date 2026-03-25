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

package appregistrations

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func (o *Options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("Listing owned expired app registrations")
	expiredApps, err := o.GraphClient.ListOwnedExpiredApplications(ctx)
	if err != nil {
		return fmt.Errorf("failed to list owned expired app registrations: %w", err)
	}

	if len(expiredApps) == 0 {
		logger.Info("No expired app registrations found")
		return nil
	}

	appObjectIDs := make([]string, 0, len(expiredApps))
	for _, app := range expiredApps {
		logger.Info("Found expired app registration", "clientID", app.AppID, "objectID", app.ID, "displayName", app.DisplayName)
		if !o.DryRun {
			appObjectIDs = append(appObjectIDs, app.ID)
		}
	}

	if o.DryRun {
		logger.Info("Dry run, not deleting", "count", len(expiredApps))
		return nil
	}

	logger.Info("Deleting owned expired app registrations", "count", len(appObjectIDs))
	if err := framework.CleanupAppRegistrations(ctx, o.GraphClient, appObjectIDs); err != nil {
		return fmt.Errorf("failed to delete app registrations: %w", err)
	}

	logger.Info("All expired app registrations successfully deleted", "count", len(appObjectIDs))
	return nil
}
