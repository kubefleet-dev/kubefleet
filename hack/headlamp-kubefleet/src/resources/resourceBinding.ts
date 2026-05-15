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

export interface ResourceBindingInterface extends KubeObjectInterface {
  spec: {
    [key: string]: unknown;
  };
  status: {
    conditions?: StatusCondition[];
    [key: string]: unknown;
  };
}

export class ResourceBinding extends KubeObject<ResourceBindingInterface> {
  static kind = 'ResourceBinding';
  static apiName = 'resourcebindings';
  static apiVersion = 'placement.kubernetes-fleet.io/v1beta1';
  static isNamespaced = true;

  get spec() {
    return this.jsonData.spec;
  }

  get conditions(): StatusCondition[] {
    return this.jsonData.status?.conditions ?? [];
  }
}
