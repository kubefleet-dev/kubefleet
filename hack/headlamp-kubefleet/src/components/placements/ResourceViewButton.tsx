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

import { request } from '@kinvolk/headlamp-plugin/lib/ApiProxy';
import { EditorDialog } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { Button } from '@mui/material';
import React from 'react';
import { SelectedResource } from '../../resources/clusterResourcePlacement';

export function ResourceViewButton({ resource }: { resource: SelectedResource }) {
  const [open, setOpen] = React.useState(false);
  const [resourceData, setResourceData] = React.useState<object | null>(null);
  const [loading, setLoading] = React.useState(false);

  const handleOpen = async () => {
    setLoading(true);
    try {
      const { group, version, kind, name, namespace } = resource;
      const plural = kind.toLowerCase() + 's';
      const url = group
        ? namespace
          ? `/apis/${group}/${version}/namespaces/${namespace}/${plural}/${name}`
          : `/apis/${group}/${version}/${plural}/${name}`
        : namespace
        ? `/api/${version}/namespaces/${namespace}/${plural}/${name}`
        : `/api/${version}/${plural}/${name}`;

      const data = await request(url);
      setResourceData(data);
      setOpen(true);
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      <Button
        variant="contained"
        color="primary"
        size="small"
        onClick={() => void handleOpen()}
        disabled={loading}
      >
        View/Edit on Hub Cluster
      </Button>
      {open && resourceData && (
        <EditorDialog
          item={resourceData}
          open={open}
          onClose={() => setOpen(false)}
          onSave={'default'}
          maxWidth={false}
          PaperProps={{ sx: { width: '80vw', height: '80vh', maxWidth: '80vw' } }}
          withFullScreen={false}
          allowToHideManagedFields
        />
      )}
    </>
  );
}
