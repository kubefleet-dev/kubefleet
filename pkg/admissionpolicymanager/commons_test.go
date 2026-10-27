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

import "testing"

func TestValidateCELStringLiterals(t *testing.T) {
	testCases := []struct {
		name    string
		strs    []string
		wantErr bool
	}{
		{name: "plain values", strs: []string{"system:serviceaccount:fleet-system:hub-agent-sa", "admins"}},
		{name: "no values"},
		{name: "a double quote", strs: []string{`a"b`}, wantErr: true},
		{name: "a backslash", strs: []string{`a\b`}, wantErr: true},
		{name: "a newline", strs: []string{"ok", "a\nb"}, wantErr: true},
		{name: "a tab", strs: []string{"a\tb"}, wantErr: true},
		{name: "a C1 control character", strs: []string{"a\u0085b"}, wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateCELStringLiterals(tc.strs...); (err != nil) != tc.wantErr {
				t.Errorf("validateCELStringLiterals(%q) = %v, want error: %t", tc.strs, err, tc.wantErr)
			}
		})
	}
}
