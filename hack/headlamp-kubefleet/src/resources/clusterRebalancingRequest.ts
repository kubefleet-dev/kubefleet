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

export interface ObjectReference {
  kind?: string;
  namespace?: string;
  name?: string;
  uid?: string;
}

export interface Migration {
  placementName: ObjectReference;
  fromClusterBinding: ObjectReference;
  toClusterBinding: ObjectReference;
  conditions?: StatusCondition[];
}

export interface ClusterRebalancingRequestInterface extends KubeObjectInterface {
  spec: {
    rollback?: boolean;
    from?: string;
    to?: string;
    mode?: 'SurgeFirst' | 'DrainFirst';
    maxConcurrency?: number;
    failurePolicy?: {
      maxFailureCount?: number;
      maximumWaitDurationPerMigrationAttemptSeconds?: number;
    };
    [key: string]: unknown;
  };
  status: {
    conditions?: StatusCondition[];
    migrations?: Migration[];
    [key: string]: unknown;
  };
}

export class ClusterRebalancingRequest extends KubeObject<ClusterRebalancingRequestInterface> {
  static kind = 'ClusterRebalancingRequest';
  static apiName = 'clusterrebalancingrequests'; // plural name
  static apiVersion = 'placement.kubernetes-fleet.io/v1beta1'; // group/version
  static isNamespaced = false; // cluster-scoped resource

  get spec() {
    return this.jsonData.spec;
  }

  get rollback(): boolean {
    return this.jsonData.spec?.rollback ?? false;
  }

  get from(): string | undefined {
    return this.jsonData.spec?.from;
  }

  get to(): string | undefined {
    return this.jsonData.spec?.to;
  }

  get mode(): 'SurgeFirst' | 'DrainFirst' | undefined {
    return this.jsonData.spec?.mode;
  }

  get maxConcurrency(): number | undefined {
    return this.jsonData.spec?.maxConcurrency;
  }

  get maxFailureCount(): number | undefined {
    return this.jsonData.spec?.failurePolicy?.maxFailureCount;
  }

  get maximumWaitDurationPerMigrationAttemptSeconds(): number | undefined {
    return this.jsonData.spec?.failurePolicy?.maximumWaitDurationPerMigrationAttemptSeconds;
  }

  get status() {
    return this.jsonData.status;
  }

  get conditions(): StatusCondition[] {
    return this.jsonData.status?.conditions ?? [];
  }

  get migrations(): Migration[] {
    return this.jsonData.status?.migrations ?? [];
  }
}
