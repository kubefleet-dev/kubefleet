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
  Chip,
  Divider,
  TextField,
  Typography,
} from '@mui/material';
import React from 'react';

interface OptionCard {
  value: string;
  title: string;
  description: string;
  disabled: boolean;
}

const PLACEMENT_TYPE_CARDS: OptionCard[] = [
  {
    value: 'All',
    title: 'All Clusters',
    description:
      'Place resources on every member cluster currently joined to the fleet, with no constraints (demo only).',
    disabled: false,
  },
  {
    value: 'PickAll',
    title: 'PickAll',
    description:
      'Select all clusters that satisfy the specified affinity and topology spread constraints.',
    disabled: true,
  },
  {
    value: 'PickN',
    title: 'PickN',
    description: 'Select exactly N clusters from those matching the placement constraints.',
    disabled: true,
  },
  {
    value: 'PickFixed',
    title: 'PickFixed',
    description: 'Place resources on a fixed set of clusters.',
    disabled: true,
  },
];

const ROLLOUT_STRATEGY_CARDS: OptionCard[] = [
  {
    value: 'RollingUpdate',
    title: 'Rolling Update',
    description: 'Roll out changes automatically.',
    disabled: false,
  },
  {
    value: 'External',
    title: 'Staged Update Run',
    description: 'Roll out changes stage by stage on demand.',
    disabled: false,
  },
];

const APPLY_STRATEGY_CARDS: OptionCard[] = [
  {
    value: 'ClientSideApply',
    title: 'Client-Side Apply',
    description:
      'Use three-way merge to apply resources, similar to how the Kubernetes CLI performs a client-side apply.',
    disabled: false,
  },
  {
    value: 'ServerSideApply',
    title: 'Server-Side Apply',
    description: 'Use server-side apply to apply resources.',
    disabled: false,
  },
];

function OptionCardRow({
  cards,
  selected,
  onSelect,
}: {
  cards: OptionCard[];
  selected: string | null;
  onSelect: (value: string) => void;
}) {
  return (
    <Box display="flex" flexDirection="row" justifyContent="center" gap={2} mt={2}>
      {cards.map(({ value, title, description, disabled }) => {
        const isSelected = value === selected;
        return (
          <Card
            key={value}
            onClick={!disabled ? () => onSelect(value) : undefined}
            sx={{
              minWidth: 160,
              maxWidth: 200,
              flex: '1 1 160px',
              ...(disabled ? { opacity: 0.45, pointerEvents: 'none' } : { cursor: 'pointer' }),
              ...(isSelected ? { outline: '2px solid', outlineColor: 'primary.main' } : {}),
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
        );
      })}
    </Box>
  );
}

export interface CreatePlacementPlacementDetailsProps {
  placementName: string;
  onPlacementNameChange: (value: string) => void;
  selectedPlacementType: string | null;
  onPlacementTypeSelect: (value: string) => void;
  selectedRolloutStrategy: string | null;
  onRolloutStrategySelect: (value: string) => void;
  selectedApplyStrategy: string | null;
  onApplyStrategySelect: (value: string) => void;
}

export function CreatePlacementPlacementDetails({
  placementName,
  onPlacementNameChange,
  selectedPlacementType,
  onPlacementTypeSelect,
  selectedRolloutStrategy,
  onRolloutStrategySelect,
  selectedApplyStrategy,
  onApplyStrategySelect,
}: CreatePlacementPlacementDetailsProps) {
  return (
    <>
      <SectionBox>
        <Typography
          variant="h4"
          align="center"
          sx={{ fontFamily: 'monospace', fontWeight: 400, mt: 6 }}
        >
          How would you like to place your resources?
        </Typography>
      </SectionBox>
      <SectionBox>
        <Box maxWidth={400} mx="auto" mt={6}>
          <Divider>
            <Chip label="Placement Name" />
          </Divider>
        </Box>
        <Box display="flex" justifyContent="center" mt={2}>
          <TextField
            variant="outlined"
            size="small"
            value={placementName}
            onChange={e => onPlacementNameChange(e.target.value)}
            sx={{ minWidth: 320, '& input': { textAlign: 'center' } }}
          />
        </Box>
        <Box maxWidth={400} mx="auto" mt={6}>
          <Divider>
            <Chip label="Placement Type" />
          </Divider>
        </Box>
        <OptionCardRow
          cards={PLACEMENT_TYPE_CARDS}
          selected={selectedPlacementType}
          onSelect={onPlacementTypeSelect}
        />
        <Box maxWidth={400} mx="auto" mt={6}>
          <Divider>
            <Chip label="Rollout Strategy" />
          </Divider>
        </Box>
        <OptionCardRow
          cards={ROLLOUT_STRATEGY_CARDS}
          selected={selectedRolloutStrategy}
          onSelect={onRolloutStrategySelect}
        />
        <Box maxWidth={400} mx="auto" mt={6}>
          <Divider>
            <Chip label="Apply Strategy" />
          </Divider>
        </Box>
        <OptionCardRow
          cards={APPLY_STRATEGY_CARDS}
          selected={selectedApplyStrategy}
          onSelect={onApplyStrategySelect}
        />
      </SectionBox>
    </>
  );
}
