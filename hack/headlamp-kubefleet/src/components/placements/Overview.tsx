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

import { SectionBox, TileChart } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { Box, Paper, Typography } from '@mui/material';
import { useTheme } from '@mui/material/styles';

export interface PlacementStatus {
  isSyncd: 'syncd' | 'out-of-sync' | 'na';
  rolloutStatus:
    | 'in-progress'
    | 'stuck'
    | 'stopping-stopped'
    | 'failed'
    | 'completed'
    | 'not-started'
    | 'na';
  healthStatus: 'failed' | 'available' | 'partially-available' | 'na' | null;
  currentStage: string | null;
  applyStrategyType: 'ClientSideApply' | 'ServerSideApply' | null;
  inProgressRollouts: number;
  completedRollouts: number;
  stoppingRollouts: number;
  stoppedRollouts: number;
  stuckRollouts: number;
  failedRollouts: number;
  selectedClusters: number;
  upToDateClusters: number;
  notAppliedClusters: number;
  notAvailableCheckedClusters: number;
  unavailableClusters: number;
  availableClusters: number;
}

export type PlacementStatusMap = Record<string, PlacementStatus>;

interface PlacementsOverviewProps {
  statusMap: PlacementStatusMap;
}

export function PlacementsOverview({ statusMap }: PlacementsOverviewProps) {
  const theme = useTheme();

  const statuses = Object.values(statusMap);
  const total = statuses.length;

  const syncd = statuses.filter(s => s.isSyncd === 'syncd').length;
  const notSyncd = statuses.filter(s => s.isSyncd === 'out-of-sync').length;

  const rolloutInProgress = statuses.filter(s => s.rolloutStatus === 'in-progress').length;
  const rolloutStuck = statuses.filter(s => s.rolloutStatus === 'stuck').length;
  const rolloutStoppingStopped = statuses.filter(s => s.rolloutStatus === 'stopping-stopped').length;
  const rolloutFailed = statuses.filter(s => s.rolloutStatus === 'failed').length;
  const rolloutCompleted = statuses.filter(s => s.rolloutStatus === 'completed').length;
  const rolloutNotStarted = statuses.filter(s => s.rolloutStatus === 'not-started').length;

  const healthFailed = statuses.filter(s => s.healthStatus === 'failed').length;
  const healthAvailable = statuses.filter(s => s.healthStatus === 'available').length;
  const healthPartial = statuses.filter(s => s.healthStatus === 'partially-available').length;

  return (
    <SectionBox title="Overview">
      <Box display="flex" flexDirection="row" gap={2}>
        <Paper
          elevation={3}
          sx={{
            width: 240,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            padding: 3,
            backgroundColor: 'background.paper',
          }}
        >
          <Typography variant="h1">{total}</Typography>
          <Typography variant="subtitle1">Total Placements</Typography>
        </Paper>
        <Box width={280} display="flex" alignItems="center" justifyContent="center">
          <TileChart
            title="Synchronization"
            data={[
              { name: "Sync'd", value: syncd, fill: '#00c853' },
              { name: "Not Sync'd", value: notSyncd, fill: theme.palette.grey[400] },
            ]}
            total={syncd + notSyncd}
            label={syncd + notSyncd > 0 ? `${Math.round((syncd / (syncd + notSyncd)) * 100)}%` : '—'}
            legend={`${syncd} sync'd, ${notSyncd} out of sync`}
          />
        </Box>
        <Box width={280} display="flex" alignItems="center" justifyContent="center">
          <TileChart
            title="Rollout"
            data={[
              { name: 'In Progress', value: rolloutInProgress, fill: theme.palette.info.main },
              { name: 'Stuck', value: rolloutStuck, fill: theme.palette.warning.main },
              { name: 'Stopping/Stopped', value: rolloutStoppingStopped, fill: theme.palette.grey[400] },
              { name: 'Failed', value: rolloutFailed, fill: theme.palette.error.main },
              { name: 'Completed', value: rolloutCompleted, fill: '#00c853' },
              { name: 'Not Started', value: rolloutNotStarted, fill: theme.palette.grey[300] },
            ]}
            total={rolloutInProgress + rolloutStuck + rolloutStoppingStopped + rolloutFailed + rolloutCompleted + rolloutNotStarted}
            label={rolloutInProgress > 0 ? `${Math.round((rolloutInProgress / (rolloutInProgress + rolloutStuck + rolloutStoppingStopped + rolloutFailed + rolloutCompleted + rolloutNotStarted)) * 100)}%` : '—'}
            legend={`${rolloutCompleted} completed, ${rolloutInProgress} in progress, ${rolloutStuck} stuck, ${rolloutFailed} failed`}
          />
        </Box>
        <Box width={280} display="flex" alignItems="center" justifyContent="center">
          <TileChart
            title="Health Check"
            data={[
              { name: 'Failed', value: healthFailed, fill: theme.palette.error.main },
              { name: 'Available', value: healthAvailable, fill: '#00c853' },
              {
                name: 'Partially Available',
                value: healthPartial,
                fill: '#fdd835',
              },
            ]}
            total={healthAvailable + healthPartial + healthFailed}
            label={healthAvailable + healthPartial + healthFailed > 0 ? `${Math.round((healthAvailable / (healthAvailable + healthPartial + healthFailed)) * 100)}%` : '—'}
            legend={`${healthAvailable} available, ${healthPartial} partially available, ${healthFailed} failed`}
          />
        </Box>
      </Box>
    </SectionBox>
  );
}
