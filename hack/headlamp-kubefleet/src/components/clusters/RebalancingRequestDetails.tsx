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
import { SectionBox } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { Box, Button, Card, CardContent, Chip, Dialog, DialogActions, DialogContent, DialogContentText, IconButton, Paper, Typography } from '@mui/material';
import { keyframes } from '@mui/system';
import React from 'react';
import { ClusterRebalancingRequest, ObjectReference } from '../../resources/clusterRebalancingRequest';
import { ClusterResourceBinding } from '../../resources/clusterResourceBinding';
import { ResourceBinding } from '../../resources/resourceBinding';
import { StatusCondition } from '../../resources/common';

function useBindingWithRefetch(ref_: ObjectReference) {
  const isNamespaced = ref_.kind === 'ResourceBinding';
  const [binding, setBinding] = React.useState<ClusterResourceBinding | ResourceBinding | null>(null);

  React.useEffect(() => {
    if (!ref_.name) return;

    const fetchBinding = isNamespaced
      ? ResourceBinding.apiGet(b => setBinding(b as ResourceBinding), ref_.name ?? '', ref_.namespace ?? '', undefined)
      : ClusterResourceBinding.apiGet(b => setBinding(b as ClusterResourceBinding), ref_.name ?? '', undefined, undefined);

    fetchBinding();
    const id = setInterval(fetchBinding, 5000);
    return () => clearInterval(id);
  }, [ref_.name, ref_.namespace, isNamespaced]);

  return binding;
}

const pulseAnimation = keyframes`
  0% { transform: scale(1); opacity: 0.8; }
  100% { transform: scale(2.4); opacity: 0; }
`;

interface MigrationFromItemProps {
  ref_: ObjectReference;
  rollback: boolean;
}

function MigrationFromItem({ ref_, rollback }: MigrationFromItemProps) {
  const binding = useBindingWithRefetch(ref_);
  const title = ref_.namespace ? `${ref_.namespace}/${ref_.name ?? '—'}` : (ref_.name ?? '—');

  let drainLabel = '';
  let drainSx: object = {};

  if (binding) {
    const annotations = binding.metadata?.annotations ?? {};
    const finalizers: string[] = binding.metadata?.finalizers ?? [];
    const hasAnnotation = 'kubernetes-fleet.io/is-withdrawn-binding' in annotations;
    const hasFinalizer = finalizers.includes('kubernetes-fleet.io/work-cleanup');

    if (!rollback) {
      if (hasAnnotation && !hasFinalizer) {
        drainLabel = 'Drained (All Resources Removed)';
        drainSx = { backgroundColor: '#1565c0', color: '#fff' };
      } else if (hasAnnotation && hasFinalizer) {
        drainLabel = 'Draining (Resources Pending Removal)';
        drainSx = { backgroundColor: '#f57f17', color: '#fff' };
      } else {
        drainLabel = 'Draining Not Started Yet';
      }
    } else {
      if (hasAnnotation) {
        drainLabel = 'Surging Not Started Yet';
      } else {
        const conditions: StatusCondition[] = binding.jsonData?.status?.conditions ?? [];
        const applied = conditions.find(c => c.type === 'Applied');
        const available = conditions.find(c => c.type === 'Available');
        if (available?.status === 'True') {
          drainLabel = 'Surged (Resources are Available)';
          drainSx = { backgroundColor: '#1565c0', color: '#fff' };
        } else if (available?.status === 'False') {
          drainLabel = 'Surged (Resources are not Available Yet)';
          drainSx = { backgroundColor: '#f57f17', color: '#fff' };
        } else if (applied?.status === 'True') {
          drainLabel = 'Surged (Resources Applied but Not Yet Available)';
          drainSx = { backgroundColor: '#f57f17', color: '#fff' };
        } else if (applied?.status === 'False') {
          drainLabel = 'Surged (Failed to Apply Resources)';
          drainSx = { backgroundColor: '#c62828', color: '#fff' };
        } else {
          drainLabel = 'Surged (Resources are not Applied Yet)';
        }
      }
    }
  }

  return (
    <Paper elevation={2} sx={{ p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Box display="flex" alignItems="center" gap={1}>
        <Chip label={ref_.kind ?? '—'} size="small" />
        <Typography variant="body2" fontWeight={600}>{title}</Typography>
      </Box>
      {drainLabel && <Chip label={drainLabel} size="small" sx={drainSx} />}
    </Paper>
  );
}

interface MigrationToItemProps {
  ref_: ObjectReference;
  rollback: boolean;
}

function MigrationToItem({ ref_, rollback }: MigrationToItemProps) {
  const binding = useBindingWithRefetch(ref_);
  const title = ref_.namespace ? `${ref_.namespace}/${ref_.name ?? '—'}` : (ref_.name ?? '—');

  let label = '';
  let chipSx: object = {};

  if (binding) {
    const annotations = binding.metadata?.annotations ?? {};
    const finalizers: string[] = binding.metadata?.finalizers ?? [];
    const hasAnnotation = 'kubernetes-fleet.io/is-withdrawn-binding' in annotations;
    const hasFinalizer = finalizers.includes('kubernetes-fleet.io/work-cleanup');

    if (rollback) {
      // to-side during rollback: being drained
      const hasProvisional = 'kubernetes-fleet.io/is-provisional-binding' in annotations;
      if (!hasProvisional) {
        label = 'No Draining Needed (Binding Already Exists)';
        chipSx = { backgroundColor: '#1565c0', color: '#fff' };
      } else if (hasAnnotation && !hasFinalizer) {
        label = 'Drained (All Resources Removed)';
        chipSx = { backgroundColor: '#1565c0', color: '#fff' };
      } else if (hasAnnotation && hasFinalizer) {
        label = 'Draining (Resources Pending Removal)';
        chipSx = { backgroundColor: '#f57f17', color: '#fff' };
      } else {
        label = 'Draining Not Started Yet';
      }
    } else {
      // to-side during migration: being surged
      const hasProvisional = 'kubernetes-fleet.io/is-provisional-binding' in annotations;
      if (!hasProvisional) {
        label = 'Surged (Binding Already Exists)';
        chipSx = { backgroundColor: '#1565c0', color: '#fff' };
      } else {
        const conditions: StatusCondition[] = binding.jsonData?.status?.conditions ?? [];
        const applied = conditions.find(c => c.type === 'Applied');
        const available = conditions.find(c => c.type === 'Available');
        if (available?.status === 'True') {
          label = 'Surged (Resources are Available)';
          chipSx = { backgroundColor: '#1565c0', color: '#fff' };
        } else if (available?.status === 'False') {
          label = 'Surged (Resources are not Available Yet)';
          chipSx = { backgroundColor: '#f57f17', color: '#fff' };
        } else if (applied?.status === 'True') {
          label = 'Surged (Resources Applied but Not Yet Available)';
          chipSx = { backgroundColor: '#f57f17', color: '#fff' };
        } else if (applied?.status === 'False') {
          label = 'Surged (Failed to Apply Resources)';
          chipSx = { backgroundColor: '#c62828', color: '#fff' };
        } else {
          label = 'Surged (Resources are not Applied Yet)';
        }
      }
    }
  }

  return (
    <Paper elevation={2} sx={{ p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Box display="flex" alignItems="center" gap={1}>
        <Chip label={ref_.kind ?? '—'} size="small" />
        <Typography variant="body2" fontWeight={600}>{title}</Typography>
      </Box>
      {label && <Chip label={label} size="small" sx={chipSx} />}
    </Paper>
  );
}

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

interface RebalancingRequestDetailsProps {
  request: ClusterRebalancingRequest;
  onBack: () => void;
}

export function RebalancingRequestDetails({ request, onBack }: RebalancingRequestDetailsProps) {
  const [rollbackDialogOpen, setRollbackDialogOpen] = React.useState(false);
  const [commitDialogOpen, setCommitDialogOpen] = React.useState(false);
  const conditions = request.conditions;

  let color: string;
  let label: string;

  if (request.rollback) {
    const rolledBack = conditions.find(c => c.type === 'RolledBack');
    if (!rolledBack) { color = '#fdd835'; label = 'Rolling back'; }
    else if (rolledBack.status === 'True') { color = '#00c853'; label = 'Rolled back'; }
    else { color = '#f44336'; label = 'Failed to roll back'; }
  } else {
    const completed = conditions.find(c => c.type === 'Completed');
    const started = conditions.find(c => c.type === 'Started');
    const initialized = conditions.find(c => c.type === 'Initialized');

    if (completed?.status === 'True') { color = '#00c853'; label = 'Completed'; }
    else if (completed?.status === 'False') { color = '#f44336'; label = 'Failed to complete'; }
    else if (started?.status === 'True') { color = '#0288d1'; label = 'Started'; }
    else if (started?.status === 'False') { color = '#f44336'; label = 'Failed to start'; }
    else if (initialized?.status === 'True') { color = '#9e9e9e'; label = 'Initialized'; }
    else if (initialized?.status === 'False') { color = '#f44336'; label = 'Failed to initialize'; }
    else { color = '#9e9e9e'; label = 'Pending'; }
  }

  return (
    <>
      <SectionBox>
        <Box display="flex" flexDirection="row" alignItems="center" gap={1} mt={2}>
          <IconButton size="small" onClick={onBack}>
            <Icon icon="mdi:arrow-left" />
          </IconButton>
          <Typography variant="h5">{request.metadata.name}</Typography>
          <Box flex={1} />
          {!request.rollback && (
            <Button variant="contained" sx={{ backgroundColor: '#f44336', color: '#fff', '&:hover': { backgroundColor: '#c62828' } }} onClick={() => setRollbackDialogOpen(true)}>
              Roll Back
            </Button>
          )}
          <Button variant="contained" color="primary" onClick={() => setCommitDialogOpen(true)}>
            Commit
          </Button>
        </Box>
      </SectionBox>
      <SectionBox title="Overview">
        <Box display="flex" flexDirection="row" gap={2}>
          <StatusIndicatorCard color={color} label={label} />
          <Paper
            elevation={3}
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              padding: 3,
              gap: 1,
              backgroundColor: 'background.paper',
            }}
          >
            <Box display="flex" alignItems="center" gap={1.5}>
              <Chip
                icon={<Icon icon="mdi:kubernetes" width={20} height={20} />}
                label={request.from ?? '—'}
                sx={{ fontSize: '1.1rem', height: 36 }}
              />
              <Box display="flex" alignItems="center" gap={0.6}>
                {(() => {
                  const rolledBack = conditions.find(c => c.type === 'RolledBack')?.status === 'True';
                  const completed = !request.rollback &&
                    conditions.find(c => c.type === 'Completed')?.status === 'True';
                  const hasFailed = !request.rollback && ['Initialized', 'Started', 'Completed'].some(
                    type => conditions.find(c => c.type === type)?.status === 'False'
                  );
                  const hasRollbackFailed = request.rollback &&
                    conditions.find(c => c.type === 'RolledBack')?.status === 'False';
                  const isStatic = hasFailed || hasRollbackFailed || rolledBack || completed;
                  const dotColor = (rolledBack || completed) ? '#00c853' : hasFailed ? '#f44336' : request.rollback ? '#fdd835' : '#00c853';
                  const delays = request.rollback
                    ? ['0.4s', '0.2s', '0s']
                    : ['0s', '0.2s', '0.4s'];
                  const dot = (delay: string) => (
                    <Box
                      sx={{
                        width: 10,
                        height: 10,
                        borderRadius: '50%',
                        backgroundColor: dotColor,
                        ...(isStatic ? {} : {
                          animation: 'flowDot 1.2s ease-in-out infinite',
                          animationDelay: delay,
                          '@keyframes flowDot': {
                            '0%, 100%': { opacity: 0.2 },
                            '50%': { opacity: 1 },
                          },
                        }),
                      }}
                    />
                  );
                  return (
                    <>
                      {rolledBack
                        ? <Icon icon="mdi:check" color="#00c853" width={18} height={18} />
                        : hasRollbackFailed
                          ? <Icon icon="mdi:close" color="#fdd835" width={18} height={18} />
                          : dot(delays[0])}
                      {dot(delays[1])}
                      {hasFailed
                        ? <Icon icon="mdi:close" color="#f44336" width={18} height={18} />
                        : completed
                          ? <Icon icon="mdi:check" color="#00c853" width={18} height={18} />
                          : dot(delays[2])}
                    </>
                  );
                })()}
              </Box>
              <Chip
                icon={<Icon icon="mdi:kubernetes" width={20} height={20} />}
                label={request.to ?? '—'}
                sx={{ fontSize: '1.1rem', height: 36 }}
              />
            </Box>
          </Paper>
        </Box>
      </SectionBox>
      <SectionBox>
        <Typography variant="h6" sx={{ mt: 1, mb: 1, fontSize: '1rem', fontWeight: 600 }}>
          Features
        </Typography>
        <Box display="flex" flexDirection="row" gap={2}>
          <Card sx={{ minWidth: 275, maxWidth: 360 }}>
            <CardContent>
              <Typography sx={{ color: 'text.secondary', fontSize: 14 }} gutterBottom>
                Mode
              </Typography>
              <Typography variant="h5" component="div" fontWeight="bold">
                {request.mode === 'DrainFirst' ? 'Drain First' : 'Surge First'}
              </Typography>
              <Typography variant="body2">
                {request.mode === 'DrainFirst'
                  ? 'Remove workloads from the source cluster before placing them on the target.'
                  : 'Place workloads on the target cluster before removing them from the source.'}
              </Typography>
            </CardContent>
          </Card>
          <Card sx={{ minWidth: 275, maxWidth: 360 }}>
            <CardContent>
              <Typography sx={{ color: 'text.secondary', fontSize: 14 }} gutterBottom>
                Max Concurrency
              </Typography>
              <Typography variant="h5" component="div" fontWeight="bold">
                {request.maxConcurrency !== undefined ? request.maxConcurrency : '—'}
              </Typography>
              <Typography variant="body2">
                Maximum number of migrations that can run in parallel at any given time.
              </Typography>
            </CardContent>
          </Card>
          <Card sx={{ minWidth: 275, maxWidth: 360 }}>
            <CardContent>
              <Typography sx={{ color: 'text.secondary', fontSize: 14 }} gutterBottom>
                Failure Threshold
              </Typography>
              <Typography variant="h5" component="div" fontWeight="bold">
                {request.maxFailureCount !== undefined ? request.maxFailureCount : '—'}
              </Typography>
              <Typography variant="body2">
                Maximum number of migration failures tolerated before the rebalancing request is considered failed.
              </Typography>
            </CardContent>
          </Card>
          <Card sx={{ minWidth: 275, maxWidth: 360 }}>
            <CardContent>
              <Typography sx={{ color: 'text.secondary', fontSize: 14 }} gutterBottom>
                Maximum Wait Time
              </Typography>
              <Typography variant="h5" component="div" fontWeight="bold">
                {request.maximumWaitDurationPerMigrationAttemptSeconds !== undefined
                  ? `${request.maximumWaitDurationPerMigrationAttemptSeconds}s`
                  : '—'}
              </Typography>
              <Typography variant="body2">
                Maximum time to wait for each migration attempt before considering it failed.
              </Typography>
            </CardContent>
          </Card>
        </Box>
      </SectionBox>
      <SectionBox title="Migration Progress">
        <Box display="flex" flexDirection="row" gap={2} width="100%">
          <Box flex={1} sx={{ border: '1px solid', borderColor: 'grey.400', borderRadius: 1, p: 2, opacity: 0.8 }}>
            <Box display="flex" alignItems="center" justifyContent="center" gap={0.5} mb={2}>
              <Icon icon="mdi:kubernetes" width={20} height={20} />
              <Typography variant="subtitle1" fontWeight={600}>{request.from ?? '—'}</Typography>
            </Box>
            <Box display="flex" flexDirection="column" gap={1}>
              {request.migrations.map((migration, i) => (
                <MigrationFromItem key={i} ref_={migration.fromClusterBinding} rollback={request.rollback} />
              ))}
            </Box>
          </Box>
          <Box flex={1} sx={{ border: '1px solid', borderColor: 'grey.400', borderRadius: 1, p: 2, opacity: 0.8 }}>
            <Box display="flex" alignItems="center" justifyContent="center" gap={0.5} mb={2}>
              <Icon icon="mdi:kubernetes" width={20} height={20} />
              <Typography variant="subtitle1" fontWeight={600}>{request.to ?? '—'}</Typography>
            </Box>
            <Box display="flex" flexDirection="column" gap={1}>
              {request.migrations.map((migration, i) => (
                <MigrationToItem key={i} ref_={migration.toClusterBinding} rollback={request.rollback} />
              ))}
            </Box>
          </Box>
        </Box>
      </SectionBox>
      <Dialog open={commitDialogOpen} onClose={() => setCommitDialogOpen(false)}>
        <DialogContent>
          <DialogContentText>
            {request.rollback
              ? 'Are you sure that you want to commit all changes? This will delete the rebalancing request.'
              : 'Are you sure that you want to commit all changes? This will delete the rebalancing request; once the request is committed, you can no longer roll the changes back.'}
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCommitDialogOpen(false)}>Cancel</Button>
          <Button
            variant="contained"
            color="primary"
            onClick={() => {
              request.delete();
              setCommitDialogOpen(false);
              onBack();
            }}
          >
            OK
          </Button>
        </DialogActions>
      </Dialog>
      <Dialog open={rollbackDialogOpen} onClose={() => setRollbackDialogOpen(false)}>
        <DialogContent>
          <DialogContentText>
            Are you sure that you would like to roll back the changes? Once the rollback starts, the process cannot be reset.
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setRollbackDialogOpen(false)}>Cancel</Button>
          <Button
            variant="contained"
            sx={{ backgroundColor: '#f44336', color: '#fff', '&:hover': { backgroundColor: '#c62828' } }}
            onClick={() => {
              const updated = request.jsonData;
              updated.spec.rollback = true;
              request.update(updated);
              setRollbackDialogOpen(false);
            }}
          >
            OK
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
