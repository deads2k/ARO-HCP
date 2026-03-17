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

package framework

import (
	"context"
	"fmt"
	"os"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/utils/ptr"
)

// Install Cilium helm chart using the helm Go SDK.
func InstallCiliumChart(ctx context.Context, chartVersion string, values map[string]any, kubeconfigContent, ciliumNamespace, clusterName string) error {
	const (
		releaseName   = "cilium"
		ciliumRepoURL = "https://helm.cilium.io/"
		chartName     = "cilium"
	)

	// generating kubeconfig file for helm client
	kubeconfigFile, err := os.CreateTemp("", "kubeconfig-cilium-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temp kubeconfig file: %w", err)
	}
	defer os.Remove(kubeconfigFile.Name())

	_, err = kubeconfigFile.WriteString(kubeconfigContent)
	if err != nil {
		return fmt.Errorf("failed to write kubeconfig content: %w", err)
	}

	if err := kubeconfigFile.Close(); err != nil {
		return fmt.Errorf("failed to close kubeconfig file: %w", err)
	}

	// Initialize helm action configuration with the kubeconfig
	actionCfg := &action.Configuration{}
	cliOpts := &genericclioptions.ConfigFlags{
		KubeConfig: ptr.To(kubeconfigFile.Name()),
		Namespace:  ptr.To(ciliumNamespace),
	}
	if err := actionCfg.Init(cliOpts, ciliumNamespace, ""); err != nil {
		return fmt.Errorf("failed to init helm action config: %w", err)
	}

	// Locate and download the chart from the Cilium repo
	installClient := action.NewInstall(actionCfg)
	installClient.ReleaseName = releaseName
	installClient.Namespace = ciliumNamespace
	installClient.RepoURL = ciliumRepoURL
	installClient.WaitStrategy = kube.HookOnlyStrategy
	installClient.Version = chartVersion

	settings := cli.New()
	chartPath, err := installClient.LocateChart(chartName, settings)
	if err != nil {
		return fmt.Errorf("failed to locate cilium chart: %w", err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load cilium chart: %w", err)
	}

	_, err = installClient.RunWithContext(ctx, chart, values)
	if err != nil {
		return fmt.Errorf("failed to install cilium chart: %w", err)
	}

	return nil
}
