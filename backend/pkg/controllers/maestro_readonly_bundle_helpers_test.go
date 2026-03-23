// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	workv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api"
)

// buildTestMaestroBundleWithStatusFeedback builds a ManifestWork with exactly one resource status manifest
// and one status feedback value named "resource" with JsonRaw type.
func buildTestMaestroBundleWithStatusFeedback(name, namespace, rawJSON string) *workv1.ManifestWork {
	jsonRaw := rawJSON
	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status: workv1.ManifestWorkStatus{
			ResourceStatus: workv1.ManifestResourceStatus{
				Manifests: []workv1.ManifestCondition{
					{
						ResourceMeta: workv1.ManifestResourceMeta{
							Group:     "hypershift.openshift.io",
							Version:   "v1beta1",
							Kind:      "HostedCluster",
							Name:      "test-hc",
							Namespace: "test-ns",
						},
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{
									Name: "resource",
									Value: workv1.FieldValue{
										Type:    workv1.JsonRaw,
										JsonRaw: &jsonRaw,
									},
								},
							},
						},
						Conditions: []metav1.Condition{},
					},
				},
			},
		},
	}
}

func TestMaestroReadonlyBundleHelpers_buildDegradedCondition(t *testing.T) {
	cond := buildDegradedCondition(api.ConditionTrue, "MaestroBundleNotFound", "bundle not found")
	assert.Equal(t, "Degraded", cond.Type)
	assert.Equal(t, api.ConditionTrue, cond.Status)
	assert.Equal(t, "MaestroBundleNotFound", cond.Reason)
	assert.Equal(t, "bundle not found", cond.Message)

	condFalse := buildDegradedCondition(api.ConditionFalse, "", "")
	assert.Equal(t, api.ConditionFalse, condFalse.Status)
	assert.Empty(t, condFalse.Reason)
	assert.Empty(t, condFalse.Message)
}

func TestMaestroReadonlyBundleHelpers_buildObjectsFromUnstructuredObj(t *testing.T) {
	t.Run("single object returns one item", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "HostedCluster"})
		obj.SetName("test-hc")
		obj.SetNamespace("test-ns")

		objs, err := buildObjectsFromUnstructuredObj(obj)
		require.NoError(t, err)
		require.Len(t, objs, 1)
		assert.Equal(t, obj, objs[0].Object)
	})

	t.Run("list object flattens items", func(t *testing.T) {
		// Build an Unstructured that represents a K8s ConfigMapList with two ConfigMap items.
		item1 := map[string]interface{}{"kind": "HostedClusterList", "metadata": map[string]interface{}{"name": "cm1"}}
		item2 := map[string]interface{}{"kind": "HostedClusterList", "metadata": map[string]interface{}{"name": "cm2"}}
		listObj := &unstructured.Unstructured{}
		listObj.SetUnstructuredContent(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMapList",
			"items":      []interface{}{item1, item2},
		})
		objs, err := buildObjectsFromUnstructuredObj(listObj)
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// RawExtension has Object set (not Raw) when coming from buildObjectsFromUnstructuredObj; unmarshal to typed.
		require.NotNil(t, objs[0].Object, "Object should be set")
		u1 := objs[0].Object.(*unstructured.Unstructured)
		cm1 := &hsv1beta1.HostedCluster{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(u1.UnstructuredContent(), cm1)
		require.NoError(t, err)
		assert.Equal(t, "cm1", cm1.Name)

		u2 := objs[1].Object.(*unstructured.Unstructured)
		cm2 := &hsv1beta1.HostedCluster{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(u2.UnstructuredContent(), cm2)
		require.NoError(t, err)
		assert.Equal(t, "cm2", cm2.Name)
	})
}

func TestMaestroReadonlyBundleHelpers_getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(t *testing.T) {
	validJSON := `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test"}}`

	tests := []struct {
		name    string
		bundle  *workv1.ManifestWork
		want    string
		wantErr bool
		errSub  string
	}{
		{
			name:   "success - returns raw JSON",
			bundle: buildTestMaestroBundleWithStatusFeedback("bundle-1", "ns", validJSON),
			want:   validJSON,
		},
		{
			name: "error - zero manifests",
			bundle: &workv1.ManifestWork{
				Status: workv1.ManifestWorkStatus{
					ResourceStatus: workv1.ManifestResourceStatus{
						Manifests: []workv1.ManifestCondition{},
					},
				},
			},
			wantErr: true,
			errSub:  "expected exactly one resource within the Maestro Bundle, got 0",
		},
		{
			name: "error - two manifests",
			bundle: &workv1.ManifestWork{
				Status: workv1.ManifestWorkStatus{
					ResourceStatus: workv1.ManifestResourceStatus{
						Manifests: []workv1.ManifestCondition{
							{ResourceMeta: workv1.ManifestResourceMeta{}, Conditions: []metav1.Condition{}},
							{ResourceMeta: workv1.ManifestResourceMeta{}, Conditions: []metav1.Condition{}},
						},
					},
				},
			},
			wantErr: true,
			errSub:  "expected exactly one resource within the Maestro Bundle, got 2",
		},
		{
			name: "error - zero status feedback values",
			bundle: &workv1.ManifestWork{
				Status: workv1.ManifestWorkStatus{
					ResourceStatus: workv1.ManifestResourceStatus{
						Manifests: []workv1.ManifestCondition{
							{
								ResourceMeta:    workv1.ManifestResourceMeta{},
								StatusFeedbacks: workv1.StatusFeedbackResult{Values: []workv1.FeedbackValue{}},
								Conditions:      []metav1.Condition{},
							},
						},
					},
				},
			},
			wantErr: true,
			errSub:  "expected exactly one status feedback value",
		},
		{
			name: "error - wrong feedback name",
			bundle: func() *workv1.ManifestWork {
				b := buildTestMaestroBundleWithStatusFeedback("b", "ns", validJSON)
				b.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0].Name = "wrong"
				return b
			}(),
			wantErr: true,
			errSub:  "expected status feedback value name to be 'resource', got wrong",
		},
		{
			name: "error - wrong feedback type",
			bundle: func() *workv1.ManifestWork {
				b := buildTestMaestroBundleWithStatusFeedback("b", "ns", validJSON)
				b.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0].Value.Type = workv1.String
				return b
			}(),
			wantErr: true,
			errSub:  "expected status feedback value type to be JsonRaw",
		},
		{
			name: "error - nil JsonRaw",
			bundle: func() *workv1.ManifestWork {
				b := buildTestMaestroBundleWithStatusFeedback("b", "ns", validJSON)
				b.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0].Value.JsonRaw = nil
				return b
			}(),
			wantErr: true,
			errSub:  "expected status feedback value JsonRaw to be not nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(tt.bundle)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSub)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, string(got))
			}
		})
	}
}
