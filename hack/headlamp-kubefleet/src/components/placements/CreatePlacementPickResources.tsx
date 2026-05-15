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
import { Box, FormControl, InputLabel, MenuItem, Select, Typography } from '@mui/material';
import React from 'react';

export interface CreatePlacementPickResourcesProps {
  clusterNames: string[];
  selectedCluster: string;
  onClusterChange: (value: string) => void;
  selectedResourceName: string;
  onResourceNameChange: (value: string) => void;
  namespaceNames: string[];
  namespaceData: object | null;
  namespaceLoading: boolean;
}

export function CreatePlacementPickResources({
  clusterNames,
  selectedCluster,
  onClusterChange,
  selectedResourceName,
  onResourceNameChange,
  namespaceNames,
  namespaceData,
  namespaceLoading,
}: CreatePlacementPickResourcesProps) {
  return (
    <>
      <SectionBox>
        <Typography
          variant="h4"
          align="center"
          sx={{ fontFamily: 'monospace', fontWeight: 400, mt: 6 }}
        >
          What resources would you like to include?
        </Typography>
      </SectionBox>
      <SectionBox>
        <Box display="flex" flexDirection="row" gap={2} mt={6} justifyContent="center">
          <FormControl size="small" sx={{ minWidth: 200 }}>
            <InputLabel>Cluster</InputLabel>
            <Select
              label="Cluster"
              value={selectedCluster}
              onChange={e => onClusterChange(e.target.value)}
            >
              {clusterNames.map(name => (
                <MenuItem key={name} value={name}>
                  {name}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          <FormControl size="small" sx={{ minWidth: 200 }}>
            <InputLabel>GVK</InputLabel>
            <Select label="GVK" value="v1/Namespaces" disabled>
              <MenuItem value="v1/Namespaces">v1/Namespaces</MenuItem>
            </Select>
          </FormControl>
          <FormControl size="small" sx={{ minWidth: 200 }} disabled={!selectedCluster}>
            <InputLabel>Resource Name</InputLabel>
            <Select
              label="Resource Name"
              value={selectedResourceName}
              onChange={e => onResourceNameChange(e.target.value)}
            >
              {namespaceNames.map(name => (
                <MenuItem key={name} value={name}>
                  {name}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
        </Box>
      </SectionBox>
      {selectedResourceName && (
        <SectionBox sx={{ mt: 4 }}>
          <EditorDialog
            item={namespaceLoading ? null : namespaceData}
            open
            onClose={() => {}}
            onSave={null}
            noDialog
            allowToHideManagedFields
          />
        </SectionBox>
      )}
    </>
  );
}
