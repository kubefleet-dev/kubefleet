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

package azure

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1beta1 "github.com/kubefleet-dev/kubefleet/apis/cluster/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/propertyprovider"
	"github.com/kubefleet-dev/kubefleet/pkg/propertyprovider/azure/trackers"
)

const (
	nodeName1 = "node-1"
	nodeName2 = "node-2"
	nodeName3 = "node-3"
	nodeName4 = "node-4"

	podName1 = "pod-1"
	podName2 = "pod-2"
	podName3 = "pod-3"
	podName4 = "pod-4"

	containerName1 = "container-1"
	containerName2 = "container-2"
	containerName3 = "container-3"
	containerName4 = "container-4"

	namespaceName1 = "work-1"
	namespaceName2 = "work-2"
	namespaceName3 = "work-3"

	nodeSKU1 = "Standard_1"
	nodeSKU2 = "Standard_2"
	nodeSKU3 = "Standard_3"

	nodeKnownMissingSKU = "Standard_Missing"

	imageName = "nginx"
)

var (
	currentTime = time.Now()
)

// dummyPricingProvider is a mock implementation that implements the PricingProvider interface.
type dummyPricingProvider struct{}

var _ trackers.PricingProvider = &dummyPricingProvider{}

func (d *dummyPricingProvider) OnDemandPrice(instanceType string) (float64, bool) {
	switch instanceType {
	case nodeSKU1:
		return 1.0, true
	case nodeSKU2:
		return 1.0, true
	case nodeKnownMissingSKU:
		return pricing.MissingPrice, true
	default:
		return 0.0, false
	}
}

func (d *dummyPricingProvider) LastUpdated() time.Time {
	return currentTime
}

// dummyPricingProviderWithStaleData is a mock implementation that implements the PricingProvider interface.
type dummyPricingProviderWithStaleData struct{}

var _ trackers.PricingProvider = &dummyPricingProviderWithStaleData{}

func (d *dummyPricingProviderWithStaleData) OnDemandPrice(instanceType string) (float64, bool) {
	switch instanceType {
	case nodeSKU1:
		return 1.0, true
	case nodeSKU2:
		return 1.0, true
	case nodeKnownMissingSKU:
		return pricing.MissingPrice, true
	default:
		return 0.0, false
	}
}

func (d *dummyPricingProviderWithStaleData) LastUpdated() time.Time {
	return currentTime.Add(-time.Hour * 48)
}

func TestCollect(t *testing.T) {
	nodes := []corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName1,
				Labels: map[string]string{
					trackers.AKSClusterNodeSKULabelName: nodeSKU1,
				},
			},
			Spec: corev1.NodeSpec{},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("3.2"),
					corev1.ResourceMemory: resource.MustParse("15.2Gi"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName2,
				Labels: map[string]string{
					trackers.AKSClusterNodeSKULabelName: nodeSKU2,
				},
			},
			Spec: corev1.NodeSpec{},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("7"),
					corev1.ResourceMemory: resource.MustParse("30Gi"),
				},
			},
		},
	}
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName1,
				Namespace: namespaceName1,
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName1,
				Containers: []corev1.Container{
					{
						Name: containerName1,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1"),
								corev1.ResourceMemory: resource.MustParse("2Gi"),
							},
						},
					},
					{
						Name: containerName2,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1.5"),
								corev1.ResourceMemory: resource.MustParse("4Gi"),
							},
						},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName2,
				Namespace: namespaceName1,
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName2,
				Containers: []corev1.Container{
					{
						Name: containerName3,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("6"),
								corev1.ResourceMemory: resource.MustParse("16Gi"),
							},
						},
					},
				},
			},
		},
	}

	testCases := []struct {
		name                         string
		nodes                        []corev1.Node
		pods                         []corev1.Pod
		pricingprovider              trackers.PricingProvider
		wantMetricCollectionResponse propertyprovider.PropertyCollectionResponse
	}{
		{
			name:            "can report properties",
			nodes:           nodes,
			pods:            pods,
			pricingprovider: &dummyPricingProvider{},
			wantMetricCollectionResponse: propertyprovider.PropertyCollectionResponse{
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{
					propertyprovider.NodeCountProperty: {
						Value: "2",
					},
					PerCPUCoreCostProperty: {
						Value: "0.167",
					},
					PerGBMemoryCostProperty: {
						Value: "0.042",
					},
				},
				Resources: clusterv1beta1.ResourceUsage{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("12"),
						corev1.ResourceMemory: resource.MustParse("48Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10.2"),
						corev1.ResourceMemory: resource.MustParse("45.2Gi"),
					},
					Available: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1.7"),
						corev1.ResourceMemory: resource.MustParse("23.2Gi"),
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:    CostPropertiesCollectionSucceededCondType,
						Status:  metav1.ConditionTrue,
						Reason:  CostPropertiesCollectionSucceededReason,
						Message: CostPropertiesCollectionSucceededMsg,
					},
				},
			},
		},
		{
			name:  "will report zero values if the requested resources exceed the allocatable resources",
			nodes: nodes,
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName1,
						Namespace: namespaceName1,
					},
					Spec: corev1.PodSpec{
						NodeName: nodeName1,
						Containers: []corev1.Container{
							{
								Name: containerName1,
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("10"),
										corev1.ResourceMemory: resource.MustParse("20Gi"),
									},
								},
							},
							{
								Name: containerName2,
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("20"),
										corev1.ResourceMemory: resource.MustParse("20Gi"),
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName2,
						Namespace: namespaceName1,
					},
					Spec: corev1.PodSpec{
						NodeName: nodeName2,
						Containers: []corev1.Container{
							{
								Name: containerName3,
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("20"),
										corev1.ResourceMemory: resource.MustParse("20Gi"),
									},
								},
							},
						},
					},
				},
			},
			pricingprovider: &dummyPricingProvider{},
			wantMetricCollectionResponse: propertyprovider.PropertyCollectionResponse{
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{
					propertyprovider.NodeCountProperty: {
						Value: "2",
					},
					PerCPUCoreCostProperty: {
						Value: "0.167",
					},
					PerGBMemoryCostProperty: {
						Value: "0.042",
					},
				},
				Resources: clusterv1beta1.ResourceUsage{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("12"),
						corev1.ResourceMemory: resource.MustParse("48Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10.2"),
						corev1.ResourceMemory: resource.MustParse("45.2Gi"),
					},
					Available: corev1.ResourceList{
						corev1.ResourceCPU:    resource.Quantity{},
						corev1.ResourceMemory: resource.Quantity{},
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:    CostPropertiesCollectionSucceededCondType,
						Status:  metav1.ConditionTrue,
						Reason:  CostPropertiesCollectionSucceededReason,
						Message: CostPropertiesCollectionSucceededMsg,
					},
				},
			},
		},
		{
			name: "can report cost properties collection failures",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName1,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeSKU3,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName2,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeSKU3,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
			pods:            []corev1.Pod{},
			pricingprovider: &dummyPricingProvider{},
			wantMetricCollectionResponse: propertyprovider.PropertyCollectionResponse{
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{
					propertyprovider.NodeCountProperty: {
						Value: "2",
					},
				},
				Resources: clusterv1beta1.ResourceUsage{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
					Available: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:   CostPropertiesCollectionSucceededCondType,
						Status: metav1.ConditionFalse,
						Reason: CostPropertiesCollectionFailedReason,
						Message: fmt.Sprintf(CostPropertiesCollectionFailedMsgTemplate,
							fmt.Sprintf("nodes are present, but no pricing data is available for any node SKUs (%v)", []string{nodeSKU3}),
						),
					},
				},
			},
		},
		{
			name: "can report cost properties collection warnings (unknown node SKUs)",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName1,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeSKU1,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("3.2"),
							corev1.ResourceMemory: resource.MustParse("15.2Gi"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName2,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeSKU3,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
			pods:            []corev1.Pod{},
			pricingprovider: &dummyPricingProvider{},
			wantMetricCollectionResponse: propertyprovider.PropertyCollectionResponse{
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{
					propertyprovider.NodeCountProperty: {
						Value: "2",
					},
					PerCPUCoreCostProperty: {
						Value: "0.167",
					},
					PerGBMemoryCostProperty: {
						Value: "0.056",
					},
				},
				Resources: clusterv1beta1.ResourceUsage{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("18Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5.2"),
						corev1.ResourceMemory: resource.MustParse("17.2Gi"),
					},
					Available: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5.2"),
						corev1.ResourceMemory: resource.MustParse("17.2Gi"),
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:   CostPropertiesCollectionSucceededCondType,
						Status: metav1.ConditionTrue,
						Reason: CostPropertiesCollectionDegradedReason,
						Message: fmt.Sprintf(CostPropertiesCollectionDegradedMsgTemplate,
							[]string{
								fmt.Sprintf("failed to find pricing information for one or more of the node SKUs (%v) in the cluster; such SKUs are ignored in cost calculation", []string{nodeSKU3}),
							},
						),
					},
				},
			},
		},
		{
			name: "can report cost properties collection warnings (no SKU labels)",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName1,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeSKU1,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("3.2"),
							corev1.ResourceMemory: resource.MustParse("15.2Gi"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName2,
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
			pods:            []corev1.Pod{},
			pricingprovider: &dummyPricingProvider{},
			wantMetricCollectionResponse: propertyprovider.PropertyCollectionResponse{
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{
					propertyprovider.NodeCountProperty: {
						Value: "2",
					},
					PerCPUCoreCostProperty: {
						Value: "0.167",
					},
					PerGBMemoryCostProperty: {
						Value: "0.056",
					},
				},
				Resources: clusterv1beta1.ResourceUsage{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("18Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5.2"),
						corev1.ResourceMemory: resource.MustParse("17.2Gi"),
					},
					Available: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5.2"),
						corev1.ResourceMemory: resource.MustParse("17.2Gi"),
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:   CostPropertiesCollectionSucceededCondType,
						Status: metav1.ConditionTrue,
						Reason: CostPropertiesCollectionDegradedReason,
						Message: fmt.Sprintf(CostPropertiesCollectionDegradedMsgTemplate,
							[]string{
								fmt.Sprintf("failed to find pricing information for one or more of the node SKUs (%v) in the cluster; such SKUs are ignored in cost calculation", []string{}),
							},
						),
					},
				},
			},
		},
		{
			name: "can report cost properties collection warnings (known missing node SKU)",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName1,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeSKU1,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("3.2"),
							corev1.ResourceMemory: resource.MustParse("15.2Gi"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName2,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeKnownMissingSKU,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
			pods:            []corev1.Pod{},
			pricingprovider: &dummyPricingProvider{},
			wantMetricCollectionResponse: propertyprovider.PropertyCollectionResponse{
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{
					propertyprovider.NodeCountProperty: {
						Value: "2",
					},
					PerCPUCoreCostProperty: {
						Value: "0.167",
					},
					PerGBMemoryCostProperty: {
						Value: "0.056",
					},
				},
				Resources: clusterv1beta1.ResourceUsage{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("18Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5.2"),
						corev1.ResourceMemory: resource.MustParse("17.2Gi"),
					},
					Available: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5.2"),
						corev1.ResourceMemory: resource.MustParse("17.2Gi"),
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:   CostPropertiesCollectionSucceededCondType,
						Status: metav1.ConditionTrue,
						Reason: CostPropertiesCollectionDegradedReason,
						Message: fmt.Sprintf(CostPropertiesCollectionDegradedMsgTemplate,
							[]string{
								fmt.Sprintf("failed to find pricing information for one or more of the node SKUs (%v) in the cluster; such SKUs are ignored in cost calculation", []string{nodeKnownMissingSKU}),
							},
						),
					},
				},
			},
		},
		{
			name:            "can report cost properties collection warnings (stale pricing data)",
			nodes:           nodes,
			pods:            pods,
			pricingprovider: &dummyPricingProviderWithStaleData{},
			wantMetricCollectionResponse: propertyprovider.PropertyCollectionResponse{
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{
					propertyprovider.NodeCountProperty: {
						Value: "2",
					},
					PerCPUCoreCostProperty: {
						Value: "0.167",
					},
					PerGBMemoryCostProperty: {
						Value: "0.042",
					},
				},
				Resources: clusterv1beta1.ResourceUsage{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("12"),
						corev1.ResourceMemory: resource.MustParse("48Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10.2"),
						corev1.ResourceMemory: resource.MustParse("45.2Gi"),
					},
					Available: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1.7"),
						corev1.ResourceMemory: resource.MustParse("23.2Gi"),
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:   CostPropertiesCollectionSucceededCondType,
						Status: metav1.ConditionTrue,
						Reason: CostPropertiesCollectionDegradedReason,
						Message: fmt.Sprintf(CostPropertiesCollectionDegradedMsgTemplate,
							[]string{
								fmt.Sprintf("the pricing data is stale (last updated at %v); the system might have issues connecting to the Azure Retail Prices API, or the current region is unsupported", currentTime.Add(-time.Hour*48)),
							},
						),
					},
				},
			},
		},
		{
			name: "can report cost properties collection warnings (stale data and missing SKUs)",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName1,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeSKU1,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("3.2"),
							corev1.ResourceMemory: resource.MustParse("15.2Gi"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName2,
						Labels: map[string]string{
							trackers.AKSClusterNodeSKULabelName: nodeSKU3,
						},
					},
					Spec: corev1.NodeSpec{},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
			pods:            []corev1.Pod{},
			pricingprovider: &dummyPricingProviderWithStaleData{},
			wantMetricCollectionResponse: propertyprovider.PropertyCollectionResponse{
				Properties: map[clusterv1beta1.PropertyName]clusterv1beta1.PropertyValue{
					propertyprovider.NodeCountProperty: {
						Value: "2",
					},
					PerCPUCoreCostProperty: {
						Value: "0.167",
					},
					PerGBMemoryCostProperty: {
						Value: "0.056",
					},
				},
				Resources: clusterv1beta1.ResourceUsage{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("18Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5.2"),
						corev1.ResourceMemory: resource.MustParse("17.2Gi"),
					},
					Available: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5.2"),
						corev1.ResourceMemory: resource.MustParse("17.2Gi"),
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:   CostPropertiesCollectionSucceededCondType,
						Status: metav1.ConditionTrue,
						Reason: CostPropertiesCollectionDegradedReason,
						Message: fmt.Sprintf(CostPropertiesCollectionDegradedMsgTemplate,
							[]string{
								fmt.Sprintf("failed to find pricing information for one or more of the node SKUs (%v) in the cluster; such SKUs are ignored in cost calculation", []string{nodeSKU3}),
								fmt.Sprintf("the pricing data is stale (last updated at %v); the system might have issues connecting to the Azure Retail Prices API, or the current region is unsupported", currentTime.Add(-time.Hour*48)),
							},
						),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Build the trackers manually for testing purposes.
			nodeTracker := trackers.NewNodeTracker(tc.pricingprovider)
			podTracker := trackers.NewPodTracker()
			for idx := range tc.nodes {
				nodeTracker.AddOrUpdate(&tc.nodes[idx])
			}
			for idx := range tc.pods {
				podTracker.AddOrUpdate(&tc.pods[idx])
			}

			p := &PropertyProvider{
				nodeTracker: nodeTracker,
				podTracker:  podTracker,
			}
			res := p.Collect(ctx)
			if diff := cmp.Diff(res, tc.wantMetricCollectionResponse, ignoreObservationTimeFieldInPropertyValue); diff != "" {
				t.Fatalf("Collect() property collection response diff (-got, +want):\n%s", diff)
			}
		})
	}
}
