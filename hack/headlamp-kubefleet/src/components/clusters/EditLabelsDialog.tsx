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
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  TextField,
  Typography,
} from '@mui/material';
import React from 'react';
import { MemberCluster } from '../../resources/memberCluster';

interface LabelEntry {
  key: string;
  value: string;
}

interface EditLabelsDialogProps {
  cluster: MemberCluster;
  open: boolean;
  onClose: () => void;
}

export function EditLabelsDialog({ cluster, open, onClose }: EditLabelsDialogProps) {
  const [labels, setLabels] = React.useState<LabelEntry[]>([]);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (open) {
      const existing = cluster.metadata.labels ?? {};
      setLabels(Object.entries(existing).map(([key, value]) => ({ key, value })));
      setError(null);
    }
  }, [open, cluster]);

  const handleKeyChange = (index: number, key: string) => {
    setLabels(prev => prev.map((l, i) => (i === index ? { ...l, key } : l)));
  };

  const handleValueChange = (index: number, value: string) => {
    setLabels(prev => prev.map((l, i) => (i === index ? { ...l, value } : l)));
  };

  const handleAdd = () => {
    setLabels(prev => [...prev, { key: '', value: '' }]);
  };

  const handleRemove = (index: number) => {
    setLabels(prev => prev.filter((_, i) => i !== index));
  };

  const handleSave = async () => {
    setError(null);
    for (const { key } of labels) {
      if (!key.trim()) {
        setError('All label keys must be non-empty.');
        return;
      }
    }
    const labelMap = Object.fromEntries(labels.map(({ key, value }) => [key.trim(), value]));
    setSaving(true);
    try {
      await cluster.update({
        ...cluster.jsonData,
        metadata: {
          ...cluster.jsonData.metadata,
          labels: labelMap,
        },
      });
      onClose();
    } catch (e: any) {
      setError(e?.message ?? 'Failed to save labels.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>Edit Labels — {cluster.metadata.name}</DialogTitle>
      <DialogContent>
        <Box display="flex" flexDirection="column" gap={1.5} mt={1}>
          {labels.map((label, i) => (
            <Box key={i} display="flex" flexDirection="row" gap={1} alignItems="center">
              <TextField
                label="Key"
                size="small"
                value={label.key}
                onChange={e => handleKeyChange(i, e.target.value)}
                sx={{ flex: 1 }}
              />
              <TextField
                label="Value"
                size="small"
                value={label.value}
                onChange={e => handleValueChange(i, e.target.value)}
                sx={{ flex: 1 }}
              />
              <IconButton size="small" onClick={() => handleRemove(i)}>
                <Icon icon="mdi:close" />
              </IconButton>
            </Box>
          ))}
          <Button
            startIcon={<Icon icon="mdi:plus" />}
            onClick={handleAdd}
            variant="outlined"
            size="small"
            sx={{ alignSelf: 'flex-start' }}
          >
            Add Label
          </Button>
          {error && (
            <Typography variant="body2" color="error">
              {error}
            </Typography>
          )}
        </Box>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={saving}>Cancel</Button>
        <Button onClick={handleSave} variant="contained" disabled={saving}>
          {saving ? 'Saving…' : 'Save'}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
