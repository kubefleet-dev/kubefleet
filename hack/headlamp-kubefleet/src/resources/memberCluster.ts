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

export interface AgentStatus {
  type: string;
  lastReceivedHeartbeat?: string;
  conditions?: StatusCondition[];
}

export interface MemberClusterTaint {
  key: string;
  value?: string;
  effect: string;
}

export interface MemberClusterInterface extends KubeObjectInterface {
  spec: {
    taints?: MemberClusterTaint[];
    [key: string]: unknown;
  };
  status: {
    conditions?: StatusCondition[];
    agentStatus?: AgentStatus[];
    [key: string]: unknown;
  };
}

export class MemberCluster extends KubeObject<MemberClusterInterface> {
  static kind = 'MemberCluster';
  static apiName = 'memberclusters'; // plural name
  static apiVersion = 'cluster.kubernetes-fleet.io/v1beta1'; // group/version
  static isNamespaced = false; // cluster-scoped resource

  get spec() {
    return this.jsonData.spec;
  }

  get status() {
    return this.jsonData.status;
  }

  get conditions(): StatusCondition[] {
    return this.jsonData.status?.conditions ?? [];
  }

  get agentStatus(): AgentStatus[] {
    return this.jsonData.status?.agentStatus ?? [];
  }

  get taints(): MemberClusterTaint[] {
    return this.jsonData.spec?.taints ?? [];
  }
}
