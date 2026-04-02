/*
Copyright 2025 The KubeFleet Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package approve

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestNormalizeKind(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clusterapprovalrequest lowercase",
			input: "clusterapprovalrequest",
			want:  "clusterapprovalrequest",
		},
		{
			name:  "clusterapprovalrequest uppercase",
			input: "CLUSTERAPPROVALREQUEST",
			want:  "clusterapprovalrequest",
		},
		{
			name:  "careq alias",
			input: "careq",
			want:  "clusterapprovalrequest",
		},
		{
			name:  "CAREQ alias uppercase",
			input: "CAREQ",
			want:  "clusterapprovalrequest",
		},
		{
			name:  "approvalrequest lowercase",
			input: "approvalrequest",
			want:  "approvalrequest",
		},
		{
			name:  "approvalrequest uppercase",
			input: "APPROVALREQUEST",
			want:  "approvalrequest",
		},
		{
			name:  "areq alias",
			input: "areq",
			want:  "approvalrequest",
		},
		{
			name:  "AREQ alias uppercase",
			input: "AREQ",
			want:  "approvalrequest",
		},
		{
			name:  "unknown kind preserved lowercase",
			input: "SomeOtherKind",
			want:  "someotherkind",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeKind(tc.input)
			if got != tc.want {
				t.Errorf("normalizeKind(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name       string
		opts       approveOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "empty kind should fail",
			opts: approveOptions{
				kind: "",
				name: "test-name",
			},
			wantErr:    true,
			wantErrMsg: "resource kind is required",
		},
		{
			name: "empty name should fail",
			opts: approveOptions{
				kind: kindClusterApprovalRequest,
				name: "",
			},
			wantErr:    true,
			wantErrMsg: "resource name is required",
		},
		{
			name: "unsupported kind should fail",
			opts: approveOptions{
				kind: "unsupported",
				name: "test-name",
			},
			wantErr:    true,
			wantErrMsg: "unsupported resource kind",
		},
		{
			name: "clusterapprovalrequest without namespace is valid",
			opts: approveOptions{
				kind: kindClusterApprovalRequest,
				name: "test-name",
			},
			wantErr: false,
		},
		{
			name: "approvalrequest without namespace should fail",
			opts: approveOptions{
				kind: kindApprovalRequest,
				name: "test-name",
			},
			wantErr:    true,
			wantErrMsg: "namespace is required for approvalrequest resources",
		},
		{
			name: "approvalrequest with namespace is valid",
			opts: approveOptions{
				kind:      kindApprovalRequest,
				name:      "test-name",
				namespace: "test-namespace",
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.validate()

			if tc.wantErr {
				if err == nil {
					t.Errorf("validate() = nil, want error")
					return
				}
				if tc.wantErrMsg != "" && !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Errorf("validate() error = %q, want error containing %q", err.Error(), tc.wantErrMsg)
				}
			} else if err != nil {
				t.Errorf("validate() = %v, want nil", err)
			}
		})
	}
}

func TestApproveClusterApprovalRequest(t *testing.T) {
	wantCondition := metav1.Condition{
		Type:    string(placementv1beta1.ApprovalRequestConditionApproved),
		Status:  metav1.ConditionTrue,
		Reason:  "ClusterApprovalRequestApproved",
		Message: "ClusterApprovalRequest has been approved",
	}

	tests := []struct {
		name                       string
		kind                       string
		requestName                string
		existingClusterApprovalReq *placementv1beta1.ClusterApprovalRequest
		wantCondition              *metav1.Condition
		wantErr                    bool
		wantErrMsg                 string
	}{
		{
			name:        "successfully approve ClusterApprovalRequest",
			kind:        kindClusterApprovalRequest,
			requestName: "test-approval",
			existingClusterApprovalReq: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval",
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{},
				},
			},
			wantCondition: &wantCondition,
			wantErr:       false,
		},
		{
			name:        "approve ClusterApprovalRequest using careq alias",
			kind:        kindClusterApprovalRequest,
			requestName: "test-approval-alias",
			existingClusterApprovalReq: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval-alias",
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{},
				},
			},
			wantCondition: &wantCondition,
			wantErr:       false,
		},
		{
			name:        "approve ClusterApprovalRequest with existing conditions",
			kind:        kindClusterApprovalRequest,
			requestName: "test-approval-existing",
			existingClusterApprovalReq: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval-existing",
					Generation: 2,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "SomeOtherCondition",
							Status: metav1.ConditionTrue,
							Reason: "SomeReason",
						},
					},
				},
			},
			wantCondition: &wantCondition,
			wantErr:       false,
		},
		{
			name:        "update existing Approved condition",
			kind:        kindClusterApprovalRequest,
			requestName: "test-approval-update",
			existingClusterApprovalReq: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval-update",
					Generation: 3,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(placementv1beta1.ApprovalRequestConditionApproved),
							Status:  metav1.ConditionFalse,
							Reason:  "OldReason",
							Message: "Old message",
						},
					},
				},
			},
			wantCondition: &wantCondition,
			wantErr:       false,
		},
		{
			name:                       "ClusterApprovalRequest not found",
			kind:                       kindClusterApprovalRequest,
			requestName:                "non-existent-approval",
			existingClusterApprovalReq: nil,
			wantErr:                    true,
			wantErrMsg:                 "not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := setupScheme(t)
			var objects []client.Object
			if tc.existingClusterApprovalReq != nil {
				objects = append(objects, tc.existingClusterApprovalReq)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&placementv1beta1.ClusterApprovalRequest{}).
				Build()

			o := &approveOptions{
				kind:      tc.kind,
				name:      tc.requestName,
				hubClient: fakeClient,
			}
			err := o.approveClusterApprovalRequest(context.Background())

			if tc.wantErr {
				if err == nil {
					t.Errorf("approveClusterApprovalRequest() = nil, want error")
					return
				}
				if tc.wantErrMsg != "" && !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Errorf("approveClusterApprovalRequest() error = %q, want error containing %q", err.Error(), tc.wantErrMsg)
				}
				return
			} else if err != nil {
				t.Errorf("approveClusterApprovalRequest() = %v, want nil", err)
				return
			}

			// Verify the ClusterApprovalRequest was updated correctly.
			var updatedCAR placementv1beta1.ClusterApprovalRequest
			err = fakeClient.Get(context.Background(), client.ObjectKey{Name: tc.requestName}, &updatedCAR)
			if err != nil {
				t.Errorf("failed to get updated ClusterApprovalRequest: %v", err)
				return
			}

			// Check that the Approved condition exists and is correct.
			approvedCondition := meta.FindStatusCondition(updatedCAR.Status.Conditions, tc.wantCondition.Type)
			if diff := cmp.Diff(tc.wantCondition, approvedCondition,
				cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime", "ObservedGeneration")); diff != "" {
				t.Errorf("condition mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestApproveApprovalRequest(t *testing.T) {
	wantCondition := metav1.Condition{
		Type:    string(placementv1beta1.ApprovalRequestConditionApproved),
		Status:  metav1.ConditionTrue,
		Reason:  "ApprovalRequestApproved",
		Message: "ApprovalRequest has been approved",
	}

	tests := []struct {
		name                string
		kind                string
		requestName         string
		namespace           string
		existingApprovalReq *placementv1beta1.ApprovalRequest
		wantCondition       *metav1.Condition
		wantErr             bool
		wantErrMsg          string
	}{
		{
			name:        "successfully approve ApprovalRequest",
			kind:        kindApprovalRequest,
			requestName: "test-approval",
			namespace:   "test-namespace",
			existingApprovalReq: &placementv1beta1.ApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval",
					Namespace:  "test-namespace",
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{},
				},
			},
			wantCondition: &wantCondition,
			wantErr:       false,
		},
		{
			name:        "approve ApprovalRequest using areq alias",
			kind:        kindApprovalRequest,
			requestName: "test-approval-alias",
			namespace:   "test-namespace",
			existingApprovalReq: &placementv1beta1.ApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval-alias",
					Namespace:  "test-namespace",
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{},
				},
			},
			wantCondition: &wantCondition,
			wantErr:       false,
		},
		{
			name:        "approve ApprovalRequest with existing conditions",
			kind:        kindApprovalRequest,
			requestName: "test-approval-existing",
			namespace:   "test-namespace",
			existingApprovalReq: &placementv1beta1.ApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval-existing",
					Namespace:  "test-namespace",
					Generation: 2,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "SomeOtherCondition",
							Status: metav1.ConditionTrue,
							Reason: "SomeReason",
						},
					},
				},
			},
			wantCondition: &wantCondition,
			wantErr:       false,
		},
		{
			name:        "update existing Approved condition",
			kind:        kindApprovalRequest,
			requestName: "test-approval-update",
			namespace:   "test-namespace",
			existingApprovalReq: &placementv1beta1.ApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval-update",
					Namespace:  "test-namespace",
					Generation: 3,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
				Status: placementv1beta1.ApprovalRequestStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(placementv1beta1.ApprovalRequestConditionApproved),
							Status:  metav1.ConditionFalse,
							Reason:  "OldReason",
							Message: "Old message",
						},
					},
				},
			},
			wantCondition: &wantCondition,
			wantErr:       false,
		},
		{
			name:                "ApprovalRequest not found",
			kind:                kindApprovalRequest,
			requestName:         "non-existent-approval",
			namespace:           "test-namespace",
			existingApprovalReq: nil,
			wantErr:             true,
			wantErrMsg:          "not found",
		},
		{
			name:        "ApprovalRequest in wrong namespace not found",
			kind:        kindApprovalRequest,
			requestName: "test-approval",
			namespace:   "wrong-namespace",
			existingApprovalReq: &placementv1beta1.ApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval",
					Namespace:  "test-namespace",
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
			},
			wantErr:    true,
			wantErrMsg: "not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := setupScheme(t)
			var objects []client.Object
			if tc.existingApprovalReq != nil {
				objects = append(objects, tc.existingApprovalReq)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&placementv1beta1.ApprovalRequest{}).
				Build()

			o := &approveOptions{
				kind:      tc.kind,
				name:      tc.requestName,
				namespace: tc.namespace,
				hubClient: fakeClient,
			}
			err := o.approveApprovalRequest(context.Background())

			if tc.wantErr {
				if err == nil {
					t.Errorf("approveApprovalRequest() = nil, want error")
					return
				}
				if tc.wantErrMsg != "" && !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Errorf("approveApprovalRequest() error = %q, want error containing %q", err.Error(), tc.wantErrMsg)
				}
				return
			} else if err != nil {
				t.Errorf("approveApprovalRequest() = %v, want nil", err)
				return
			}

			// Verify the ApprovalRequest was updated correctly.
			var updatedAR placementv1beta1.ApprovalRequest
			err = fakeClient.Get(context.Background(), client.ObjectKey{Name: tc.requestName, Namespace: tc.namespace}, &updatedAR)
			if err != nil {
				t.Errorf("failed to get updated ApprovalRequest: %v", err)
				return
			}

			// Check that the Approved condition exists and is correct.
			approvedCondition := meta.FindStatusCondition(updatedAR.Status.Conditions, tc.wantCondition.Type)
			if diff := cmp.Diff(tc.wantCondition, approvedCondition,
				cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime", "ObservedGeneration")); diff != "" {
				t.Errorf("condition mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRun(t *testing.T) {
	wantClusterCondition := metav1.Condition{
		Type:    string(placementv1beta1.ApprovalRequestConditionApproved),
		Status:  metav1.ConditionTrue,
		Reason:  "ClusterApprovalRequestApproved",
		Message: "ClusterApprovalRequest has been approved",
	}
	wantNamespacedCondition := metav1.Condition{
		Type:    string(placementv1beta1.ApprovalRequestConditionApproved),
		Status:  metav1.ConditionTrue,
		Reason:  "ApprovalRequestApproved",
		Message: "ApprovalRequest has been approved",
	}

	tests := []struct {
		name                       string
		kind                       string
		requestName                string
		namespace                  string
		existingClusterApprovalReq *placementv1beta1.ClusterApprovalRequest
		existingApprovalReq        *placementv1beta1.ApprovalRequest
		wantCondition              *metav1.Condition
		wantErr                    bool
		wantErrMsg                 string
	}{
		{
			name:        "run dispatches to ClusterApprovalRequest",
			kind:        kindClusterApprovalRequest,
			requestName: "test-approval",
			existingClusterApprovalReq: &placementv1beta1.ClusterApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval",
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
			},
			wantCondition: &wantClusterCondition,
			wantErr:       false,
		},
		{
			name:        "run dispatches to ApprovalRequest",
			kind:        kindApprovalRequest,
			requestName: "test-approval",
			namespace:   "test-namespace",
			existingApprovalReq: &placementv1beta1.ApprovalRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-approval",
					Namespace:  "test-namespace",
					Generation: 1,
				},
				Spec: placementv1beta1.ApprovalRequestSpec{
					TargetUpdateRun: "test-update-run",
					TargetStage:     "test-stage",
				},
			},
			wantCondition: &wantNamespacedCondition,
			wantErr:       false,
		},
		{
			name:        "unsupported kind returns error",
			kind:        "unsupportedkind",
			requestName: "test-approval",
			wantErr:     true,
			wantErrMsg:  "unsupported resource kind",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := setupScheme(t)
			var objects []client.Object
			if tc.existingClusterApprovalReq != nil {
				objects = append(objects, tc.existingClusterApprovalReq)
			}
			if tc.existingApprovalReq != nil {
				objects = append(objects, tc.existingApprovalReq)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&placementv1beta1.ClusterApprovalRequest{}, &placementv1beta1.ApprovalRequest{}).
				Build()

			o := &approveOptions{
				kind:      tc.kind,
				name:      tc.requestName,
				namespace: tc.namespace,
				hubClient: fakeClient,
			}
			err := o.run(context.Background())

			if tc.wantErr {
				if err == nil {
					t.Errorf("run() = nil, want error")
					return
				}
				if tc.wantErrMsg != "" && !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Errorf("run() error = %q, want error containing %q", err.Error(), tc.wantErrMsg)
				}
				return
			} else if err != nil {
				t.Errorf("run() = %v, want nil", err)
				return
			}

			// Verify the resource was updated correctly based on kind.
			if tc.kind == kindClusterApprovalRequest {
				var updatedCAR placementv1beta1.ClusterApprovalRequest
				err = fakeClient.Get(context.Background(), client.ObjectKey{Name: tc.requestName}, &updatedCAR)
				if err != nil {
					t.Errorf("failed to get updated ClusterApprovalRequest: %v", err)
					return
				}
				approvedCondition := meta.FindStatusCondition(updatedCAR.Status.Conditions, tc.wantCondition.Type)
				if diff := cmp.Diff(tc.wantCondition, approvedCondition,
					cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime", "ObservedGeneration")); diff != "" {
					t.Errorf("condition mismatch (-want +got):\n%s", diff)
				}
			} else if tc.kind == kindApprovalRequest {
				var updatedAR placementv1beta1.ApprovalRequest
				err = fakeClient.Get(context.Background(), client.ObjectKey{Name: tc.requestName, Namespace: tc.namespace}, &updatedAR)
				if err != nil {
					t.Errorf("failed to get updated ApprovalRequest: %v", err)
					return
				}
				approvedCondition := meta.FindStatusCondition(updatedAR.Status.Conditions, tc.wantCondition.Type)
				if diff := cmp.Diff(tc.wantCondition, approvedCondition,
					cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime", "ObservedGeneration")); diff != "" {
					t.Errorf("condition mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// setupScheme creates a scheme with the necessary APIs for testing.
func setupScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := placementv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add placement v1beta1 scheme: %v", err)
	}
	return scheme
}
