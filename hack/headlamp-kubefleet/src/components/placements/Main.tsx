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
import { Alert, Box, IconButton, Snackbar, SpeedDial, SpeedDialAction, SpeedDialIcon, Typography } from '@mui/material';
import React from 'react';
import { ClusterResourcePlacement } from '../../resources/clusterResourcePlacement';
import { CreatePlacement } from './CreatePlacement';
import { PlacementDetailView } from './Details';
import { PlacementsList } from './List';
import { PlacementsOverview, PlacementStatusMap } from './Overview';

export function PlacementsDetails() {
  const [placements, error] = ClusterResourcePlacement.useList();
  const [statusMap, setStatusMap] = React.useState<PlacementStatusMap>({});
  const [currentView, setCurrentView] = React.useState<'list' | 'detail' | 'create'>('list');
  const [selectedPlacementName, setSelectedPlacementName] = React.useState<string | null>(null);
  const [snackbar, setSnackbar] = React.useState<{
    message: string;
    severity: 'success' | 'error';
  } | null>(null);

  const handleViewDetails = React.useCallback((name: string) => {
    setSelectedPlacementName(name);
    setCurrentView('detail');
  }, []);

  const selectedPlacement = React.useMemo(
    () => placements?.find(p => p.metadata.name === selectedPlacementName) ?? null,
    [placements, selectedPlacementName]
  );

  React.useEffect(() => {
    if (!placements) return;
    const map: PlacementStatusMap = {};
    for (const p of placements) {
      map[p.metadata.name] = {
        isSyncd: p.isSyncd,
        rolloutStatus: p.rolloutStatus,
        healthStatus: p.healthStatus,
        currentStage: p.currentStage,
        applyStrategyType: p.applyStrategyType,
        inProgressRollouts: p.inProgressRollouts,
        completedRollouts: p.completedRollouts,
        stoppingRollouts: p.stoppingRollouts,
        stoppedRollouts: p.stoppedRollouts,
        stuckRollouts: p.stuckRollouts,
        failedRollouts: p.failedRollouts,
        selectedClusters: p.selectedClustersCount,
        upToDateClusters: p.upToDateClusters,
        notAppliedClusters: p.notAppliedClusters,
        notAvailableCheckedClusters: p.notAvailableCheckedClusters,
        unavailableClusters: p.unavailableClusters,
        availableClusters: p.availableClusters,
      };
    }
    setStatusMap(map);
  }, [placements]);

  return (
    <>
      {currentView === 'list' && (
        <>
          <PlacementsOverview statusMap={statusMap} />
          <PlacementsList
            placements={placements}
            statusMap={statusMap}
            error={error}
            onViewDetails={handleViewDetails}
          />
          <SpeedDial
            ariaLabel="Placement actions"
            sx={{ position: 'fixed', bottom: 32, right: 32 }}
            icon={<SpeedDialIcon />}
          >
            <SpeedDialAction
              icon={<Icon icon="mdi:pencil-plus" />}
              tooltipTitle="Create"
              tooltipOpen
              onClick={() => setCurrentView('create')}
            />
          </SpeedDial>
        </>
      )}
      {currentView === 'create' && (
        <CreatePlacement
          onBack={() => setCurrentView('list')}
          onResult={(message, severity) => setSnackbar({ message, severity })}
        />
      )}
      {currentView === 'detail' && selectedPlacement && (
        <PlacementDetailView placement={selectedPlacement} onBack={() => setCurrentView('list')} />
      )}
      {currentView === 'detail' && !selectedPlacement && (
        <SectionBox>
          <Box display="flex" flexDirection="row" alignItems="center" gap={1}>
            <IconButton size="small" onClick={() => setCurrentView('list')}>
              <Icon icon="mdi:arrow-left" />
            </IconButton>
            <Typography variant="body1">Placement not found.</Typography>
          </Box>
        </SectionBox>
      )}
      <Snackbar
        open={Boolean(snackbar)}
        autoHideDuration={4000}
        onClose={() => setSnackbar(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      >
        <Alert severity={snackbar?.severity ?? 'success'} onClose={() => setSnackbar(null)}>
          {snackbar?.message}
        </Alert>
      </Snackbar>
    </>
  );
}
