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
import { post, request } from '@kinvolk/headlamp-plugin/lib/ApiProxy';
import { SectionBox } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { useClustersConf } from '@kinvolk/headlamp-plugin/lib/k8s';
import { Box, Button, CircularProgress, IconButton, Step, StepLabel, Stepper } from '@mui/material';
import React from 'react';
import { CreatePlacementPickResources } from './CreatePlacementPickResources';
import { CreatePlacementPlacementDetails } from './CreatePlacementPlacementDetails';
import { CreatePlacementReviewCreate, TEMPLATE } from './CreatePlacementReviewCreate';
import { CreatePlacementSelectTemplate } from './CreatePlacementSelectTemplate';

const STEPS = [
  'Select a Template',
  'Pick Resources',
  'Specify Placement Details',
  'Review and Create',
];

const STEP_INDEX: Record<string, number> = {
  'select-template': 0,
  'pick-resources': 1,
  'placement-details': 2,
  'review-create': 3,
};

export interface CreatePlacementProps {
  onBack: () => void;
  onResult: (message: string, severity: 'success' | 'error') => void;
}

export function CreatePlacement({ onBack, onResult }: CreatePlacementProps) {
  const [selectedTemplate, setSelectedTemplate] = React.useState<string | null>(null);
  const [step, setStep] = React.useState<string>('select-template');
  const [selectedCluster, setSelectedCluster] = React.useState<string>('');
  const [selectedResourceName, setSelectedResourceName] = React.useState<string>('');
  const [namespaceNames, setNamespaceNames] = React.useState<string[]>([]);
  const [namespaceData, setNamespaceData] = React.useState<object | null>(null);
  const [namespaceLoading, setNamespaceLoading] = React.useState(false);
  const [selectedPlacementType, setSelectedPlacementType] = React.useState<string | null>(null);
  const [selectedRolloutStrategy, setSelectedRolloutStrategy] = React.useState<string | null>(null);
  const [selectedApplyStrategy, setSelectedApplyStrategy] = React.useState<string | null>(null);
  const [placementName, setPlacementName] = React.useState<string>('');
  const [creating, setCreating] = React.useState(false);

  const clustersConf = useClustersConf();
  const clusterNames = clustersConf ? Object.keys(clustersConf).sort() : [];

  const yaml = React.useMemo(
    () =>
      TEMPLATE.replace(/CRP_NAME/g, placementName)
        .replace(/NS_NAME/g, selectedResourceName)
        .replace(/ROLLOUT_STRATEGY/g, selectedRolloutStrategy ?? '')
        .replace(/APPLY_STRATEGY/g, selectedApplyStrategy ?? ''),
    [placementName, selectedResourceName, selectedRolloutStrategy, selectedApplyStrategy]
  );

  const namespaceObject = React.useMemo(
    () => ({
      apiVersion: 'v1',
      kind: 'Namespace',
      metadata: {
        name: selectedResourceName,
      },
    }),
    [selectedResourceName]
  );

  const crpObject = React.useMemo(
    () => ({
      apiVersion: 'placement.kubernetes-fleet.io/v1beta1',
      kind: 'ClusterResourcePlacement',
      metadata: {
        name: placementName,
      },
      spec: {
        policy: {
          placementType: 'PickAll',
        },
        resourceSelectors: [
          {
            group: '',
            kind: 'Namespace',
            version: 'v1',
            name: selectedResourceName,
          },
        ],
        strategy: {
          type: selectedRolloutStrategy,
          applyStrategy: {
            type: selectedApplyStrategy,
          },
        },
      },
    }),
    [placementName, selectedResourceName, selectedRolloutStrategy, selectedApplyStrategy]
  );

  React.useEffect(() => {
    setSelectedResourceName('');
    setNamespaceNames([]);
    if (!selectedCluster) return;
    let cancelled = false;
    request('/api/v1/namespaces', { cluster: selectedCluster })
      .then((data: any) => {
        if (cancelled) return;
        const names: string[] = (data?.items ?? [])
          .map((item: any) => item?.metadata?.name as string)
          .filter((n: string) => Boolean(n) && !n.startsWith('kube-') && !n.startsWith('fleet-'))
          .sort();
        setNamespaceNames(names);
      })
      .catch(() => {
        if (!cancelled) setNamespaceNames([]);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedCluster]);

  React.useEffect(() => {
    setNamespaceData(null);
    if (!selectedCluster || !selectedResourceName) return;
    let cancelled = false;
    setNamespaceLoading(true);
    request(`/api/v1/namespaces/${selectedResourceName}`, { cluster: selectedCluster })
      .then((data: any) => {
        if (!cancelled) setNamespaceData(data);
      })
      .catch(() => {
        if (!cancelled) setNamespaceData(null);
      })
      .finally(() => {
        if (!cancelled) setNamespaceLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedCluster, selectedResourceName]);

  const handleCreate = React.useCallback(async () => {
    setCreating(true);
    try {
      await Promise.all([
        post('/api/v1/namespaces', namespaceObject),
        post(
          '/apis/placement.kubernetes-fleet.io/v1beta1/clusterresourceplacements',
          crpObject
        ),
      ]);
      onResult('Creation Succeeded', 'success');
    } catch (e: any) {
      onResult(`Failed to Create: ${e?.message ?? 'Unknown error'}`, 'error');
    } finally {
      setCreating(false);
      onBack();
    }
  }, [namespaceObject, crpObject, onResult, onBack]);

  return (
    <>
      <SectionBox>
        <Box display="flex" flexDirection="row" alignItems="center" mt={2}>
          <IconButton size="small" onClick={onBack}>
            <Icon icon="mdi:arrow-left" />
          </IconButton>
          <Box flexGrow={1} />
          <Button
            variant="contained"
            color="primary"
            disabled={
              creating ||
              (step === 'select-template' && !selectedTemplate) ||
              (step === 'pick-resources' && !selectedResourceName) ||
              (step === 'placement-details' &&
                (!placementName.trim() ||
                  !selectedPlacementType ||
                  !selectedRolloutStrategy ||
                  !selectedApplyStrategy)) ||
              (step === 'review-create' && !yaml.trim())
            }
            onClick={() => {
              if (step === 'select-template') setStep('pick-resources');
              else if (step === 'pick-resources') setStep('placement-details');
              else if (step === 'placement-details') setStep('review-create');
              else if (step === 'review-create') handleCreate();
            }}
          >
            {step === 'review-create' && creating ? (
              <CircularProgress size={20} color="inherit" />
            ) : step === 'review-create' ? (
              'Create'
            ) : (
              'Next'
            )}
          </Button>
        </Box>
      </SectionBox>
      <SectionBox>
        <Stepper
          activeStep={STEP_INDEX[step] ?? 0}
          sx={{
            mt: 4,
            maxWidth: 800,
            mx: 'auto',
            '& .MuiStepIcon-root.Mui-active': { color: '#43a047' },
            '& .MuiStepIcon-root.Mui-completed': { color: '#0288d1' },
          }}
        >
          {STEPS.map(label => (
            <Step key={label}>
              <StepLabel>{label}</StepLabel>
            </Step>
          ))}
        </Stepper>
      </SectionBox>

      {step === 'select-template' && (
        <CreatePlacementSelectTemplate
          selectedTemplate={selectedTemplate}
          onSelect={setSelectedTemplate}
        />
      )}

      {step === 'pick-resources' && (
        <CreatePlacementPickResources
          clusterNames={clusterNames}
          selectedCluster={selectedCluster}
          onClusterChange={setSelectedCluster}
          selectedResourceName={selectedResourceName}
          onResourceNameChange={setSelectedResourceName}
          namespaceNames={namespaceNames}
          namespaceData={namespaceData}
          namespaceLoading={namespaceLoading}
        />
      )}

      {step === 'placement-details' && (
        <CreatePlacementPlacementDetails
          placementName={placementName}
          onPlacementNameChange={setPlacementName}
          selectedPlacementType={selectedPlacementType}
          onPlacementTypeSelect={setSelectedPlacementType}
          selectedRolloutStrategy={selectedRolloutStrategy}
          onRolloutStrategySelect={setSelectedRolloutStrategy}
          selectedApplyStrategy={selectedApplyStrategy}
          onApplyStrategySelect={setSelectedApplyStrategy}
        />
      )}

      {step === 'review-create' && (
        <CreatePlacementReviewCreate yaml={yaml} />
      )}
    </>
  );
}
