/*
Copyright 2026 The KubeFleet Authors.

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

package fieldindexers

import (
	"context"
	"fmt"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	placementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

const (
	// The field-based indexes set up for KubeFleet API objects on the KubeFleet member agent.
	//
	// Important: many KubeFleet components run under the assumption that proper custom fields
	// have been added and indexed in the cache when running. Failure to complete such prior setup **before
	// the manager starts** will result in unexpected behaviors. Make sure that all applicable components
	// are properly set up using the client provided by the hub controller manager, and `SetupWithManager` is
	// called before the manager starts.

	// WorkOwnedByPlacementBindingCustomFieldName is the name of the custom field that indexes
	// work objects by their owner placement bindings.
	//
	// This is added to help the work applier retrieve all the work objects associated with a placement binding
	// in one batch.
	WorkOwnedByPlacementBindingCustomFieldName = "ownedByPlacementBinding"
)

const (
	// The format of the custom field values for the field-based indexes defined above.

	// WorkOwnedByPlacementBindingCustomFieldValFormat is used to format the value for the custom field,
	// `WorkOwnedByPlacementBindingCustomFieldName`, in the form of `[OWNER-NAMESPACE]/[OWNER-NAME]`. The owner
	// namespace is empty for cluster-scoped placement bindings.
	WorkOwnedByPlacementBindingCustomFieldValFormat = "%s/%s"
)

// IndexWorkOwnedByPlacementBindingField indexes work objects by their
// owner placement binding.
func IndexWorkOwnedByPlacementBindingField(ctx context.Context, fieldIdxer client.FieldIndexer) error {
	if err := fieldIdxer.IndexField(ctx, &placementv1alpha1.Work{}, WorkOwnedByPlacementBindingCustomFieldName, func(rawObj client.Object) []string {
		work, ok := rawObj.(*placementv1alpha1.Work)
		if !ok {
			wrappedErr := errors.NewUnexpectedError(nil, "failed to convert object to work",
				"object", klog.KObj(rawObj))
			klog.ErrorS(wrappedErr, "failed to index work by owner placement binding", errors.Args(wrappedErr)...)
			return nil
		}

		// The value might have been truncated with a hash appended; lookups should be keyed on the label
		// value rather than the raw owner name.
		ownedBy := work.GetLabels()[placementv1alpha1.WorkOwnedByPlacementBindingLabelKey]
		if ownedBy == "" {
			wrappedErr := errors.NewUnexpectedError(nil, "work is missing the owner placement binding label",
				"work", klog.KObj(work))
			klog.ErrorS(wrappedErr, "failed to index work by owner placement binding", errors.Args(wrappedErr)...)
			return nil
		}
		// An empty owner namespace signals a cluster-scoped placement binding.
		ownerNS := work.GetLabels()[placementv1alpha1.WorkOwnerNamespaceLabelKey]

		v := fmt.Sprintf(WorkOwnedByPlacementBindingCustomFieldValFormat, ownerNS, ownedBy)
		return []string{v}
	}); err != nil {
		return errors.NewUnexpectedError(err, "failed to index work objects by owner placement binding")
	}
	return nil
}

// SetupWithMemberAgentManager sets up the field indices the KubeFleet member agent needs to run properly.
// It must be called before the manager starts.
func SetupWithMemberAgentManager(ctx context.Context, mgr ctrl.Manager) error {
	fieldIdxer := mgr.GetFieldIndexer()

	if err := IndexWorkOwnedByPlacementBindingField(ctx, fieldIdxer); err != nil {
		return errors.Wraps(err, "failed to set up work placement binding owner field index")
	}
	return nil
}
