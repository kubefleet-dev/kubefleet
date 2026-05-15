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
import { Box, Card, CardContent, Typography } from '@mui/material';
import React from 'react';

interface TemplateCard {
  label: string;
  title: React.ReactNode;
  description: string;
  disabled?: boolean;
  value?: string;
}

const CARDS: TemplateCard[] = [
  {
    label: 'Demo Only',
    title: 'A Namespace',
    value: 'namespace-demo-only',
    description:
      'Place a namespace sourced from a member cluster across your fleet, so that you can manage it via the hub cluster.',
  },
  {
    label: 'AI',
    title: 'A LLM Model with Kaito',
    description:
      'Deploy and distribute a large language model across your fleet using Kaito, automating GPU node provisioning and model configuration.',
    disabled: true,
  },
  {
    label: 'AI',
    title: 'A Training Job with Multi-Kueue',
    description:
      'Distribute AI/ML training jobs across multiple clusters using Multi-Kueue, enabling efficient resource sharing and workload scheduling at fleet scale.',
    disabled: true,
  },
  {
    label: 'Application',
    title: (
      <>
        Resources from a{' '}
        <Box component="span" sx={{ fontFamily: 'monospace' }}>
          git
        </Box>{' '}
        Repository
      </>
    ),
    description:
      'Sync and propagate Kubernetes resources directly from a git repository across your fleet, enabling GitOps-driven multi-cluster deployments.',
    disabled: true,
  },
  {
    label: 'Policy',
    title: 'Security Policies with Kyverno',
    description:
      'Propagate Kyverno security policies across your fleet to enforce consistent governance, compliance, and access controls on all member clusters.',
    disabled: true,
  },
  {
    label: 'Multi-Cluster Service',
    title: 'Expose multi-cluster service with Cilium',
    description:
      'Use Cilium to expose and load-balance services across member clusters, enabling seamless multi-cluster connectivity with eBPF-powered networking.',
    disabled: true,
  },
];

export interface CreatePlacementSelectTemplateProps {
  selectedTemplate: string | null;
  onSelect: (value: string) => void;
}

export function CreatePlacementSelectTemplate({
  selectedTemplate,
  onSelect,
}: CreatePlacementSelectTemplateProps) {
  return (
    <>
      <SectionBox>
        <Typography
          variant="h4"
          align="center"
          sx={{ fontFamily: 'monospace', fontWeight: 400, mt: 6 }}
        >
          What would you like to place in your fleet?
        </Typography>
      </SectionBox>
      <SectionBox>
        <Box display="flex" flexDirection="row" flexWrap="wrap" gap={2} mt={4}>
          {CARDS.map(({ label, title, description, disabled, value }) => {
            const isSelected = !!value && value === selectedTemplate;
            return (
              <Card
                key={String(title)}
                onClick={value && !disabled ? () => onSelect(value) : undefined}
                sx={{
                  minWidth: 240,
                  maxWidth: 300,
                  flex: '1 1 240px',
                  ...(disabled ? { opacity: 0.45, pointerEvents: 'none' } : { cursor: 'pointer' }),
                  ...(isSelected ? { outline: '2px solid', outlineColor: 'primary.main' } : {}),
                }}
              >
                <CardContent>
                  <Typography sx={{ color: 'text.secondary', fontSize: 14 }} gutterBottom>
                    {label}
                  </Typography>
                  <Typography variant="h5" component="div" fontWeight="bold">
                    {title}
                  </Typography>
                  <Typography variant="body2">{description}</Typography>
                </CardContent>
              </Card>
            );
          })}
        </Box>
      </SectionBox>
    </>
  );
}
