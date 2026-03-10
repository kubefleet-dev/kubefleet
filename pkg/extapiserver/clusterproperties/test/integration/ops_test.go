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

package integration

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clusterpropertiesv1beta1 "github.com/kubefleet-dev/kubefleet/apis/clusterproperties/v1beta1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("GET ops", func() {
	Context("basic GET op", Ordered, func() {
		memberClusterName := "bravelion"

		It("can get a cluster properties object by name", func() {
			clusterProperties := &clusterpropertiesv1beta1.MemberClusterProperties{}
			Expect(hubClient.Get(ctx, types.NamespacedName{Name: memberClusterName}, clusterProperties)).To(Succeed())

			GinkgoLogr.Info(fmt.Sprintf("%+v", clusterProperties))
		})
	})
})
