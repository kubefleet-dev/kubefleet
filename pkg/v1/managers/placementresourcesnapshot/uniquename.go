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

package placementresourcesnapshot

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
)

const (
	nameLenLimit = 251
	hashSegLen   = 12
)

const (
	// The name format for primary placement resource snapshots. Typically, these snapshots are named using the
	// format:
	//
	// `[PLACEMENT-POLICY-NAME]-resource-snapshot-[SNAPSHOT-INDEX]`,
	//
	// where `[PLACEMENT-POLICY-NAME]` is the name of the owner placement policy, and
	// `[SNAPSHOT-INDEX]` is the monotonically increasing index of the snapshot.
	//
	// If the name becomes too long (> 251 characters), KubeFleet will truncate the placement policy name segment
	// and the snapshot index segment as appropriate and add a hash suffix to the name, i.e.,
	//
	// `[PLACEMENT-POLICY-NAME-TRUNCATED]-resource-snapshot-[SNAPSHOT-INDEX-TRUNCATED]-[HASH]`,
	//
	// where `[HASH]` is the first few characters of the hash of the value
	// `[PLACEMENT-POLICY-NAME]-resource-snapshot-[SNAPSHOT-INDEX]`.
	PrimaryPlacementResourceSnapshotNameFmt         = "%s-resource-snapshot-%d"
	PrimaryPlacementResourceSnapshotNameWithHashFmt = "%s-resource-snapshot-%s-%s"

	// The name format for secondary placement resource snapshots. Typically, these snapshots are named using the
	// format:
	//
	// `[PLACEMENT-POLICY-NAME]-resource-snapshot-[SNAPSHOT-INDEX]-[SNAPSHOT-SUB-INDEX]`,
	//
	// where `[PLACEMENT-POLICY-NAME]` is the name of the owner placement policy,
	// `[SNAPSHOT-INDEX]` is the monotonically increasing index of the snapshot, and
	// `[SNAPSHOT-SUB-INDEX]` is the monotonically increasing sub-index of the snapshot.
	//
	// If the name becomes too long (> 251 characters), KubeFleet will truncate the placement policy name segment
	// and the `[SNAPSHOT-INDEX]-[SNAPSHOT-SUB-INDEX]` segment as appropriate and add a hash suffix to the name, i.e.,
	//
	// `[PLACEMENT-POLICY-NAME-TRUNCATED]-resource-snapshot-[INDEX-TRUNCATED]-[HASH]`,
	//
	// where `[HASH]` is the first few characters of the hash of the value
	// `[PLACEMENT-POLICY-NAME-TRUNCATED]-resource-snapshot-[SNAPSHOT-INDEX]-[SNAPSHOT-SUB-INDEX]`.
	SecondaryPlacementResourceSnapshotNameFmt         = "%s-resource-snapshot-%d-%d"
	SecondaryPlacementResourceSnapshotNameWithHashFmt = "%s-resource-snapshot-%s-%s"
)

// uniqueNameForPrimaryPlacementResourceSnapshot generates a unique name for a primary placement resource snapshot.
func uniqueNameForPrimaryPlacementResourceSnapshot(placementPolicyName string, idx int) (string, error) {
	name := fmt.Sprintf(PrimaryPlacementResourceSnapshotNameFmt, placementPolicyName, idx)
	if len(name) <= nameLenLimit {
		return name, nil
	}

	// The name is too long; truncate the placement policy name segment and append a hash suffix.
	// The hash is computed over the full (untruncated) name.
	//
	// Note that here only the first few (12) characters are kept. This does lead to increased risk of name
	// collisions, but the chances still remain extremely low. If such a collision does occur, manual intervention
	// is needed for resolution.
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(name)))[:hashSegLen]

	// Compute how many characters are left for the two variable segments (the placement policy name and the
	// snapshot index), then split the available space evenly between them.
	//
	// reservedLen accounts for the static decoration and the hash suffix only.
	//
	// The offset 1 is the length of placeholder index (0).
	reservedLen := len(fmt.Sprintf(PrimaryPlacementResourceSnapshotNameWithHashFmt, "", "0", hash)) - 1
	availableLen := nameLenLimit - reservedLen
	availablePerSeg := availableLen / 2

	// Remove all dots from the placement policy name segment so that truncation cannot leave a trailing dot,
	// which would produce an invalid DNS subdomain label.
	truncatedPlacementPolicyName := strings.ReplaceAll(placementPolicyName, ".", "")
	if len(truncatedPlacementPolicyName) > availablePerSeg {
		truncatedPlacementPolicyName = truncatedPlacementPolicyName[:availablePerSeg]
	}

	truncatedIdxStr := strconv.Itoa(idx)
	if len(truncatedIdxStr) > availablePerSeg {
		truncatedIdxStr = truncatedIdxStr[:availablePerSeg]
	}

	return fmt.Sprintf(PrimaryPlacementResourceSnapshotNameWithHashFmt, truncatedPlacementPolicyName, truncatedIdxStr, hash), nil
}

func uniqueNameForSecondaryPlacementResourceSnapshot(placementPolicyName string, idx int, subIdx int) (string, error) {
	name := fmt.Sprintf(SecondaryPlacementResourceSnapshotNameFmt, placementPolicyName, idx, subIdx)
	if len(name) <= nameLenLimit {
		return name, nil
	}

	// The name is too long; truncate the placement policy name segment and append a hash suffix.
	// The hash is computed over the full (untruncated) name.
	//
	// Note that here only the first few (12) characters are kept. This does lead to increased risk of name
	// collisions, but the chances still remain extremely low. If such a collision does occur, manual intervention
	// is needed for resolution.
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(name)))[:hashSegLen]

	// Compute how many characters are left for the two variable segments (the placement policy name and the
	// combined snapshot index/sub-index segment), then split the available space evenly between them.
	//
	// reservedLen accounts for the static decoration and the hash suffix only.
	reservedLen := len(fmt.Sprintf(SecondaryPlacementResourceSnapshotNameWithHashFmt, "", "", hash))
	availableLen := nameLenLimit - reservedLen
	availablePerSeg := availableLen / 2

	// Remove all dots from the placement policy name segment so that truncation cannot leave a trailing dot,
	// which would produce an invalid DNS subdomain label.
	truncatedPlacementPolicyName := strings.ReplaceAll(placementPolicyName, ".", "")
	if len(truncatedPlacementPolicyName) > availablePerSeg {
		truncatedPlacementPolicyName = truncatedPlacementPolicyName[:availablePerSeg]
	}

	truncatedIdxStr := fmt.Sprintf("%d-%d", idx, subIdx)
	if len(truncatedIdxStr) > availablePerSeg {
		truncatedIdxStr = truncatedIdxStr[:availablePerSeg]
	}

	return fmt.Sprintf(SecondaryPlacementResourceSnapshotNameWithHashFmt, truncatedPlacementPolicyName, truncatedIdxStr, hash), nil
}
