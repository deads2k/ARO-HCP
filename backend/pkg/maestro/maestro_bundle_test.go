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
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestGetOrCreateMaestroBundle(t *testing.T) {
	desiredBundle := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-maestro-api-maestro-bundle-name",
			Namespace: "test-maestro-consumer",
		},
		Spec: workv1.ManifestWorkSpec{
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: workv1.ResourceIdentifier{
						Name:      "hostedcluster-name",
						Namespace: "ocm-testenv-11111111111111111111111111111111",
					},
				},
			},
		},
	}

	tests := []struct {
		name       string
		setupMock  func(*MockClient, *workv1.ManifestWork)
		wantBundle *workv1.ManifestWork
		wantErr    bool
		errSubstr  string
	}{
		{
			name: "returns existing bundle if it already exists",
			setupMock: func(m *MockClient, want *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(want, nil)
			},
			wantBundle: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-maestro-api-maestro-bundle-name", Namespace: "test-maestro-consumer", UID: "existing-uid",
				},
			},
		},
		{
			name: "creates new bundle if it does not exist",
			setupMock: func(m *MockClient, want *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				m.EXPECT().Create(gomock.Any(), desiredBundle, gomock.Any()).Return(want, nil)
			},
			wantBundle: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-maestro-api-maestro-bundle-name", Namespace: "test-maestro-consumer", UID: "new-uid",
				},
			},
		},
		{
			name: "returns existing bundle when internal call to create returns AlreadyExists and then the following get succeeds",
			setupMock: func(m *MockClient, want *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				m.EXPECT().Create(gomock.Any(), desiredBundle, gomock.Any()).Return(nil, k8serrors.NewAlreadyExists(schema.GroupResource{}, "test-maestro-api-maestro-bundle-name"))
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(want, nil)
			},
			wantBundle: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-maestro-api-maestro-bundle-name", Namespace: "test-maestro-consumer", UID: "existing-uid",
				},
			},
		},
		{
			name: "returns error if it fails to get the bundle",
			setupMock: func(m *MockClient, _ *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(nil, fmt.Errorf("connection error"))
			},
			wantErr:   true,
			errSubstr: "failed to get Maestro Bundle",
		},
		{
			name: "returns error if it fails to create the bundle",
			setupMock: func(m *MockClient, _ *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "test-maestro-api-maestro-bundle-name"))
				m.EXPECT().Create(gomock.Any(), desiredBundle, gomock.Any()).Return(nil, fmt.Errorf("maestro API error"))
			},
			wantErr:   true,
			errSubstr: "failed to create Maestro Bundle: maestro API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := NewMockClient(ctrl)
			tt.setupMock(mockMaestro, tt.wantBundle)

			result, err := GetOrCreateMaestroBundle(context.Background(), mockMaestro, desiredBundle)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantBundle, result)
			}
		})
	}
}
