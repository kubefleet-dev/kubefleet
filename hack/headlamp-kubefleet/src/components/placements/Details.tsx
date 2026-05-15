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
  SectionBox,
  SimpleTable,
  StatusLabel,
} from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { Box, Chip, IconButton, Typography } from '@mui/material';
import React from 'react';
import {
  ClusterResourcePlacement,
  PerClusterPlacementStatus,
  SelectedResource,
} from '../../resources/clusterResourcePlacement';
import { BrowseResourcesButton } from './BrowseResourcesButton';
import { PlacementDetailOverview } from './DetailsOverview';
import { ResourceViewButton } from './ResourceViewButton';

const ROLLOUT_STATES = ['Completed', 'Failed', 'InProgress', 'NotStarted'] as const;
type RolloutState = (typeof ROLLOUT_STATES)[number];

export interface ClusterRolloutStateCounts {
  Completed: number;
  Failed: number;
  InProgress: number;
  NotStarted: number;
}

export type ClusterRolloutStateMap = Record<string, ClusterRolloutStateCounts>;

function clusterSyncStatus(
  cluster: PerClusterPlacementStatus,
  latestIndex: number | null,
  hasResources: boolean
): 'synced' | 'out-of-sync' | 'na' {
  if (!hasResources || latestIndex === null) return 'na';
  return cluster.observedResourceIndex === latestIndex ? 'synced' : 'out-of-sync';
}

export interface PlacementDetailViewProps {
  placement: ClusterResourcePlacement;
  onBack: () => void;
}

export function PlacementDetailView({ placement, onBack }: PlacementDetailViewProps) {
  const clusterRolloutStateMap = React.useMemo<ClusterRolloutStateMap>(() => {
    const map: ClusterRolloutStateMap = {};
    for (const cluster of placement.selectedClusterStatuses) {
      const counts: ClusterRolloutStateCounts = {
        Completed: 0,
        Failed: 0,
        InProgress: 0,
        NotStarted: 0,
      };
      if (cluster.rolloutState) {
        for (const state of Object.values(cluster.rolloutState)) {
          if ((ROLLOUT_STATES as readonly string[]).includes(state)) {
            counts[state as RolloutState]++;
          }
        }
      }
      map[cluster.clusterName] = counts;
    }
    return map;
  }, [placement.selectedClusterStatuses]);

  const perClusterFailedResMap = React.useMemo<
    Record<string, { failedToApplyRes: number; unavailableRes: number }>
  >(() => {
    const map: Record<string, { failedToApplyRes: number; unavailableRes: number }> = {};
    for (const cluster of placement.selectedClusterStatuses) {
      const counts = { failedToApplyRes: 0, unavailableRes: 0 };
      for (const fp of cluster.failedPlacements ?? []) {
        if (fp.condition.type === 'Applied') counts.failedToApplyRes++;
        else if (fp.condition.type === 'Available') counts.unavailableRes++;
      }
      map[cluster.clusterName] = counts;
    }
    return map;
  }, [placement.selectedClusterStatuses]);

  return (
    <>
      <SectionBox>
        <Box display="flex" flexDirection="row" alignItems="center" gap={1} mt={2}>
          <IconButton size="small" onClick={onBack}>
            <Icon icon="mdi:arrow-left" />
          </IconButton>
          <Typography variant="h5">{placement.metadata.name}</Typography>
        </Box>
      </SectionBox>
      <PlacementDetailOverview placement={placement} />
      <SectionBox title="Selected Resources">
        <SimpleTable
          columns={[
            {
              label: 'Kind',
              getter: (r: SelectedResource) => <Chip label={r.kind} size="small" />,
            },
            {
              label: 'Group/Version',
              getter: (r: SelectedResource) => (
                <Chip label={r.group ? `${r.group}/${r.version}` : r.version} size="small" />
              ),
            },
            { label: 'Namespace', getter: (r: SelectedResource) => r.namespace || '—' },
            { label: 'Name', getter: (r: SelectedResource) => r.name },
            {
              label: '',
              gridTemplate: 'fit-content(100%)',
              getter: (r: SelectedResource) => <ResourceViewButton resource={r} />,
            },
          ]}
          data={placement.selectedResources}
          sx={{ '& .MuiTableCell-root': { fontSize: '1.1rem' } }}
        />
      </SectionBox>
      <SectionBox title="Selected Clusters">
        <SimpleTable
          columns={[
            {
              label: 'Cluster',
              getter: (c: PerClusterPlacementStatus) => c.clusterName,
            },
            {
              label: 'Sync Status',
              getter: (c: PerClusterPlacementStatus) => {
                const s = clusterSyncStatus(
                  c,
                  placement.latestResourceSnapshotIndex,
                  placement.selectedResourcesCount > 0
                );
                if (s === 'synced') return <StatusLabel status="success">Sync'd</StatusLabel>;
                if (s === 'out-of-sync')
                  return (
                    <StatusLabel status="" sx={{ backgroundColor: '#c9b84c', color: 'grey.900' }}>
                      Out of Sync
                    </StatusLabel>
                  );
                return (
                  <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                    N/A
                  </StatusLabel>
                );
              },
            },
            {
              label: 'Resource Snapshot Index',
              getter: (c: PerClusterPlacementStatus) => {
                const current = c.observedResourceIndex;
                const latest = placement.latestResourceSnapshotIndex;
                if (current === undefined || current === null) return '—';
                if (latest === null) return String(current);
                const diff = latest - current;
                if (diff < 0) return '-';
                if (diff === 0) return `${current} (latest)`;
                return `${current} (${diff} behind latest)`;
              },
            },
            {
              label: 'Rollout Status',
              getter: (c: PerClusterPlacementStatus) => {
                const counts = clusterRolloutStateMap[c.clusterName] ?? {
                  Completed: 0,
                  Failed: 0,
                  InProgress: 0,
                  NotStarted: 0,
                };
                const { InProgress, NotStarted } = counts;

                if (InProgress > 0)
                  return (
                    <StatusLabel
                      status=""
                      sx={{ backgroundColor: 'info.main', color: 'info.contrastText' }}
                    >
                      {InProgress} rollout{InProgress !== 1 ? 's' : ''} in progress
                    </StatusLabel>
                  );
                if (NotStarted > 0)
                  return (
                    <StatusLabel status="" sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}>
                      {NotStarted} rollout{NotStarted !== 1 ? 's' : ''} not started
                    </StatusLabel>
                  );
                return <StatusLabel status="success">No active rollouts</StatusLabel>;
              },
            },
            {
              label: 'Health Check Status',
              getter: (c: PerClusterPlacementStatus) => {
                const { failedToApplyRes, unavailableRes } = perClusterFailedResMap[
                  c.clusterName
                ] ?? {
                  failedToApplyRes: 0,
                  unavailableRes: 0,
                };
                const N = placement.selectedResourcesCount;

                if (failedToApplyRes === 0 && unavailableRes === 0)
                  return <StatusLabel status="success">{N} resources available</StatusLabel>;

                if (failedToApplyRes > 0)
                  return (
                    <Box display="flex" flexDirection="column" alignItems="flex-start" gap={0.5}>
                      <StatusLabel status="error">
                        {failedToApplyRes} of {N} resources failed to apply
                      </StatusLabel>
                      {unavailableRes > 0 && (
                        <StatusLabel status="warning">
                          {unavailableRes} of {N} resources unavailable
                        </StatusLabel>
                      )}
                    </Box>
                  );

                return (
                  <StatusLabel status="warning">
                    {unavailableRes} of {N} resources unavailable
                  </StatusLabel>
                );
              },
            },
            {
              label: '',
              gridTemplate: 'fit-content(100%)',
              getter: (c: PerClusterPlacementStatus) => (
                <BrowseResourcesButton
                  clusterName={c.clusterName}
                  selectedResources={placement.selectedResources}
                />
              ),
            },
          ]}
          data={placement.selectedClusterStatuses}
          sx={{ '& .MuiTableCell-root': { fontSize: '1.1rem' } }}
        />
      </SectionBox>
    </>
  );
}
