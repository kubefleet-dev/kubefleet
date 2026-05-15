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
  DateLabel,
  SectionBox,
  SimpleTable,
  StatusLabel,
} from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { Box, IconButton, Menu, MenuItem } from '@mui/material';
import React from 'react';
import { ClusterResourcePlacement } from '../../resources/clusterResourcePlacement';
import { PlacementStatusMap } from './Overview';

interface PlacementActionsMenuProps {
  onViewDetails: () => void;
}

function PlacementActionsMenu({ onViewDetails }: PlacementActionsMenuProps) {
  const [anchor, setAnchor] = React.useState<null | HTMLElement>(null);

  return (
    <>
      <IconButton size="small" onClick={e => setAnchor(e.currentTarget)}>
        <Icon icon="mdi:dots-vertical" />
      </IconButton>
      <Menu anchorEl={anchor} open={Boolean(anchor)} onClose={() => setAnchor(null)}>
        <MenuItem
          onClick={() => {
            setAnchor(null);
            onViewDetails();
          }}
        >
          View details
        </MenuItem>
        <MenuItem onClick={() => setAnchor(null)}>Start a rollout</MenuItem>
        <MenuItem onClick={() => setAnchor(null)}>Edit</MenuItem>
        <MenuItem onClick={() => setAnchor(null)} sx={{ color: 'error.main' }}>
          Delete
        </MenuItem>
      </Menu>
    </>
  );
}

interface PlacementsListProps {
  placements: ClusterResourcePlacement[] | null;
  statusMap: PlacementStatusMap;
  error: Error | null;
  onViewDetails: (name: string) => void;
}

export function PlacementsList({
  placements,
  statusMap,
  error,
  onViewDetails,
}: PlacementsListProps) {
  return (
    <SectionBox title="Placements">
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
            getter: item => <DateLabel date={item.metadata.creationTimestamp} format="mini" />,
          },
          {
            label: 'Selected Resources',
            gridTemplate: '1fr',
            getter: item => item.selectedResourcesCount,
          },
          {
            label: 'Selected Clusters',
            gridTemplate: '1fr',
            getter: item => item.selectedClustersCount,
          },
          {
            label: "Sync'd",
            gridTemplate: '1fr',
            getter: item => {
              const isSyncd = statusMap[item.metadata.name]?.isSyncd;
              if (isSyncd === 'syncd') return <StatusLabel status="success">Sync'd</StatusLabel>;
              if (isSyncd === 'out-of-sync')
                return (
                  <StatusLabel status="" sx={{ backgroundColor: '#c9b84c', color: 'grey.900' }}>
                    Out of sync
                  </StatusLabel>
                );
              return (
                <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                  Not Applicable
                </StatusLabel>
              );
            },
          },
          {
            label: 'Rollout Status',
            gridTemplate: '1fr',
            getter: item => {
              const entry = statusMap[item.metadata.name];
              const status = entry?.rolloutStatus;

              const n = (count: number, singular: string) =>
                `${count} rollout${count !== 1 ? 's' : ''} ${singular}`;

              const subLabel = (count: number, text: string) =>
                count > 0 ? (
                  <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                    {n(count, text)}
                  </StatusLabel>
                ) : null;

              if (status === 'in-progress') {
                const count = entry?.inProgressRollouts ?? 0;
                return (
                  <Box display="flex" flexDirection="column" alignItems="flex-start" gap={0.5}>
                    <StatusLabel
                      status=""
                      sx={{ backgroundColor: 'info.main', color: 'info.contrastText' }}
                    >
                      {n(count, 'in progress')}
                    </StatusLabel>
                    {subLabel(entry?.stoppingRollouts ?? 0, 'stopping')}
                    {subLabel(entry?.stoppedRollouts ?? 0, 'stopped')}
                    {subLabel(entry?.stuckRollouts ?? 0, 'stuck')}
                    {subLabel(entry?.failedRollouts ?? 0, 'failed')}
                    {subLabel(entry?.completedRollouts ?? 0, 'completed')}
                  </Box>
                );
              }
              if (status === 'stuck') {
                const count = entry?.stuckRollouts ?? 0;
                return (
                  <Box display="flex" flexDirection="column" alignItems="flex-start" gap={0.5}>
                    <StatusLabel status="warning">{n(count, 'stuck')}</StatusLabel>
                    {subLabel(entry?.stoppingRollouts ?? 0, 'stopping')}
                    {subLabel(entry?.stoppedRollouts ?? 0, 'stopped')}
                    {subLabel(entry?.failedRollouts ?? 0, 'failed')}
                    {subLabel(entry?.completedRollouts ?? 0, 'completed')}
                  </Box>
                );
              }
              if (status === 'stopping-stopped') {
                const total = (entry?.stoppingRollouts ?? 0) + (entry?.stoppedRollouts ?? 0);
                return (
                  <Box display="flex" flexDirection="column" alignItems="flex-start" gap={0.5}>
                    <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                      {n(total, 'stopping/stopped')}
                    </StatusLabel>
                    {subLabel(entry?.stoppingRollouts ?? 0, 'stopping')}
                    {subLabel(entry?.stoppedRollouts ?? 0, 'stopped')}
                    {subLabel(entry?.failedRollouts ?? 0, 'failed')}
                    {subLabel(entry?.completedRollouts ?? 0, 'completed')}
                  </Box>
                );
              }
              if (status === 'failed') {
                const count = entry?.failedRollouts ?? 0;
                return (
                  <Box display="flex" flexDirection="column" alignItems="flex-start" gap={0.5}>
                    <StatusLabel status="error">{n(count, 'failed')}</StatusLabel>
                    {subLabel(entry?.stoppingRollouts ?? 0, 'stopping')}
                    {subLabel(entry?.stoppedRollouts ?? 0, 'stopped')}
                    {subLabel(entry?.completedRollouts ?? 0, 'completed')}
                  </Box>
                );
              }
              if (status === 'completed')
                return <StatusLabel status="success">Completed</StatusLabel>;
              if (status === 'not-started')
                return (
                  <StatusLabel status="" sx={{ backgroundColor: 'grey.300', color: 'grey.900' }}>
                    Not Started
                  </StatusLabel>
                );
              if (status === 'na')
                return (
                  <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                    Not Applicable
                  </StatusLabel>
                );
              return null;
            },
          },
          {
            label: 'Health Check Status',
            gridTemplate: '1fr',
            getter: item => {
              const entry = statusMap[item.metadata.name];
              const status = entry?.healthStatus;
              if (status === 'available') {
                const available = entry?.availableClusters ?? 0;
                const selected = entry?.selectedClusters ?? 0;
                return (
                  <StatusLabel status="success">
                    {available} of {selected} clusters available
                  </StatusLabel>
                );
              }
              if (status === 'partially-available') {
                const unavailable = entry?.unavailableClusters ?? 0;
                const unknown = entry?.notAvailableCheckedClusters ?? 0;
                const notApplied = entry?.notAppliedClusters ?? 0;
                return (
                  <Box display="flex" flexDirection="column" alignItems="flex-start" gap={0.5}>
                    <StatusLabel status="warning">Partially Available</StatusLabel>
                    {unavailable > 0 && (
                      <StatusLabel
                        status=""
                        sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}
                      >
                        {unavailable} clusters unavailable
                      </StatusLabel>
                    )}
                    {unknown > 0 && (
                      <StatusLabel
                        status=""
                        sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}
                      >
                        {unknown} clusters having unknown availability
                      </StatusLabel>
                    )}
                    {notApplied > 0 && (
                      <StatusLabel
                        status=""
                        sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}
                      >
                        {notApplied} clusters not having resources applied yet
                      </StatusLabel>
                    )}
                  </Box>
                );
              }
              if (status === 'failed') return <StatusLabel status="error">Failed</StatusLabel>;
              if (status === 'na')
                return (
                  <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                    Not Applicable
                  </StatusLabel>
                );
              return null;
            },
          },
          {
            label: '',
            gridTemplate: 'min-content',
            getter: item => (
              <PlacementActionsMenu onViewDetails={() => onViewDetails(item.metadata.name)} />
            ),
          },
        ]}
        data={placements}
        errorMessage={error ? error.message : null}
        sx={{ width: '100%', '& .MuiTableCell-root': { fontSize: '1.1rem' } }}
      />
    </SectionBox>
  );
}
