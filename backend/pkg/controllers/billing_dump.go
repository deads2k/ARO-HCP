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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type billingDump struct {
	cosmosClient database.DBClient
}

// noCooldownChecker is a simple cooldown checker that always allows syncing.
type noCooldownChecker struct{}

func (n *noCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

// NewBillingDumpController periodically dumps billing documents for each cluster.
func NewBillingDumpController(cosmosClient database.DBClient) controllerutils.ClusterSyncer {
	return &billingDump{
		cosmosClient: cosmosClient,
	}
}

func (c *billingDump) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	if err := serverutils.DumpBillingToLogger(ctx, c.cosmosClient, key.GetResourceID()); err != nil {
		// never fail, this is best effort
		logger.Error(err, "failed to dump billing to logger")
	}

	return nil
}

func (c *billingDump) CooldownChecker() controllerutils.CooldownChecker {
	return &noCooldownChecker{}
}
