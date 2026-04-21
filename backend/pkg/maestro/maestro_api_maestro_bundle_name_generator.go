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
	"fmt"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// MaestroAPIMaestroBundleNameGenerator is an interface that defines a method to generate a new Maestro API Maestro Bundle name.
// The generated name must be globally unique within a given Maestro Consumer Name and Maestro Source ID.
// It can be used to generate a new Maestro API Maestro Bundle name for a new Maestro Bundle reference.
type MaestroAPIMaestroBundleNameGenerator interface {
	// NewMaestroAPIMaestroBundleName generates a new Maestro API Maestro Bundle name.
	// The generated name must be globally unique within a given Maestro Consumer Name and Maestro Source ID.
	NewMaestroAPIMaestroBundleName() (string, error)
}

// NewMaestroAPIMaestroBundleNameGenerator creates a new Maestro API Maestro Bundle name generator.
// The generator generates a new Maestro API Maestro Bundle name whose value is a UUIDv4.
func NewMaestroAPIMaestroBundleNameGenerator() MaestroAPIMaestroBundleNameGenerator {
	return &maestroAPIMaestroBundleNameGenerator{
		uuidV4Generator: uuid.NewRandom,
	}
}

type maestroAPIMaestroBundleNameGenerator struct {
	uuidV4Generator func() (uuid.UUID, error)
}

// generateNewMaestroAPIMaestroBundleName generates a new Maestro API Maestro Bundle name.
// Used to generate a new Maestro API Maestro Bundle name for a new Maestro Bundle reference.
// The generated name is a UUIDv4.
func (c *maestroAPIMaestroBundleNameGenerator) NewMaestroAPIMaestroBundleName() (string, error) {
	newUUIDForMaestroAPIMaestroBundleName, err := c.uuidV4Generator()
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to generate UUIDv4 for Maestro API Maestro Bundle name: %w", err))
	}
	return newUUIDForMaestroAPIMaestroBundleName.String(), nil
}

type alwaysSameNameMaestroAPIMaestroBundleNameGenerator struct {
	maestroAPIMaestroBundleName string
}

// NewAlwaysSameNameMaestroAPIMaestroBundleNameGenerator creates a new Maestro API Maestro Bundle name generator that always returns the same name.
// This is useful for testing purposes.
func NewAlwaysSameNameMaestroAPIMaestroBundleNameGenerator(maestroAPIMaestroBundleName string) MaestroAPIMaestroBundleNameGenerator {
	return &alwaysSameNameMaestroAPIMaestroBundleNameGenerator{
		maestroAPIMaestroBundleName: maestroAPIMaestroBundleName,
	}
}

func (c *alwaysSameNameMaestroAPIMaestroBundleNameGenerator) NewMaestroAPIMaestroBundleName() (string, error) {
	return c.maestroAPIMaestroBundleName, nil
}
