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

package externalauthpropertiescontroller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testExternalAuthName    = "test-external-auth"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testExternalAuthCSIDStr = testClusterServiceIDStr + "/external_auth_config/external_auths/ea123"
	testMigrationIssuerURL  = "https://login.example.com/tenant/v2.0"
)

func TestExternalAuthCustomerPropertiesMigrationController_SyncOnce(t *testing.T) {
	testCases := []struct {
		name                       string
		cachedExternalAuth         *api.HCPOpenShiftClusterExternalAuth // nil means use same as existingCosmosExternalAuth
		existingCosmosExternalAuth *api.HCPOpenShiftClusterExternalAuth
		csExternalAuth             *arohcpv1alpha1.ExternalAuth
		csError                    error
		expectCSCall               bool
		expectError                bool
		expectedIssuerURL          string
	}{
		{
			name: "cache indicates no work needed - early return without cosmos lookup",
			cachedExternalAuth: newTestExternalAuthForMigration(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Issuer.URL = testMigrationIssuerURL
			}),
			existingCosmosExternalAuth: newTestExternalAuthForMigration(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Issuer.URL = testMigrationIssuerURL
			}),
			expectCSCall:      false,
			expectError:       false,
			expectedIssuerURL: testMigrationIssuerURL,
		},
		{
			name:               "cache says work needed but live data says no work needed",
			cachedExternalAuth: newTestExternalAuthForMigration(func(ea *api.HCPOpenShiftClusterExternalAuth) {}),
			existingCosmosExternalAuth: newTestExternalAuthForMigration(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Issuer.URL = testMigrationIssuerURL
			}),
			expectCSCall:      false,
			expectError:       false,
			expectedIssuerURL: testMigrationIssuerURL,
		},
		{
			name:                       "error reading from cluster-service",
			existingCosmosExternalAuth: newTestExternalAuthForMigration(func(ea *api.HCPOpenShiftClusterExternalAuth) {}),
			csError:                    fmt.Errorf("connection refused"),
			expectCSCall:               true,
			expectError:                true,
			expectedIssuerURL:          "",
		},
		{
			name:                       "success - migrate issuer URL when missing",
			existingCosmosExternalAuth: newTestExternalAuthForMigration(func(ea *api.HCPOpenShiftClusterExternalAuth) {}),
			csExternalAuth:             newTestFullCSExternalAuth(),
			expectCSCall:               true,
			expectError:                false,
			expectedIssuerURL:          testMigrationIssuerURL,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)

			mockDB := databasetesting.NewMockDBClient()

			externalAuthCRUD := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName)
			_, err := externalAuthCRUD.Create(ctx, tc.existingCosmosExternalAuth, nil)
			require.NoError(t, err)

			cachedExternalAuth := tc.cachedExternalAuth
			if cachedExternalAuth == nil {
				cachedExternalAuth = tc.existingCosmosExternalAuth
			}
			sliceExternalAuthLister := &listertesting.SliceExternalAuthLister{
				ExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{cachedExternalAuth},
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tc.expectCSCall {
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), api.Must(api.NewInternalID(testExternalAuthCSIDStr))).
					Return(tc.csExternalAuth, tc.csError)
			}

			syncer := &externalAuthCustomerPropertiesMigrationController{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				externalAuthLister:   sliceExternalAuthLister,
				cosmosClient:         mockDB,
				clusterServiceClient: mockCSClient,
			}

			key := controllerutils.HCPExternalAuthKey{
				SubscriptionID:      testSubscriptionID,
				ResourceGroupName:   testResourceGroupName,
				HCPClusterName:      testClusterName,
				HCPExternalAuthName: testExternalAuthName,
			}
			err = syncer.SyncOnce(ctx, key)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			updatedExternalAuth, err := externalAuthCRUD.Get(ctx, testExternalAuthName)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedIssuerURL, updatedExternalAuth.Properties.Issuer.URL)
		})
	}
}

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

func newTestExternalAuthForMigration(opts func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
	ea := newTestExternalAuthWithClusterServiceID()
	ea.Properties = api.HCPOpenShiftClusterExternalAuthProperties{}
	if opts != nil {
		opts(ea)
	}
	return ea
}

func newTestExternalAuthWithClusterServiceID() *api.HCPOpenShiftClusterExternalAuth {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/" + testExternalAuthName))
	csID := api.Must(api.NewInternalID(testExternalAuthCSIDStr))
	return &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.NewProxyResource(resourceID),
		ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID: csID,
		},
	}
}

func newTestFullCSExternalAuth() *arohcpv1alpha1.ExternalAuth {
	externalAuth, err := arohcpv1alpha1.NewExternalAuth().
		ID("ea123").
		HREF(testExternalAuthCSIDStr).
		Issuer(arohcpv1alpha1.NewTokenIssuer().
			URL(testMigrationIssuerURL).
			CA("testCAPem").
			Audiences(
				"87654321-4321-4321-4321-abcdefghijkl",
				"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			)).
		Claim(arohcpv1alpha1.NewExternalAuthClaim().
			Mappings(arohcpv1alpha1.NewTokenClaimMappings().
				UserName(arohcpv1alpha1.NewUsernameClaim().
					Claim("sub").
					Prefix("prefix-").
					PrefixPolicy("Prefix")).
				Groups(arohcpv1alpha1.NewGroupsClaim().
					Claim("groups").
					Prefix("grp-"))).
			ValidationRules(
				arohcpv1alpha1.NewTokenClaimValidationRule().
					Claim("tid").
					RequiredValue("expected-tenant"),
				arohcpv1alpha1.NewTokenClaimValidationRule().
					Claim("scp").
					RequiredValue("api.read"),
			)).
		Clients(
			arohcpv1alpha1.NewExternalAuthClientConfig().
				ID("11111111-1111-1111-1111-111111111111").
				Component(arohcpv1alpha1.NewClientComponent().
					Name("console").
					Namespace("openshift-console")).
				ExtraScopes("openid", "profile").
				Type(arohcpv1alpha1.ExternalAuthClientTypeConfidential),
			arohcpv1alpha1.NewExternalAuthClientConfig().
				ID("22222222-2222-2222-2222-222222222222").
				Component(arohcpv1alpha1.NewClientComponent().
					Name("cli").
					Namespace("openshift-console")).
				ExtraScopes("offline_access").
				Type(arohcpv1alpha1.ExternalAuthClientTypePublic),
		).
		Build()
	if err != nil {
		panic(err)
	}
	return externalAuth
}
