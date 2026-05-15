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
import { MemberCluster } from '../../resources/memberCluster';

interface MemberClustersOverviewProps {
  clusters: MemberCluster[] | null;
}

export function MemberClustersOverview({ clusters }: MemberClustersOverviewProps) {
  const theme = useTheme();

  const total = clusters?.length ?? 0;

  const hasCondition = (cluster: MemberCluster, type: string, status: 'True' | 'False') =>
    cluster.conditions.some(c => c.type === type && c.status === status);

  const memberAgentHealthy = (cluster: MemberCluster) => {
    const memberAgent = cluster.agentStatus.find(a => a.type === 'MemberAgent');
    return memberAgent?.conditions?.find(c => c.type === 'Healthy')?.status;
  };

  const joined = clusters?.filter(c => hasCondition(c, 'Joined', 'True')).length ?? 0;
  const notJoined = clusters?.filter(c => hasCondition(c, 'Joined', 'False')).length ?? 0;

  const healthy = clusters?.filter(c => memberAgentHealthy(c) === 'True').length ?? 0;
  const unhealthy = clusters?.filter(c => memberAgentHealthy(c) === 'False').length ?? 0;

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
          <Typography variant="subtitle1">Total Member Clusters</Typography>
        </Paper>
        <Box width={280} display="flex" alignItems="center" justifyContent="center">
          <TileChart
            title="Joined"
            data={[
              { name: 'Joined', value: joined, fill: '#00c853' },
              { name: 'Not Joined', value: notJoined, fill: theme.palette.error.main },
            ]}
            total={joined + notJoined}
            label={joined + notJoined > 0 ? `${Math.round((joined / (joined + notJoined)) * 100)}%` : '—'}
            legend={`${joined} out of ${joined + notJoined} joined to fleet`}
          />
        </Box>
        <Box width={280} display="flex" alignItems="center" justifyContent="center">
          <TileChart
            title="Health"
            data={[
              { name: 'Healthy', value: healthy, fill: '#00c853' },
              { name: 'Unhealthy', value: unhealthy, fill: theme.palette.error.main },
            ]}
            total={healthy + unhealthy}
            label={healthy + unhealthy > 0 ? `${Math.round((healthy / (healthy + unhealthy)) * 100)}%` : '—'}
            legend={`${healthy} healthy, ${unhealthy} unhealthy`}
          />
        </Box>
      </Box>
    </SectionBox>
  );
}
