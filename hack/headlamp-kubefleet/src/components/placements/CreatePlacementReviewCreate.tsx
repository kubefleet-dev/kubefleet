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

import { EditorDialog, SectionBox } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { Typography } from '@mui/material';
import React from 'react';
export const TEMPLATE = `\
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: CRP_NAME
spec:
  policy:
    placementType: PickAll
  resourceSelectors:
    - group: ''
      kind: Namespace
      version: v1
      name: NS_NAME
  strategy:
    type: ROLLOUT_STRATEGY
    applyStrategy:
      type: APPLY_STRATEGY
---
apiVersion: v1
kind: Namespace
metadata:
  name: NS_NAME
`;

export interface CreatePlacementReviewCreateProps {
  yaml: string;
}

export function CreatePlacementReviewCreate({ yaml }: CreatePlacementReviewCreateProps) {

  return (
    <>
      <SectionBox>
        <Typography
          variant="h4"
          align="center"
          sx={{ fontFamily: 'monospace', fontWeight: 400, mt: 6 }}
        >
          Review and Create
        </Typography>
      </SectionBox>
      <SectionBox sx={{ mt: 4 }}>
        <EditorDialog
          item={yaml}
          open
          onClose={() => {}}
          onSave={null}
          noDialog
          allowToHideManagedFields
        />
      </SectionBox>
    </>
  );
}
