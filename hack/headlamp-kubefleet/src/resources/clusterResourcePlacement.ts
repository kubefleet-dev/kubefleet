/*
 * Copyright 2025 The Kubernetes Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { KubeObject, KubeObjectInterface } from '@kinvolk/headlamp-plugin/lib/k8s/cluster';
import { StatusCondition } from './common';

export interface SelectedResource {
  group: string;
  version: string;
  kind: string;
  name: string;
  namespace?: string;
}

export interface FailedResourcePlacement {
  condition: StatusCondition;
}

export interface PerClusterPlacementStatus {
  clusterName: string;
  conditions?: StatusCondition[];
  observedResourceIndex?: number;
  rolloutState?: Record<string, string>;
  failedPlacements?: FailedResourcePlacement[];
}


export interface ClusterResourcePlacementInterface extends KubeObjectInterface {
  spec: {
    [key: string]: unknown;
  };
  status: {
    conditions?: StatusCondition[];
    selectedResources?: SelectedResource[];
    placementStatuses?: unknown[];
    rolloutManagedBy?: Record<string, string>;
    latestResourceSnapshotIndex?: number;
    resourceSnapshotSynchronized?: boolean;
    [key: string]: unknown;
  };
}

export class ClusterResourcePlacement extends KubeObject<ClusterResourcePlacementInterface> {
  static kind = 'ClusterResourcePlacement';
  static apiName = 'clusterresourceplacements'; // plural name
  static apiVersion = 'placement.kubernetes-fleet.io/v1beta1'; // group/version
  static isNamespaced = false; // cluster-scoped resource

  get spec() {
    return this.jsonData.spec;
  }

  get status() {
    return this.jsonData.status;
  }

  get selectedResourcesCount(): number {
    return this.jsonData.status?.selectedResources?.length ?? 0;
  }

  get selectedResources(): SelectedResource[] {
    return this.jsonData.status?.selectedResources ?? [];
  }

  get selectedClusterStatuses(): PerClusterPlacementStatus[] {
    return (
      (this.jsonData.status?.placementStatuses as PerClusterPlacementStatus[] | undefined) ?? []
    );
  }

  get selectedClustersCount(): number {
    return this.jsonData.status?.placementStatuses?.length ?? 0;
  }

  get notAppliedClusters(): number {
    const statuses = this.jsonData.status?.placementStatuses as
      | Array<{ conditions?: Array<{ type: string }> }>
      | undefined;
    if (!statuses) return 0;
    return statuses.filter(s => !s.conditions?.some(c => c.type === 'Applied')).length;
  }

  get applyErredClusters(): number {
    const statuses = this.jsonData.status?.placementStatuses as
      | Array<{ conditions?: Array<{ type: string; status: string }> }>
      | undefined;
    if (!statuses) return 0;
    return statuses.filter(s =>
      s.conditions?.some(c => c.type === 'Applied' && c.status === 'False')
    ).length;
  }

  get notAvailableCheckedClusters(): number {
    const statuses = this.jsonData.status?.placementStatuses as
      | Array<{ conditions?: Array<{ type: string; status: string }> }>
      | undefined;
    if (!statuses) return 0;
    return statuses.filter(
      s =>
        s.conditions?.some(c => c.type === 'Applied' && c.status === 'True') &&
        !s.conditions?.some(c => c.type === 'Available')
    ).length;
  }

  get unavailableClusters(): number {
    const statuses = this.jsonData.status?.placementStatuses as
      | Array<{ conditions?: Array<{ type: string; status: string }> }>
      | undefined;
    if (!statuses) return 0;
    return statuses.filter(s =>
      s.conditions?.some(c => c.type === 'Available' && c.status === 'False')
    ).length;
  }

  get availableClusters(): number {
    const statuses = this.jsonData.status?.placementStatuses as
      | Array<{ conditions?: Array<{ type: string; status: string }> }>
      | undefined;
    if (!statuses) return 0;
    return statuses.filter(s =>
      s.conditions?.some(c => c.type === 'Available' && c.status === 'True')
    ).length;
  }

  get upToDateClusters(): number {
    const x = this.latestResourceSnapshotIndex;
    if (x === null) return 0;
    return this.selectedClusterStatuses.filter(s => s.observedResourceIndex === x).length;
  }

  get isSyncd(): 'syncd' | 'out-of-sync' | 'na' {
    if (this.selectedResourcesCount === 0 || this.selectedClustersCount === 0) return 'na';
    if (this.resourceSnapshotSynchronized === false || this.resourceSnapshotSynchronized === null)
      return 'out-of-sync';
    return this.upToDateClusters === this.selectedClustersCount ? 'syncd' : 'out-of-sync';
  }

  get healthStatus(): 'failed' | 'available' | 'partially-available' | 'na' | null {
    if (this.selectedResourcesCount === 0 || this.selectedClustersCount === 0) return 'na';
    if (this.selectedClustersCount === this.availableClusters) return 'available';
    if (this.applyErredClusters === 0) return 'partially-available';
    return 'failed';
  }

  get rolloutStatus():
    | 'in-progress'
    | 'stuck'
    | 'stopping-stopped'
    | 'failed'
    | 'completed'
    | 'not-started'
    | 'na' {
    if (this.selectedResourcesCount === 0 || this.selectedClustersCount === 0) return 'na';
    if (this.rolloutStrategyType === 'RollingUpdate') {
      return this.upToDateClusters === this.selectedClustersCount ? 'completed' : 'in-progress';
    }
    if (this.inProgressRollouts > 0) return 'in-progress';
    if (this.stuckRollouts > 0) return 'stuck';
    if (this.stoppingRollouts > 0 || this.stoppedRollouts > 0) return 'stopping-stopped';
    if (this.failedRollouts > 0) return 'failed';
    if (this.completedRollouts > 0) return 'completed';
    return 'not-started';
  }

  get currentStage(): 'scheduled' | 'rolled-out' | 'available' | null {
    const conditions = this.jsonData.status?.conditions as
      | StatusCondition[]
      | undefined;
    const isScheduled = conditions?.some(
      c => c.type === 'ClusterResourcePlacementScheduled' && c.status === 'True'
    );
    if (!isScheduled) return null;
    if (this.healthStatus === 'available') return 'available';
    if (this.rolloutStatus === 'completed') return 'rolled-out';
    return 'scheduled';
  }

  private get rolloutCounts(): Record<string, number> {
    if (this.rolloutStrategyType === 'RollingUpdate') return {};
    const rolloutManagedBy = this.jsonData.status?.rolloutManagedBy as
      | Record<string, string>
      | undefined;
    if (!rolloutManagedBy) return {};
    const counts: Record<string, number> = {};
    for (const state of Object.values(rolloutManagedBy)) {
      counts[state] = (counts[state] ?? 0) + 1;
    }
    return counts;
  }

  get inProgressRollouts(): number {
    return this.rolloutCounts['InProgress'] ?? 0;
  }

  get completedRollouts(): number {
    return this.rolloutCounts['Completed'] ?? 0;
  }

  get stoppingRollouts(): number {
    return this.rolloutCounts['Stopping'] ?? 0;
  }

  get stoppedRollouts(): number {
    return this.rolloutCounts['Stopped'] ?? 0;
  }

  get stuckRollouts(): number {
    return this.rolloutCounts['Stuck'] ?? 0;
  }

  get failedRollouts(): number {
    return this.rolloutCounts['Failed'] ?? 0;
  }

  get latestResourceSnapshotIndex(): number | null {
    return this.jsonData.status?.latestResourceSnapshotIndex ?? null;
  }

  get resourceSnapshotSynchronized(): boolean | null {
    return this.jsonData.status?.resourceSnapshotSynchronized ?? null;
  }

  get applyStrategyType(): 'ClientSideApply' | 'ServerSideApply' | null {
    const val = (this.jsonData.spec as { strategy?: { applyStrategy?: string } })?.strategy
      ?.applyStrategy;
    if (val === 'ClientSideApply' || val === 'ServerSideApply') return val;
    return null;
  }

  get rolloutStrategyType(): 'External' | 'RollingUpdate' | null {
    const val = (this.jsonData.spec as { strategy?: { type?: string } })?.strategy?.type;
    if (val === 'External' || val === 'RollingUpdate') return val;
    return null;
  }
}
