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

import { SectionBox } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import {
  Box,
  Card,
  CardContent,
  Paper,
  Step,
  StepLabel,
  Stepper,
  Typography,
} from '@mui/material';
import { useTheme } from '@mui/material/styles';
import { keyframes } from '@mui/system';
import { ClusterResourcePlacement } from '../../resources/clusterResourcePlacement';

const pulseAnimation = keyframes`
  0% { transform: scale(1); opacity: 0.8; }
  100% { transform: scale(2.4); opacity: 0; }
`;

const iconGlowAnimation = keyframes`
  0% { filter: drop-shadow(0 0 5px #0288d1); }
  100% { filter: drop-shadow(0 0 0px transparent); }
`;

interface StatusIndicatorCardProps {
  color: string;
  label: string;
}

function StatusIndicatorCard({ color, label }: StatusIndicatorCardProps) {
  return (
    <Paper
      elevation={3}
      sx={{
        width: 240,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 3,
        gap: 1,
        backgroundColor: 'background.paper',
      }}
    >
      <Box
        sx={{
          position: 'relative',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Box
          sx={{
            position: 'absolute',
            width: 14,
            height: 14,
            borderRadius: '50%',
            backgroundColor: color,
            animation: `${pulseAnimation} 1.6s ease-out 3`,
          }}
        />
        <Box
          sx={{
            width: 14,
            height: 14,
            borderRadius: '50%',
            backgroundColor: color,
            position: 'relative',
          }}
        />
      </Box>
      <Typography variant="subtitle1">{label}</Typography>
    </Paper>
  );
}

export interface PlacementDetailOverviewProps {
  placement: ClusterResourcePlacement;
}

export function PlacementDetailOverview({ placement }: PlacementDetailOverviewProps) {
  const theme = useTheme();

  const isSyncd = placement.isSyncd;
  const syncColor =
    isSyncd === 'syncd'
      ? '#00c853'
      : isSyncd === 'out-of-sync'
      ? theme.palette.grey[400]
      : theme.palette.grey[300];
  const syncLabel =
    isSyncd === 'syncd' ? "Sync'd" : isSyncd === 'out-of-sync' ? 'Out of Sync' : 'Sync status N/A';

  const rolloutStatus = placement.rolloutStatus;
  const rolloutColor =
    rolloutStatus === 'completed'
      ? '#00c853'
      : rolloutStatus === 'in-progress'
      ? theme.palette.info.main
      : rolloutStatus === 'stuck'
      ? theme.palette.warning.main
      : rolloutStatus === 'stopping-stopped'
      ? theme.palette.grey[400]
      : rolloutStatus === 'failed'
      ? theme.palette.error.main
      : rolloutStatus === 'not-started'
      ? theme.palette.grey[300]
      : theme.palette.grey[300];
  const rolloutLabel =
    rolloutStatus === 'completed'
      ? 'Rollout Completed'
      : rolloutStatus === 'in-progress'
      ? 'Rollout In Progress'
      : rolloutStatus === 'stuck'
      ? 'Rollout Stuck'
      : rolloutStatus === 'stopping-stopped'
      ? 'Rollout Stopping/Stopped'
      : rolloutStatus === 'failed'
      ? 'Rollout Failed'
      : rolloutStatus === 'not-started'
      ? 'Rollout Not Started'
      : 'Rollout status N/A';

  const healthStatus = placement.healthStatus;
  const healthColor =
    healthStatus === 'available'
      ? '#00c853'
      : healthStatus === 'partially-available'
      ? theme.palette.warning.main
      : healthStatus === 'failed'
      ? theme.palette.error.main
      : theme.palette.grey[300];
  const healthLabel =
    healthStatus === 'available'
      ? 'Available'
      : healthStatus === 'partially-available'
      ? 'Partially Available'
      : healthStatus === 'failed'
      ? 'Failed'
      : 'Availability status N/A';

  const stageToStep: Record<string, number> = { scheduled: 1, 'rolled-out': 2, available: 3 };
  const activeStep = stageToStep[placement.currentStage ?? ''] ?? 0;

  return (
    <SectionBox title="Overview">
      <Box display="flex" flexDirection="row" gap={2}>
        <StatusIndicatorCard color={syncColor} label={syncLabel} />
        {rolloutStatus !== 'na' && (
          <StatusIndicatorCard color={rolloutColor} label={rolloutLabel} />
        )}
        {healthStatus !== null && <StatusIndicatorCard color={healthColor} label={healthLabel} />}
      </Box>
      <Typography variant="h6" sx={{ mt: 3, mb: 1, fontSize: '1rem', fontWeight: 600 }}>
        Progression
      </Typography>
      <Stepper
        activeStep={activeStep}
        sx={{
          py: 2,
          width: { xs: '100%', md: 900 },
          '& .MuiStepLabel-label': { fontSize: '1rem' },
          '& .MuiStepIcon-root.Mui-completed': {
            color: '#43a047',
            filter: 'drop-shadow(0 0 4px rgba(67, 160, 71, 0.6))',
          },
          '& .MuiStepIcon-root.Mui-active': {
            color: '#0288d1',
            animation: `${iconGlowAnimation} 1.6s ease-out infinite`,
          },
        }}
      >
        {[
          { label: 'Scheduled', description: 'The placement has been scheduled.' },
          {
            label: 'Rolled Out',
            description: 'Resources have been rolled out to the target clusters.',
          },
          { label: 'All Resources Available', description: 'All resources are available.' },
        ].map(({ label, description }) => (
          <Step key={label}>
            <StepLabel optional={<Typography variant="caption">{description}</Typography>}>
              {label}
            </StepLabel>
          </Step>
        ))}
      </Stepper>
      <Typography variant="h6" sx={{ mt: 3, mb: 1, fontSize: '1rem', fontWeight: 600 }}>
        Features
      </Typography>
      <Box display="flex" flexDirection="row" gap={2}>
        <Card sx={{ minWidth: 275, maxWidth: 360 }}>
          <CardContent>
            <Typography sx={{ color: 'text.secondary', fontSize: 14 }} gutterBottom>
              Rollout Strategy
            </Typography>
            <Typography variant="h5" component="div" fontWeight="bold">
              {placement.rolloutStrategyType === 'External' ? 'Staged Update' : 'Rolling Update'}
            </Typography>
            <Typography variant="body2">
              {placement.rolloutStrategyType === 'External'
                ? 'Roll out changes stage by stage on demand.'
                : 'Roll out changes automatically.'}
            </Typography>
          </CardContent>
        </Card>
        <Card sx={{ minWidth: 275, maxWidth: 360 }}>
          <CardContent>
            <Typography sx={{ color: 'text.secondary', fontSize: 14 }} gutterBottom>
              Apply Strategy
            </Typography>
            <Typography variant="h5" component="div" fontWeight="bold">
              {placement.applyStrategyType === 'ServerSideApply'
                ? 'Server-Side Apply'
                : 'Client-Side Apply'}
            </Typography>
            <Typography variant="body2">
              {placement.applyStrategyType === 'ServerSideApply'
                ? 'Use server-side apply to apply resources.'
                : 'Use three-way merge to apply resources, similar to how the Kubernetes CLI performs a client-side apply.'}
            </Typography>
          </CardContent>
        </Card>
      </Box>
    </SectionBox>
  );
}
