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

import { Icon } from '@iconify/react';
import { request } from '@kinvolk/headlamp-plugin/lib/ApiProxy';
import { EditorDialog, SectionBox } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import {
  Box,
  Button,
  Chip,
  Dialog,
  DialogContent,
  DialogTitle,
  FormControl,
  IconButton,
  MenuItem,
  Select,
  SelectChangeEvent,
} from '@mui/material';
import React from 'react';
import { SelectedResource } from '../../resources/clusterResourcePlacement';

type Mode = 'closed' | 'single' | 'sideBySide';

function formatResource(r: SelectedResource): string {
  const gvk = r.group ? `${r.group}/${r.version}/${r.kind}` : `${r.version}/${r.kind}`;
  const namespacedName = r.namespace ? `${r.namespace}/${r.name}` : r.name;
  return `(${gvk}) ${namespacedName}`;
}

function buildResourceUrl(r: SelectedResource): string {
  const { group, version, kind, name, namespace } = r;
  const plural = kind.toLowerCase() + 's';
  if (group) {
    return namespace
      ? `/apis/${group}/${version}/namespaces/${namespace}/${plural}/${name}`
      : `/apis/${group}/${version}/${plural}/${name}`;
  }
  return namespace
    ? `/api/${version}/namespaces/${namespace}/${plural}/${name}`
    : `/api/${version}/${plural}/${name}`;
}

const DIALOG_PAPER_SX = { width: '80vw', height: '80vh', maxWidth: '80vw' };

interface ResourceDialogContentProps {
  clusterName: string;
  selectedResources: SelectedResource[];
  selectedIdx: string;
  onChangeIdx: (e: SelectChangeEvent) => void;
  onClose: () => void;
  onToggleSideBySide: () => void;
  sideBySide: false;
  resourceData: object | null;
  loading: boolean;
}

interface SideBySideDialogContentProps {
  clusterName: string;
  selectedResources: SelectedResource[];
  selectedIdx: string;
  onChangeIdx: (e: SelectChangeEvent) => void;
  onClose: () => void;
  onToggleSideBySide: () => void;
  sideBySide: true;
  resourceData: object | null;
  loading: boolean;
  hubResourceData: object | null;
  hubLoading: boolean;
}

type DialogContentProps = ResourceDialogContentProps | SideBySideDialogContentProps;

function BrowseDialogContent(props: DialogContentProps) {
  const { clusterName, selectedResources, selectedIdx, onChangeIdx, onClose, onToggleSideBySide, sideBySide, resourceData, loading } = props;

  return (
    <>
      <SectionBox>
        <Box display="flex" alignItems="center">
          <FormControl size="small">
            <Select value={selectedIdx} onChange={onChangeIdx}>
              {selectedResources.map((r, i) => (
                <MenuItem key={formatResource(r)} value={String(i)}>
                  {formatResource(r)}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          <Box flexGrow={1} />
          <Button variant="outlined" size="small" onClick={onToggleSideBySide}>
            {sideBySide ? 'Disable Side-By-Side View' : 'Compare with Hub Cluster Side-By-Side'}
          </Button>
        </Box>
      </SectionBox>
      <SectionBox sx={{ mt: 3 }}>
        {sideBySide ? (
          <Box display="flex" flexDirection="row" gap={2}>
            <Box flex={1} minWidth={0}>
              <Box display="flex" justifyContent="center" mb={1}>
                <Chip label={clusterName} sx={{ fontWeight: 'bold', fontSize: '1rem' }} />
              </Box>
              <EditorDialog
                title={clusterName}
                item={loading ? null : resourceData}
                open
                onClose={onClose}
                onSave={null}
                noDialog
                allowToHideManagedFields
              />
            </Box>
            <Box flex={1} minWidth={0}>
              <Box display="flex" justifyContent="center" mb={1}>
                <Chip label="Hub Cluster" sx={{ fontWeight: 'bold', fontSize: '1rem' }} />
              </Box>
              <EditorDialog
                title="Hub Cluster"
                item={props.hubLoading ? null : props.hubResourceData}
                open
                onClose={onClose}
                onSave={null}
                noDialog
                allowToHideManagedFields
              />
            </Box>
          </Box>
        ) : (
          <EditorDialog
            item={loading ? null : resourceData}
            open
            onClose={onClose}
            onSave={null}
            noDialog
            allowToHideManagedFields
          />
        )}
      </SectionBox>
    </>
  );
}

export interface BrowseResourcesButtonProps {
  clusterName: string;
  selectedResources: SelectedResource[];
}

export function BrowseResourcesButton({
  clusterName,
  selectedResources,
}: BrowseResourcesButtonProps) {
  const [mode, setMode] = React.useState<Mode>('closed');
  const [selectedIdx, setSelectedIdx] = React.useState('0');
  const [resourceData, setResourceData] = React.useState<object | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [hubResourceData, setHubResourceData] = React.useState<object | null>(null);
  const [hubLoading, setHubLoading] = React.useState(false);

  const isOpen = mode !== 'closed';
  const sideBySide = mode === 'sideBySide';
  const selectedResource = selectedResources[parseInt(selectedIdx, 10)] ?? null;

  // Fetch resource from the member cluster whenever the modal is open
  React.useEffect(() => {
    if (!isOpen || !selectedResource) {
      setResourceData(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setResourceData(null);
    request(buildResourceUrl(selectedResource), { cluster: clusterName })
      .then(data => { if (!cancelled) setResourceData(data); })
      .catch(() => { if (!cancelled) setResourceData(null); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [isOpen, selectedIdx, clusterName]);

  // Fetch resource from the hub cluster only in side-by-side mode
  React.useEffect(() => {
    if (!sideBySide || !selectedResource) {
      setHubResourceData(null);
      return;
    }
    let cancelled = false;
    setHubLoading(true);
    setHubResourceData(null);
    request(buildResourceUrl(selectedResource))
      .then(data => { if (!cancelled) setHubResourceData(data); })
      .catch(() => { if (!cancelled) setHubResourceData(null); })
      .finally(() => { if (!cancelled) setHubLoading(false); });
    return () => { cancelled = true; };
  }, [sideBySide, selectedIdx]);

  const handleChangeIdx = (e: SelectChangeEvent) => setSelectedIdx(e.target.value);
  const handleClose = () => setMode('closed');
  const handleToggleSideBySide = () => setMode(m => (m === 'sideBySide' ? 'single' : 'sideBySide'));

  const sharedContentProps = {
    clusterName,
    selectedResources,
    selectedIdx,
    onChangeIdx: handleChangeIdx,
    onClose: handleClose,
    onToggleSideBySide: handleToggleSideBySide,
    resourceData,
    loading,
  };

  const dialogTitleSx = { display: 'flex', alignItems: 'center', gap: 1 };
  const closeBtn = (
    <IconButton onClick={handleClose} size="small" aria-label="close">
      <Icon icon="mdi:close" />
    </IconButton>
  );

  return (
    <>
      <Button variant="contained" size="small" onClick={() => setMode('single')}>
        Browse Resources
      </Button>

      {/* Single view dialog */}
      {mode === 'single' && (
        <Dialog open onClose={handleClose} maxWidth={false} PaperProps={{ sx: DIALOG_PAPER_SX }}>
          <DialogTitle sx={dialogTitleSx}>
            {closeBtn}
            Browse Resources — {clusterName}
          </DialogTitle>
          <DialogContent>
            <BrowseDialogContent {...sharedContentProps} sideBySide={false} />
          </DialogContent>
        </Dialog>
      )}

      {/* Side-by-side dialog */}
      {mode === 'sideBySide' && (
        <Dialog open onClose={handleClose} maxWidth={false} PaperProps={{ sx: DIALOG_PAPER_SX }}>
          <DialogTitle sx={dialogTitleSx}>
            {closeBtn}
            Browse Resources — {clusterName}
          </DialogTitle>
          <DialogContent>
            <BrowseDialogContent
              {...sharedContentProps}
              sideBySide
              hubResourceData={hubResourceData}
              hubLoading={hubLoading}
            />
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}
