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

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"k8s.io/kube-openapi/pkg/common"
	"k8s.io/kube-openapi/pkg/util"
	"k8s.io/kube-openapi/pkg/validation/spec"

	openapi "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/openapi"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <output-file>\n", os.Args[0])
		os.Exit(1)
	}

	refFunc := func(name string) spec.Ref {
		return spec.MustCreateRef("#/definitions/" + common.EscapeJsonPointer(util.ToRESTFriendlyName(name)))
	}

	defs := openapi.GetOpenAPIDefinitions(refFunc)

	swagger := spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Info: &spec.Info{
				InfoProps: spec.InfoProps{
					Title:   "sessiongate",
					Version: "v1alpha1",
				},
			},
			Swagger:     "2.0",
			Definitions: make(spec.Definitions),
		},
	}

	for name, def := range defs {
		swagger.Definitions[util.ToRESTFriendlyName(name)] = def.Schema
	}

	data, err := json.MarshalIndent(swagger, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal swagger: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(os.Args[1], data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write file: %v\n", err)
		os.Exit(1)
	}
}
