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

package writefile

import (
	"os"
	"path/filepath"
)

// CreateSecureFile creates or truncates a file with owner-only permissions (0600).
// The file is opened write-only since callers only need to write sensitive data
// (e.g., tokens, certificates, private keys). The 0600 mode ensures that only the
// file owner can read or write the file, preventing other users from accessing it.
// The path is sanitized with filepath.Clean to resolve any relative or redundant elements.
func CreateSecureFile(path string) (*os.File, error) {
	return os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
}
