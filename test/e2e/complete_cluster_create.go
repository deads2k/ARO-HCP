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

package e2e

import (
	"context"
	"embed"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

//go:embed test-artifacts
var TestArtifactsFS embed.FS

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create an HCP cluster using bicep templates",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "basic-hcp-cluster"
				customerNodePoolName             = "np-1"
			)
			ic := framework.InvocationContext()

			By("creating a resource group")
			resourceGroup, cleanupResourceGroup, err := ic.NewResourceGroup(ctx, "basic-create", ic.Location())
			DeferCleanup(func(ctx SpecContext) {
				err := cleanupResourceGroup(ctx)
				Expect(err).NotTo(HaveOccurred())
			}, NodeTimeout(45*time.Minute))
			Expect(err).NotTo(HaveOccurred())

			By("creating a prereqs in the resource group")
			infraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/customer-infra.json")),
				map[string]string{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the hcp cluster")
			networkSecurityGroupID, err := framework.GetOutputValueString(infraDeploymentResult, "networkSecurityGroupId")
			Expect(err).NotTo(HaveOccurred())
			subnetID, err := framework.GetOutputValueString(infraDeploymentResult, "subnetId")
			Expect(err).NotTo(HaveOccurred())
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/cluster.json")),
				map[string]string{
					"networkSecurityGroupId":   networkSecurityGroupID,
					"subnetId":                 subnetID,
					"clusterName":              customerClusterName,
					"managedResourceGroupName": managedResourceGroupName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			// TODO list all HCP clusters in the resource group cleanup and remove them there.
			DeferCleanup(func(ctx SpecContext) {
				err := framework.DeleteHCPCluster(
					ctx,
					ic.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())
			}, NodeTimeout(45*time.Minute))

			By("creating the node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/nodepool.json")),
				map[string]string{
					"clusterName":  customerClusterName,
					"nodePoolName": customerNodePoolName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

		})
})
