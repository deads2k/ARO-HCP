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

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

type RawOptions struct {
	DryRun bool
}

type ValidatedOptions struct {
	*RawOptions
}

type Options struct {
	DryRun      bool
	GraphClient *graphutil.Client
}

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	return &ValidatedOptions{RawOptions: o}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	tc := framework.NewTestContext()

	graphClient, err := tc.GetGraphClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get graph client: %w", err)
	}

	return &Options{
		DryRun:      o.DryRun,
		GraphClient: graphClient,
	}, nil
}
