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

package frontend

import (
	"context"
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ResourceGroupChecker checks if a resource group exists in Azure.
type ResourceGroupChecker interface {
	Exists(ctx context.Context, subscriptionID, resourceGroupName string) (bool, error)
}

type azureResourceGroupChecker struct {
	cred azcore.TokenCredential
	opts *azcorearm.ClientOptions
}

// NewAzureResourceGroupChecker creates a ResourceGroupChecker that validates resource group existence
func NewAzureResourceGroupChecker(cred azcore.TokenCredential, opts *azcorearm.ClientOptions) ResourceGroupChecker {
	return &azureResourceGroupChecker{cred: cred, opts: opts}
}

func (c *azureResourceGroupChecker) Exists(ctx context.Context, subscriptionID, resourceGroupName string) (bool, error) {
	client, err := armresources.NewResourceGroupsClient(subscriptionID, c.cred, c.opts)
	if err != nil {
		return false, err
	}
	_, err = client.Get(ctx, resourceGroupName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.ErrorCode == "ResourceGroupNotFound" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
