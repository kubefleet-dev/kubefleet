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
import { post } from '@kinvolk/headlamp-plugin/lib/ApiProxy';
import {
  Box,
  Button,
  Card,
  CardContent,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  IconButton,
  Menu,
  MenuItem,
  Select,
  SpeedDial,
  SpeedDialAction,
  SpeedDialIcon,
  TextField,
  Typography,
} from '@mui/material';
import React from 'react';
import { ClusterRebalancingRequest } from '../../resources/clusterRebalancingRequest';
import { MemberCluster } from '../../resources/memberCluster';
import { MemberClustersList } from './List';
import { MemberClustersOverview } from './Overview';
import { RebalancingRequestDetails } from './RebalancingRequestDetails';

interface RebalancingRequestActionsMenuProps {
  onViewDetails: () => void;
}

function RebalancingRequestActionsMenu({ onViewDetails }: RebalancingRequestActionsMenuProps) {
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
      </Menu>
    </>
  );
}

export function ClustersDetails() {
  const [clusters, error] = MemberCluster.useList();
  const [rebalancingRequests] = ClusterRebalancingRequest.useList();
  const [selectedRequest, setSelectedRequest] = React.useState<ClusterRebalancingRequest | null>(
    null
  );
  const [rebalanceDialogOpen, setRebalanceDialogOpen] = React.useState(false);
  const [requestName, setRequestName] = React.useState('');
  const [fromCluster, setFromCluster] = React.useState('');
  const [toCluster, setToCluster] = React.useState('');
  const [mode, setMode] = React.useState<'SurgeFirst' | 'DrainFirst' | null>(null);
  const [maxConcurrency, setMaxConcurrency] = React.useState('');
  const [failureThreshold, setFailureThreshold] = React.useState('');
  const [maxWaitTime, setMaxWaitTime] = React.useState('');

  if (selectedRequest) {
    return (
      <RebalancingRequestDetails
        request={selectedRequest}
        onBack={() => setSelectedRequest(null)}
      />
    );
  }

  return (
    <>
      <MemberClustersOverview clusters={clusters} />
      {rebalancingRequests && rebalancingRequests.length > 0 && (
        <SectionBox title="Ongoing Rebalancing Requests">
          <SimpleTable
            columns={[
              {
                label: 'Name',
                gridTemplate: 'min-content',
                getter: item => item.metadata.name,
              },
              {
                label: 'Mode',
                gridTemplate: 'min-content',
                getter: item => (item.mode ? <Chip label={item.mode} size="small" /> : '—'),
              },
              {
                label: 'Max Concurrency',
                gridTemplate: 'min-content',
                getter: item => item.maxConcurrency ?? '—',
              },
              {
                label: 'Placements to Migrate',
                gridTemplate: 'min-content',
                getter: item => item.migrations.length,
              },
              {
                label: 'Status',
                gridTemplate: 'min-content',
                getter: item => {
                  const conditions = item.conditions;
                  if (item.rollback) {
                    const rolledBack = conditions.find(c => c.type === 'RolledBack');
                    if (!rolledBack)
                      return <StatusLabel status="warning">Rolling back</StatusLabel>;
                    if (rolledBack.status === 'True')
                      return (
                        <StatusLabel
                          status=""
                          sx={{ backgroundColor: '#00c853', color: 'grey.900' }}
                        >
                          Rolled back
                        </StatusLabel>
                      );
                    return <StatusLabel status="error">Failed to roll back</StatusLabel>;
                  }
                  const completed = conditions.find(c => c.type === 'Completed');
                  if (completed?.status === 'True')
                    return <StatusLabel status="success">Completed</StatusLabel>;
                  if (completed?.status === 'False')
                    return <StatusLabel status="error">Failed to complete</StatusLabel>;
                  const started = conditions.find(c => c.type === 'Started');
                  if (started?.status === 'True')
                    return (
                      <StatusLabel
                        status=""
                        sx={{ backgroundColor: 'info.main', color: 'info.contrastText' }}
                      >
                        Started
                      </StatusLabel>
                    );
                  if (started?.status === 'False')
                    return <StatusLabel status="error">Failed to start</StatusLabel>;
                  const initialized = conditions.find(c => c.type === 'Initialized');
                  if (initialized?.status === 'True')
                    return (
                      <StatusLabel
                        status=""
                        sx={{ backgroundColor: 'grey.400', color: 'grey.900' }}
                      >
                        Initialized
                      </StatusLabel>
                    );
                  if (initialized?.status === 'False')
                    return <StatusLabel status="error">Failed to initialize</StatusLabel>;
                  return '—';
                },
              },
              {
                label: 'From/To',
                gridTemplate: '1fr',
                getter: item => {
                  const kubeIcon = <Icon icon="mdi:kubernetes" width={16} height={16} />;
                  const conditions = item.conditions;
                  const rolledBack =
                    item.rollback &&
                    conditions.find(c => c.type === 'RolledBack')?.status === 'True';
                  const completed =
                    !item.rollback &&
                    conditions.find(c => c.type === 'Completed')?.status === 'True';
                  const hasFailed =
                    !item.rollback &&
                    ['Initialized', 'Started', 'Completed'].some(
                      type => conditions.find(c => c.type === type)?.status === 'False'
                    );
                  const hasRollbackFailed =
                    item.rollback &&
                    conditions.find(c => c.type === 'RolledBack')?.status === 'False';
                  const isStatic = hasFailed || hasRollbackFailed || rolledBack || completed;
                  const dotColor =
                    rolledBack || completed
                      ? '#00c853'
                      : hasFailed
                      ? '#f44336'
                      : item.rollback
                      ? '#fdd835'
                      : '#00c853';
                  const dotStyle = (delay: string) => ({
                    width: 6,
                    height: 6,
                    borderRadius: '50%',
                    backgroundColor: dotColor,
                    ...(isStatic
                      ? {}
                      : {
                          animation: 'flowDot 1.2s ease-in-out infinite',
                          animationDelay: delay,
                          '@keyframes flowDot': {
                            '0%, 100%': { opacity: 0.2 },
                            '50%': { opacity: 1 },
                          },
                        }),
                  });
                  const dots = item.rollback
                    ? [dotStyle('0.4s'), dotStyle('0.2s'), dotStyle('0s')]
                    : [dotStyle('0s'), dotStyle('0.2s'), dotStyle('0.4s')];
                  return (
                    <Box display="flex" alignItems="center" gap={0.5}>
                      <Chip
                        icon={kubeIcon}
                        label={item.from ?? '—'}
                        size="small"
                        sx={{ fontSize: '0.95rem' }}
                      />
                      <Box display="flex" alignItems="center" gap={0.4}>
                        {rolledBack ? (
                          <Icon icon="mdi:check" color="#00c853" width={14} height={14} />
                        ) : hasRollbackFailed ? (
                          <Icon icon="mdi:close" color="#fdd835" width={14} height={14} />
                        ) : (
                          <Box sx={dots[0]} />
                        )}
                        <Box sx={dots[1]} />
                        {hasFailed ? (
                          <Icon icon="mdi:close" color="#f44336" width={14} height={14} />
                        ) : completed ? (
                          <Icon icon="mdi:check" color="#00c853" width={14} height={14} />
                        ) : (
                          <Box sx={dots[2]} />
                        )}
                      </Box>
                      <Chip
                        icon={kubeIcon}
                        label={item.to ?? '—'}
                        size="small"
                        sx={{ fontSize: '0.95rem' }}
                      />
                    </Box>
                  );
                },
              },
              {
                label: '',
                gridTemplate: 'min-content',
                getter: item => (
                  <RebalancingRequestActionsMenu onViewDetails={() => setSelectedRequest(item)} />
                ),
              },
            ]}
            data={rebalancingRequests}
            sx={{ width: '100%', '& .MuiTableCell-root': { fontSize: '1.1rem' } }}
          />
        </SectionBox>
      )}
      <SectionBox title="Member Clusters">
        <MemberClustersList clusters={clusters} error={error} />
      </SectionBox>
      <SpeedDial
        ariaLabel="Actions"
        sx={{ position: 'fixed', bottom: 32, right: 32 }}
        icon={<SpeedDialIcon />}
      >
        <SpeedDialAction
          icon={<Icon icon="mdi:plus" />}
          tooltipTitle="Rebalance"
          tooltipOpen
          onClick={() => {
            setRequestName('');
            setFromCluster('');
            setToCluster('');
            setMode(null);
            setMaxConcurrency('');
            setFailureThreshold('');
            setMaxWaitTime('');
            setRebalanceDialogOpen(true);
          }}
        />
      </SpeedDial>
      <Dialog
        open={rebalanceDialogOpen}
        onClose={() => setRebalanceDialogOpen(false)}
        maxWidth="sm"
        fullWidth
      >
        <DialogTitle>Rebalance Resources between Clusters</DialogTitle>
        <DialogContent>
          <Box maxWidth={400} mx="auto" mt={2}>
            <Divider>
              <Chip label="Request Name" />
            </Divider>
          </Box>
          <Box display="flex" justifyContent="center" mt={2}>
            <TextField
              variant="outlined"
              size="small"
              value={requestName}
              onChange={e => setRequestName(e.target.value)}
              sx={{ minWidth: 320, '& input': { textAlign: 'center' } }}
            />
          </Box>
          <Box maxWidth={400} mx="auto" mt={4}>
            <Divider>
              <Chip label="Clusters" />
            </Divider>
          </Box>
          <Box display="flex" flexDirection="row" alignItems="center" gap={2} mt={2}>
            <Select
              size="small"
              displayEmpty
              value={fromCluster}
              onChange={e => setFromCluster(e.target.value)}
              sx={{ flex: 1 }}
            >
              <MenuItem value="">
                <em>From cluster</em>
              </MenuItem>
              {(clusters ?? [])
                .filter(c => c.metadata.name !== toCluster)
                .map(c => (
                  <MenuItem key={c.metadata.name} value={c.metadata.name}>
                    {c.metadata.name}
                  </MenuItem>
                ))}
            </Select>
            <Box display="flex" alignItems="center" gap={0.6}>
              {['0s', '0.2s', '0.4s'].map((delay, i) => (
                <Box
                  key={i}
                  sx={{
                    width: 8,
                    height: 8,
                    borderRadius: '50%',
                    backgroundColor: '#00c853',
                    animation: 'flowDot 1.2s ease-in-out infinite',
                    animationDelay: delay,
                    '@keyframes flowDot': {
                      '0%, 100%': { opacity: 0.2 },
                      '50%': { opacity: 1 },
                    },
                  }}
                />
              ))}
            </Box>
            <Select
              size="small"
              displayEmpty
              value={toCluster}
              onChange={e => setToCluster(e.target.value)}
              sx={{ flex: 1 }}
            >
              <MenuItem value="">
                <em>To cluster</em>
              </MenuItem>
              {(clusters ?? [])
                .filter(c => c.metadata.name !== fromCluster)
                .map(c => (
                  <MenuItem key={c.metadata.name} value={c.metadata.name}>
                    {c.metadata.name}
                  </MenuItem>
                ))}
            </Select>
          </Box>
          <Box maxWidth={400} mx="auto" mt={4}>
            <Divider>
              <Chip label="Mode" />
            </Divider>
          </Box>
          <Box display="flex" flexDirection="row" justifyContent="center" gap={2} mt={2}>
            {(
              [
                {
                  value: 'SurgeFirst',
                  title: 'Surge First',
                  description:
                    'Place workloads on the target cluster before removing them from the source.',
                },
                {
                  value: 'DrainFirst',
                  title: 'Drain First',
                  description:
                    'Remove workloads from the source cluster before placing them on the target.',
                },
              ] as { value: 'SurgeFirst' | 'DrainFirst'; title: string; description: string }[]
            ).map(({ value, title, description }) => (
              <Card
                key={value}
                onClick={() => setMode(value)}
                sx={{
                  flex: 1,
                  cursor: 'pointer',
                  ...(mode === value ? { outline: '2px solid white' } : {}),
                }}
              >
                <CardContent>
                  <Typography variant="h6" fontWeight="bold">
                    {title}
                  </Typography>
                  <Typography variant="body2" sx={{ color: 'text.secondary' }}>
                    {description}
                  </Typography>
                </CardContent>
              </Card>
            ))}
          </Box>
          <Box maxWidth={400} mx="auto" mt={4}>
            <Divider><Chip label="Max Concurrency" /></Divider>
          </Box>
          <Box display="flex" alignItems="center" justifyContent="center" gap={2} mt={2}>
            <Typography variant="body2" sx={{ minWidth: 220, textAlign: 'right' }}>Max Concurrency</Typography>
            <TextField
              variant="outlined"
              size="small"
              type="number"
              value={maxConcurrency}
              onChange={e => setMaxConcurrency(e.target.value)}
              inputProps={{ min: 1, max: 5 }}
              sx={{ width: 120 }}
            />
          </Box>
          <Box maxWidth={400} mx="auto" mt={4}>
            <Divider>
              <Chip label="Failure Policy" />
            </Divider>
          </Box>
          <Box display="flex" flexDirection="column" gap={2} mt={2}>
            <Box display="flex" alignItems="center" gap={2}>
              <Typography variant="body2" sx={{ minWidth: 220, textAlign: 'right' }}>
                Failure Threshold
              </Typography>
              <TextField
                variant="outlined"
                size="small"
                type="number"
                value={failureThreshold}
                onChange={e => setFailureThreshold(e.target.value)}
                inputProps={{ min: 1, max: 10 }}
                sx={{ width: 120 }}
              />
            </Box>
            <Box display="flex" alignItems="center" gap={2}>
              <Typography variant="body2" sx={{ minWidth: 220, textAlign: 'right' }}>
                Maximum Wait Time in Seconds
              </Typography>
              <TextField
                variant="outlined"
                size="small"
                type="number"
                value={maxWaitTime}
                onChange={e => setMaxWaitTime(e.target.value)}
                inputProps={{ min: 30, max: 600 }}
                sx={{ width: 120 }}
              />
            </Box>
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setRebalanceDialogOpen(false)}>Cancel</Button>
          <Button
            variant="contained"
            color="primary"
            disabled={
              !requestName.trim() ||
              !fromCluster ||
              !toCluster ||
              !mode ||
              (!!failureThreshold &&
                (Number(failureThreshold) < 1 || Number(failureThreshold) > 10)) ||
              (!!maxWaitTime && (Number(maxWaitTime) < 30 || Number(maxWaitTime) > 600)) ||
              (!!maxConcurrency && (Number(maxConcurrency) < 1 || Number(maxConcurrency) > 5))
            }
            onClick={() => {
              const body: Record<string, unknown> = {
                apiVersion: 'placement.kubernetes-fleet.io/v1beta1',
                kind: 'ClusterRebalancingRequest',
                metadata: { name: requestName.trim() },
                spec: {
                  from: fromCluster,
                  to: toCluster,
                  mode: mode,
                  ...(maxConcurrency ? { maxConcurrency: Number(maxConcurrency) } : {}),
                  ...(failureThreshold || maxWaitTime
                    ? {
                        failurePolicy: {
                          ...(failureThreshold
                            ? { maxFailureCount: Number(failureThreshold) }
                            : {}),
                          ...(maxWaitTime
                            ? { maximumWaitDurationPerMigrationAttemptSeconds: Number(maxWaitTime) }
                            : {}),
                        },
                      }
                    : {}),
                },
              };
              post('/apis/placement.kubernetes-fleet.io/v1beta1/clusterrebalancingrequests', body);
              setRebalanceDialogOpen(false);
            }}
          >
            Create
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
