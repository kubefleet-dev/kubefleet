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

package testutils

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/onsi/gomega"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
)

// ExpectValidationError asserts that err is a StatusError whose message contains substring.
func ExpectValidationError(err error, substring string) {
	var statusErr *k8sErrors.StatusError
	gomega.Expect(errors.As(err, &statusErr)).To(gomega.BeTrue(), fmt.Sprintf("API call produced error %s. Error type wanted is %s.", reflect.TypeOf(err), reflect.TypeOf(&k8sErrors.StatusError{})))
	gomega.Expect(statusErr.ErrStatus.Message).Should(gomega.ContainSubstring(substring))
}
