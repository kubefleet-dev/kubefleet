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
import {
  SimpleTable,
  StatusLabel,
} from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { Box, Chip, IconButton, Menu, MenuItem } from '@mui/material';
import React from 'react';
import { MemberCluster } from '../../resources/memberCluster';
import { EditLabelsDialog } from './EditLabelsDialog';
import { EditTaintsDialog } from './EditTaintsDialog';

interface MemberClusterActionsMenuProps {
  cluster: MemberCluster;
}

function MemberClusterActionsMenu({ cluster }: MemberClusterActionsMenuProps) {
  const [anchor, setAnchor] = React.useState<null | HTMLElement>(null);
  const [editLabelsOpen, setEditLabelsOpen] = React.useState(false);
  const [editTaintsOpen, setEditTaintsOpen] = React.useState(false);

  return (
    <>
      <IconButton size="small" onClick={e => setAnchor(e.currentTarget)}>
        <Icon icon="mdi:dots-vertical" />
      </IconButton>
      <Menu anchorEl={anchor} open={Boolean(anchor)} onClose={() => setAnchor(null)}>
        <MenuItem onClick={() => { setAnchor(null); setEditLabelsOpen(true); }}>Edit Labels</MenuItem>
        <MenuItem onClick={() => { setAnchor(null); setEditTaintsOpen(true); }}>Edit Taints</MenuItem>
        <MenuItem onClick={() => setAnchor(null)}>View details</MenuItem>
      </Menu>
      <EditLabelsDialog
        cluster={cluster}
        open={editLabelsOpen}
        onClose={() => setEditLabelsOpen(false)}
      />
      <EditTaintsDialog
        cluster={cluster}
        open={editTaintsOpen}
        onClose={() => setEditTaintsOpen(false)}
      />
    </>
  );
}

interface MemberClustersListProps {
  clusters: MemberCluster[] | null;
  error: Error | null;
}

export function MemberClustersList({ clusters, error }: MemberClustersListProps) {
  return (
    <SimpleTable
      columns={[
        {
          label: 'Name',
          gridTemplate: '1fr',
          getter: item => item.metadata.name,
        },
        {
          label: 'Age',
          gridTemplate: '1fr',
          getter: item => {
            const created = item.metadata.creationTimestamp;
            if (!created) return '—';
            const diff = Date.now() - new Date(created).getTime();
            const minutes = Math.floor(diff / 60000);
            const hours = Math.floor(minutes / 60);
            const days = Math.floor(hours / 24);
            if (days > 0) return `${days}d`;
            if (hours > 0) return `${hours}h`;
            return `${minutes}m`;
          },
        },
        {
          label: 'Joined',
          gridTemplate: '1fr',
          getter: item => {
            const joined = item.conditions.find(c => c.type === 'Joined');
            if (joined?.status === 'True') return <StatusLabel status="success">Joined</StatusLabel>;
            if (joined?.status === 'False') return <StatusLabel status="error">Not Joined</StatusLabel>;
            return (
              <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                Unknown
              </StatusLabel>
            );
          },
        },
        {
          label: 'Health',
          gridTemplate: '1fr',
          getter: item => {
            const memberAgent = item.agentStatus.find(a => a.type === 'MemberAgent');
            const healthy = memberAgent?.conditions?.find(c => c.type === 'Healthy');
            if (healthy?.status === 'True') return <StatusLabel status="success">Healthy</StatusLabel>;
            if (healthy?.status === 'False') return <StatusLabel status="error">Unhealthy</StatusLabel>;
            return (
              <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                Unknown
              </StatusLabel>
            );
          },
        },
        {
          label: 'Labels',
          gridTemplate: '2fr',
          getter: item => {
            const labels = item.metadata.labels;
            if (!labels || Object.keys(labels).length === 0) return '—';
            return (
              <Box display="flex" flexWrap="wrap" gap={0.5}>
                {Object.entries(labels).map(([key, value]) => (
                  <Chip key={key} label={`${key}=${value}`} size="small" />
                ))}
              </Box>
            );
          },
        },
        {
          label: 'Taints',
          gridTemplate: '2fr',
          getter: item => {
            const taints = item.taints;
            if (taints.length === 0) return '—';
            return (
              <Box display="flex" flexWrap="wrap" gap={0.5}>
                {taints.map((taint, i) => (
                  <Chip
                    key={i}
                    label={taint.value ? `${taint.key}=${taint.value}:${taint.effect}` : `${taint.key}:${taint.effect}`}
                    size="small"
                  />
                ))}
              </Box>
            );
          },
        },
        {
          label: '',
          gridTemplate: 'min-content',
          getter: item => <MemberClusterActionsMenu cluster={item} />,
        },
      ]}
      data={clusters}
      errorMessage={error ? error.message : null}
      sx={{ width: '100%', '& .MuiTableCell-root': { fontSize: '1.1rem' } }}
    />
  );
}
