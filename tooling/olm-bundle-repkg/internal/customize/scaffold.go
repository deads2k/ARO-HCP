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

package customize

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func LoadScaffoldTemplates(scaffoldDir string) ([]unstructured.Unstructured, error) {
	// If no scaffold directory is provided, return empty slice
	if scaffoldDir == "" {
		return []unstructured.Unstructured{}, nil
	}

	// If a scaffold directory is explicitly provided, it must exist
	if _, err := os.Stat(scaffoldDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("scaffold directory does not exist: %s", scaffoldDir)
	}

	var manifests []unstructured.Unstructured
	err := filepath.Walk(scaffoldDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			fileContent, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			mapContent := make(map[string]interface{})
			err = yaml.Unmarshal(fileContent, &mapContent)
			if err != nil {
				return err
			}

			manifests = append(manifests, convertMapToUnstructured(mapContent))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifests, nil
}
