package updaterun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
)

func TestValidateAfterStageTask(t *testing.T) {
	tests := []struct {
		name    string
		task    []placementv1beta1.StageTask
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid AfterTasks",
			task: []placementv1beta1.StageTask{
				{
					Type: placementv1beta1.StageTaskTypeApproval,
				},
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 5 * time.Minute}),
				},
			},
			wantErr: false,
		},
		{
			name: "invalid AfterTasks, same type of tasks",
			task: []placementv1beta1.StageTask{
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 1 * time.Minute}),
				},
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 5 * time.Minute}),
				},
			},
			wantErr: true,
			errMsg:  "afterStageTasks cannot have two tasks of the same type: TimedWait",
		},
		{
			name: "invalid AfterTasks, with nil duration for TimedWait",
			task: []placementv1beta1.StageTask{
				{
					Type: placementv1beta1.StageTaskTypeTimedWait,
				},
			},
			wantErr: true,
			errMsg:  "task 0 of type TimedWait has wait duration set to nil",
		},
		{
			name: "invalid AfterTasks, with zero duration for TimedWait",
			task: []placementv1beta1.StageTask{
				{
					Type:     placementv1beta1.StageTaskTypeTimedWait,
					WaitTime: ptr.To(metav1.Duration{Duration: 0 * time.Minute}),
				},
			},
			wantErr: true,
			errMsg:  "task 0 of type TimedWait has wait duration <= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAfterStageTask(tt.task)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateAfterStageTask() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if err.Error() != tt.errMsg {
					t.Errorf("validateAfterStageTask() error = %v, wantErr %v", err, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("validateAfterStageTask() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetResourceSnapshotObjs(t *testing.T) {
	ctx := context.Background()
	placementName := "test-placement"
	placementKey := types.NamespacedName{Name: placementName, Namespace: "test-namespace"}
	updateRunRef := klog.ObjectRef{
		Name:      "test-updaterun",
		Namespace: "test-namespace",
	}

	// Create test resource snapshots
	masterResourceSnapshot := &placementv1beta1.ClusterResourceSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      placementName + "-1-snapshot",
			Namespace: placementKey.Namespace,
			Labels: map[string]string{
				placementv1beta1.PlacementTrackingLabel:      placementName,
				placementv1beta1.ResourceIndexLabel:          "1",
				placementv1beta1.IsLatestSnapshotLabel:       "false",
				placementv1beta1.ResourceGroupHashAnnotation: "hash123",
			},
			Annotations: map[string]string{
				placementv1beta1.ResourceGroupHashAnnotation: "hash123",
			},
		},
	}

	tests := []struct {
		name              string
		updateRunSpec     *placementv1beta1.UpdateRunSpec
		resourceSnapshots []runtime.Object
		wantSnapshotCount int
		wantErr           bool
		wantErrMsg        string
	}{
		// negative cases only
		{
			name: "invalid resource snapshot index - non-numeric",
			updateRunSpec: &placementv1beta1.UpdateRunSpec{
				ResourceSnapshotIndex: "invalid",
			},
			resourceSnapshots: []runtime.Object{},
			wantSnapshotCount: 0,
			wantErr:           true,
			wantErrMsg:        "invalid resource snapshot index `invalid` provided, expected an integer >= 0",
		},
		{
			name: "invalid resource snapshot index - negative",
			updateRunSpec: &placementv1beta1.UpdateRunSpec{
				ResourceSnapshotIndex: "-1",
			},
			resourceSnapshots: []runtime.Object{},
			wantSnapshotCount: 0,
			wantErr:           true,
			wantErrMsg:        "invalid resource snapshot index `-1` provided, expected an integer >= 0",
		},
		{
			name: "no resource snapshots found for specific index",
			updateRunSpec: &placementv1beta1.UpdateRunSpec{
				ResourceSnapshotIndex: "999",
			},
			resourceSnapshots: []runtime.Object{
				masterResourceSnapshot, // has index "1", not "999"
			},
			wantSnapshotCount: 0,
			wantErr:           true,
			wantErrMsg:        fmt.Sprintf("no resourceSnapshots with index `999` found for placement `%s`", placementKey),
		},
		{
			name: "no latest resource snapshots found",
			updateRunSpec: &placementv1beta1.UpdateRunSpec{
				ResourceSnapshotIndex: "",
			},
			resourceSnapshots: []runtime.Object{}, // no snapshots
			wantSnapshotCount: 0,
			wantErr:           true,
			wantErrMsg:        fmt.Sprintf("no resourceSnapshots found for placement `%s`", placementKey),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with the test objects
			fakeClient := fake.NewClientBuilder().
				WithScheme(serviceScheme(t)).
				WithRuntimeObjects(tt.resourceSnapshots...).
				Build()

			// Create reconciler with fake client
			r := &Reconciler{
				Client: fakeClient,
			}

			// Call the function
			result, err := r.getResourceSnapshotObjs(ctx, tt.updateRunSpec, placementName, placementKey, updateRunRef)

			// Verify error expectations
			if tt.wantErr {
				if err == nil {
					t.Errorf("getResourceSnapshotObjs() error = nil, wantErr %v", tt.wantErr)
					return
				}
				// Check if the error message contains the expected substring
				if !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("getResourceSnapshotObjs() error = %v, want error containing %v", err, tt.wantErrMsg)
				}
				return
			}

			// Verify no error when not expected
			if err != nil {
				t.Errorf("getResourceSnapshotObjs() unexpected error = %v", err)
				return
			}

			// Verify result count
			if len(result) != tt.wantSnapshotCount {
				t.Errorf("getResourceSnapshotObjs() returned %d snapshots, want %d", len(result), tt.wantSnapshotCount)
				return
			}
		})
	}
}

func TestGetResourceSnapshotObjs_ListError(t *testing.T) {
	tests := []struct {
		name       string
		spec       *placementv1beta1.UpdateRunSpec
		wantErrMsg string
	}{
		{
			name: "list error simulation with resource index",
			spec: &placementv1beta1.UpdateRunSpec{
				ResourceSnapshotIndex: "1",
			},
			wantErrMsg: "Failed to list the resourceSnapshots associated with the placement for the given index",
		},
		{
			name: "list error simulation without resource index",
			spec: &placementv1beta1.UpdateRunSpec{
				ResourceSnapshotIndex: "",
			},
			wantErrMsg: "Failed to list the resourceSnapshots associated with the placement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			placementName := "test-placement"
			placementKey := types.NamespacedName{Name: placementName, Namespace: "test-namespace"}
			updateRunRef := klog.ObjectRef{
				Name:      "test-updaterun",
				Namespace: "test-namespace",
			}

			// Use interceptor to make Get calls fail.
			fakeClient := interceptor.NewClient(
				fake.NewClientBuilder().WithScheme(serviceScheme(t)).Build(),
				interceptor.Funcs{
					List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						return errors.New(tt.wantErrMsg)
					},
				},
			)
			r := &Reconciler{Client: fakeClient}

			_, err := r.getResourceSnapshotObjs(ctx, tt.spec, placementName, placementKey, updateRunRef)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("expected simulated list error, got: %v", err)
			}
		})
	}
}

func serviceScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := placementv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add placement v1beta1 scheme: %v", err)
	}
	return scheme
}
