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

package placementbinding

import (
	"context"
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	experimentalv1beta1 "github.com/kubefleet-dev/kubefleet/apis/experimental/v1beta1"
	placementv1beta1 "github.com/kubefleet-dev/kubefleet/apis/placement/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/reimagined/ociartifactconnector"
	"github.com/kubefleet-dev/kubefleet/pkg/utils/errors"
)

func (r *Reconciler) extractManifestsFrom(ctx context.Context, snapshot *experimentalv1beta1.PlacementResourceSnapshot) ([]placementv1beta1.Manifest, error) {
	snapshotRevision, found := snapshot.Labels[experimentalv1beta1.ResourceSnapshotRevisionLabelKey]
	if !found {
		// Do a sanity check.
		return nil, errors.NewUnexpectedError(nil, "the placement resource snapshot does not have an annotated revision")
	}
	resources := snapshot.Spec.Resources
	if len(resources) == 0 {
		klog.V(2).InfoS("No resources found in the snapshot")
		return nil, nil
	}

	manifests := []placementv1beta1.Manifest{}

	if len(resources) > 1 {
		for idx := range resources {
			resource := &resources[idx]
			if isSnapshottedResourceORASManifests(resource) || isSnapshottedResourceORASHelmChart(resource) {
				return nil, errors.NewUserError(nil,
					"multiple ORAS resources are grouped in a placement, or an ORAS resource is grouped with raw manifests",
					"ORASResourceIdentifier", resource.Identifier, "resourceCount", len(resources))
			}

			manifests = append(manifests, placementv1beta1.Manifest{
				RawExtension: resource.Manifest,
			})
		}
		klog.V(2).InfoS("Found raw manifests in the snapshot; extracted them as they are", "resourceCount", len(resources))
		return manifests, nil
	}

	singleResource := &resources[0]
	switch {
	case isSnapshottedResourceORASManifests(singleResource):
		rawExts, err := r.extractORASManifestsFrom(ctx, snapshotRevision, *singleResource)
		if err != nil {
			return nil, errors.Wraps(err, "failed to extract manifests from the ORAS resource in the snapshot")
		}
		for idx := range rawExts {
			manifests = append(manifests, placementv1beta1.Manifest{
				RawExtension: rawExts[idx],
			})
		}
		klog.V(2).InfoS("Found an ORAS resource in the snapshot; extracted its manifests",
			"resourceIdentifier", singleResource.Identifier, "manifestCount", len(rawExts))
	case isSnapshottedResourceORASHelmChart(singleResource):
		panic("not yet implemented")
	default:
		manifests = append(manifests, placementv1beta1.Manifest{
			RawExtension: singleResource.Manifest,
		})
		klog.V(2).InfoS("Found a single raw manifest in the snapshot; extracted it as it is", "resourceIdentifier", singleResource.Identifier)
	}

	return manifests, nil
}

func isSnapshottedResourceORASManifests(resource *experimentalv1beta1.ResourceContent) bool {
	return resource != nil &&
		resource.Identifier.APIGroup == experimentalv1beta1.GroupVersion.Group &&
		resource.Identifier.Kind == "ORASManifests"
}

func isSnapshottedResourceORASHelmChart(resource *experimentalv1beta1.ResourceContent) bool {
	return resource != nil &&
		resource.Identifier.APIGroup == experimentalv1beta1.GroupVersion.Group &&
		resource.Identifier.Kind == "ORASHelmChart"
}

func (r *Reconciler) extractORASManifestsFrom(
	ctx context.Context,
	key string,
	wrappedOrasManifest experimentalv1beta1.ResourceContent,
) ([]runtime.RawExtension, error) {
	// Load the ORASManifests API object.
	unstructuredObj := &unstructured.Unstructured{}
	if err := unstructuredObj.UnmarshalJSON(wrappedOrasManifest.Manifest.Raw); err != nil {
		return nil, errors.NewUnexpectedError(err, "failed to unmarshal raw data into a unstructured object")
	}

	var orasManifests experimentalv1beta1.ORASManifests
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &orasManifests); err != nil {
		return nil, errors.NewUnexpectedError(err, "failed to convert unstructured object into an ORASManifests API object")
	}

	// Build the OCI artifact connector.
	url := orasManifests.Spec.OCIArtifact.URL
	if len(url) == 0 {
		return nil, errors.NewUserError(nil, "invalid OCI artifact URL: empty string")
	}
	connector, err := ociartifactconnector.NewRemoteConnectorFromAuthProvider(ctx, url, orasManifests.Spec.OCIArtifact.AuthProvider, r.HubClient, r.UseHTTPToConnectToOCIRegistry)
	if err != nil {
		return nil, errors.Wraps(err, "failed to connect to the remote OCI artifact", "url", url)
	}

	// Read the digest of the OCI artifact to retrieve from the sanpshot.
	orasManifestsAdditionalInfoBytes, found := wrappedOrasManifest.AdditionalInfo[experimentalv1beta1.ResourceSnapshotAdditionalInfoKeyORASManifests]
	if !found {
		return nil, errors.NewUserError(nil, "no layer information is found")
	}
	var orasManifestsAdditionalInfo experimentalv1beta1.ORASManifestsAdditionalInfoForSnapshots
	if err := json.Unmarshal(orasManifestsAdditionalInfoBytes, &orasManifestsAdditionalInfo); err != nil {
		return nil, errors.NewUnexpectedError(err, "failed to unmarshal ORAS manifests additional info from JSON data")
	}

	// Retrieve all the manifests from the OCI artifact.
	artifactDigest := orasManifestsAdditionalInfo.OCIArtifactDigest
	path := orasManifests.Spec.Path
	return r.OCIArtifactCachedStoreManager.GetManifests(ctx, artifactDigest, &key, connector, path)
}
