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

package admissionpolicymanager

import (
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	kfplacementv1alpha1 "github.com/kubefleet-dev/kubefleet/apis/kubefleet.dev/placement/v1alpha1"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

const (
	ClusterClaimApprovalVAPGeneratorName = "RestrictClusterClaimApproval"
)

const (
	clusterClaimApprovalVAPPolicyName        = "restrict-clusterclaim-approval"
	clusterClaimApprovalVAPPolicyBindingName = "restrict-clusterclaim-approval-binding"

	// approveVerb is the RBAC verb on clusterclaims that admits a requester to the Approved
	// condition; it is a virtual verb, the way "approve" on certificatesigningrequests is.
	approveVerb = "approve"
)

// Verify that ClusterClaimApprovalValidatingAdmissionPolicyGenerator implements the
// ValidatingAdmissionPolicyGenerator interface.
var _ ValidatingAdmissionPolicyGenerator = &ClusterClaimApprovalValidatingAdmissionPolicyGenerator{}

// ClusterClaimApprovalValidatingAdmissionPolicyGenerator generates a ValidatingAdmissionPolicy
// and its binding that reserve the Approved condition of a ClusterClaim to approvers: a status
// update that adds, removes, or changes the Approved condition is denied unless the requester is
// authorized for the "approve" verb on that ClusterClaim, the way CertificateSigningRequest
// approval is gated. Approvers are therefore managed in RBAC -- granted and revoked live, and
// scoped per claim name if wanted -- and the hub agent, which stamps automatic approvals, needs the
// same verb in its ClusterRole; cluster admins hold every verb already. Any other status write,
// such as a provider accepting or completing the claim, passes as long as it leaves the Approved
// condition as it found it.
//
// The generator is not part of the default configuration: enabling it before the hub agent's
// ClusterRole carries the approve verb would deny the hub agent's own automatic approvals.
type ClusterClaimApprovalValidatingAdmissionPolicyGenerator struct{}

// Name returns the name of the generator, which is used to determine if a specific generator
// has been enabled or not.
func (g *ClusterClaimApprovalValidatingAdmissionPolicyGenerator) Name() string {
	return ClusterClaimApprovalVAPGeneratorName
}

// Validate validates the configuration of the generator, which has nothing to configure.
func (g *ClusterClaimApprovalValidatingAdmissionPolicyGenerator) Validate() error {
	return nil
}

// approvedConditionsOf returns a CEL expression that evaluates to the Approved entries of the
// given object's status conditions, or an empty list when it has none.
func approvedConditionsOf(obj string) string {
	return fmt.Sprintf(`(has(%[1]s.status) && has(%[1]s.status.conditions) ? %[1]s.status.conditions : []).filter(c, c.type == "%[2]s")`, obj, kfplacementv1alpha1.ClusterClaimCondTypeApproved)
}

// PoliciesWithBindings generates a ValidatingAdmissionPolicy and its policy binding that denies a
// ClusterClaim status update changing the Approved condition, unless the requester may approve
// the claim.
func (g *ClusterClaimApprovalValidatingAdmissionPolicyGenerator) PoliciesWithBindings() ([]PolicyWithBindings, error) {
	// Allow the request if it leaves the Approved condition as it found it. The entry is compared
	// whole, byte for byte as serialized: a change to its status, reason, message, observed
	// generation, or transition time counts, and so would a differently spelled timestamp, which
	// no client library produces.
	isApprovedUnchanged := RawCELExpr(fmt.Sprintf("%s == %s", approvedConditionsOf("object"), approvedConditionsOf("oldObject")))
	// Otherwise allow it only from a requester who may approve this claim. The check runs only
	// when the Approved entry did change, since the OR short-circuits.
	mayApprove := RawCELExpr(fmt.Sprintf(`authorizer.group("%s").resource("clusterclaims").name(object.metadata.name).check("%s").allowed()`, kfplacementv1alpha1.GroupVersion.Group, approveVerb))

	celExpr, err := LogicalOr(isApprovedUnchanged, mayApprove).Build()
	if err != nil {
		return nil, errors.Wraps(err, "failed to build CEL expression")
	}

	policy := &admissionregistrationv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterClaimApprovalVAPPolicyName,
		},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicySpec{
			FailurePolicy: ptr.To(admissionregistrationv1.Fail),
			MatchConstraints: &admissionregistrationv1.MatchResources{
				ResourceRules: []admissionregistrationv1.NamedRuleWithOperations{
					{
						RuleWithOperations: admissionregistrationv1.RuleWithOperations{
							Operations: []admissionregistrationv1.OperationType{
								admissionregistrationv1.Update,
							},
							Rule: admissionregistrationv1.Rule{
								APIGroups:   []string{kfplacementv1alpha1.GroupVersion.Group},
								APIVersions: []string{kfplacementv1alpha1.GroupVersion.Version},
								// Conditions live on the status subresource; a write to the main
								// resource cannot change them.
								Resources: []string{"clusterclaims/status"},
							},
						},
					},
				},
			},
			Validations: []admissionregistrationv1.Validation{
				{
					Expression: celExpr,
					Message:    "setting or changing the Approved condition of a ClusterClaim requires the approve verb on it",
					Reason:     ptr.To(metav1.StatusReasonForbidden),
				},
			},
		},
	}

	binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterClaimApprovalVAPPolicyBindingName,
		},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName: clusterClaimApprovalVAPPolicyName,
			ValidationActions: []admissionregistrationv1.ValidationAction{
				admissionregistrationv1.Deny,
			},
		},
	}
	return []PolicyWithBindings{
		{
			Policy:   policy,
			Bindings: []*admissionregistrationv1.ValidatingAdmissionPolicyBinding{binding},
		},
	}, nil
}
