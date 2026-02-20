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

package gvkwhitelist

import (
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	wildCardCharacter = "*"
)

// Use a trie-like structure so that we can easily support GVK matching with wildcards.
type tNode struct {
	children map[string]*tNode
}

// WhitelistedGVKs represents a collection of GVKs that are whitelisted for additional processing,
// e.g., status back-reporting.
type WhitelistedGVKs struct {
	root *tNode

	// TO-DO (chenyu1): evaluate if the shortcuts can really help improve performance, as there
	// might be additional overhead associated with the guarding mutex.
	shortcuts map[schema.GroupVersionKind]bool

	mu sync.Mutex
}

// NewWhitelistedGVKs returns a new WhitelistedGVKs instance.
func NewWhitelistedGVKs() *WhitelistedGVKs {
	return &WhitelistedGVKs{
		root: &tNode{
			children: map[string]*tNode{},
		},
		shortcuts: map[schema.GroupVersionKind]bool{},
	}
}

// Register adds a new GVK into the whitelist.
func (w *WhitelistedGVKs) Register(apiGroup, apiVersion, kind string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	newValH := []string{apiGroup, apiVersion, kind}

	if w.root == nil {
		w.root = &tNode{
			children: map[string]*tNode{},
		}
	}

	curN := w.root
	for _, val := range newValH {
		childN, exists := curN.children[val]
		if !exists {
			childN = &tNode{
				children: map[string]*tNode{},
			}
			curN.children[val] = childN
		}
		curN = childN
	}
}

// IsWhitelisted checks if a given GVK is whitelisted.
func (w *WhitelistedGVKs) IsWhitelisted(gvk schema.GroupVersionKind) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if matched, ok := w.shortcuts[gvk]; ok {
		return matched
	}

	valToCheckH := []string{gvk.Group, gvk.Version, gvk.Kind}

	var match func(node *tNode, depthLeft int) bool
	match = func(node *tNode, depthLeft int) bool {
		if depthLeft == 0 {
			return true
		}

		valToCheck := valToCheckH[len(valToCheckH)-depthLeft]

		// Check for exact match first.
		if childN, exists := node.children[valToCheck]; exists {
			if match(childN, depthLeft-1) {
				return true
			}
		}
		// Check for wildcard match.
		if childN, exists := node.children[wildCardCharacter]; exists {
			if match(childN, depthLeft-1) {
				return true
			}
		}
		// No match found.
		return false
	}

	matched := match(w.root, len(valToCheckH))
	w.shortcuts[gvk] = matched
	return matched
}

// Parse parses a raw string representation of a GVK whitelist into a WhitelistedGVKs instance.
func Parse(raw string) (*WhitelistedGVKs, error) {
	if len(raw) == 0 {
		// Empty input; return a nil pointer. This effectively means that status back-reporting to original
		// resource is disabled for all GVKs.
		return nil, nil
	}
	whitelist := NewWhitelistedGVKs()

	// Break the raw string into segments, separated by commas (,).
	gvkSegs := strings.Split(raw, ",")

	for idx := range gvkSegs {
		seg := gvkSegs[idx]

		// Break each segment into separate parts, separated by slashes (/).
		parts := strings.Split(seg, "/")
		var apiGroup, apiVersion, kind string
		switch {
		case len(parts) < 2 || len(parts) > 3:
			return nil, fmt.Errorf("failed to parse GVK whitelist raw string: segment %s is not valid (too few or too many parts)", seg)
		case len(parts) == 2:
			// If there are only two parts, the API resource is assumed to be of the core API group.
			apiGroup = ""
			apiVersion = parts[0]
			kind = parts[1]
		case len(parts) == 3:
			apiGroup = parts[0]
			apiVersion = parts[1]
			kind = parts[2]
		}

		// Validate the input parts.
		//
		// Note that we do not verify if the inputs are consistent with the Kubernetes API conventions (e.g., versions should start with "v");
		// only the hard requirements are checked.
		if len(apiGroup) > 0 && apiGroup != "*" {
			if errs := validation.IsDNS1123Subdomain(apiGroup); len(errs) > 0 {
				return nil, fmt.Errorf("failed to parse GVK whitelist raw string: segment %s has an invalid API group part: %v", seg, errs)
			}
		}
		if apiVersion != "*" {
			if errs := validation.IsDNS1123Label(apiVersion); len(errs) > 0 {
				return nil, fmt.Errorf("failed to parse GVK whitelist raw string: segment %s has an invalid API version part: %v", seg, errs)
			}
		}
		if kind != "*" {
			if errs := validation.IsDNS1035Label(strings.ToLower(kind)); len(errs) > 0 {
				return nil, fmt.Errorf("failed to parse GVK whitelist raw string: segment %s has an invalid Kind part: %v", seg, errs)
			}
		}

		// Register the GVK into the whitelist.
		whitelist.Register(apiGroup, apiVersion, kind)
	}
	return whitelist, nil
}
