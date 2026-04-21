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
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaestroAPIMaestroBundleNameGenerator_NewMaestroAPIMaestroBundleName(t *testing.T) {
	generator := NewMaestroAPIMaestroBundleNameGenerator()

	// Test successful generation
	name1, err := generator.NewMaestroAPIMaestroBundleName()
	require.NoError(t, err)
	assert.NotEmpty(t, name1)

	// Verify it's a valid UUID
	_, err = uuid.Parse(name1)
	assert.NoError(t, err, "Generated name should be a valid UUID")

	// Test that multiple calls generate different UUIDs
	name2, err := generator.NewMaestroAPIMaestroBundleName()
	require.NoError(t, err)
	assert.NotEqual(t, name1, name2, "Multiple calls should generate different UUIDs")

	// Verify second name is also a valid UUID
	_, err = uuid.Parse(name2)
	assert.NoError(t, err, "Second generated name should also be a valid UUID")
}
