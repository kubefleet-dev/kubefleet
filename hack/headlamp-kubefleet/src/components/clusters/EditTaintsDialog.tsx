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
  FormControl,
  IconButton,
  InputLabel,
  MenuItem,
  Select,
  TextField,
  Typography,
} from '@mui/material';
import React from 'react';
import { MemberCluster, MemberClusterTaint } from '../../resources/memberCluster';

const TAINT_EFFECTS = ['NoSchedule'];

interface EditTaintsDialogProps {
  cluster: MemberCluster;
  open: boolean;
  onClose: () => void;
}

export function EditTaintsDialog({ cluster, open, onClose }: EditTaintsDialogProps) {
  const [taints, setTaints] = React.useState<MemberClusterTaint[]>([]);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (open) {
      setTaints(cluster.taints.map(t => ({ ...t })));
      setError(null);
    }
  }, [open, cluster]);

  const handleChange = (index: number, field: keyof MemberClusterTaint, val: string) => {
    setTaints(prev => prev.map((t, i) => (i === index ? { ...t, [field]: val } : t)));
  };

  const handleAdd = () => {
    setTaints(prev => [...prev, { key: '', value: '', effect: 'NoSchedule' }]);
  };

  const handleRemove = (index: number) => {
    setTaints(prev => prev.filter((_, i) => i !== index));
  };

  const handleSave = async () => {
    setError(null);
    for (const { key, effect } of taints) {
      if (!key.trim()) {
        setError('All taint keys must be non-empty.');
        return;
      }
      if (!effect) {
        setError('All taints must have an effect.');
        return;
      }
    }
    setSaving(true);
    try {
      await cluster.update({
        ...cluster.jsonData,
        spec: {
          ...cluster.jsonData.spec,
          taints: taints.map(t => ({
            key: t.key.trim(),
            ...(t.value ? { value: t.value } : {}),
            effect: t.effect,
          })),
        },
      });
      onClose();
    } catch (e: any) {
      setError(e?.message ?? 'Failed to save taints.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="md">
      <DialogTitle>Edit Taints — {cluster.metadata.name}</DialogTitle>
      <DialogContent>
        <Box display="flex" flexDirection="column" gap={1.5} mt={1}>
          {taints.map((taint, i) => (
            <Box key={i} display="flex" flexDirection="row" gap={1} alignItems="center">
              <TextField
                label="Key"
                size="small"
                value={taint.key}
                onChange={e => handleChange(i, 'key', e.target.value)}
                sx={{ flex: 2 }}
              />
              <TextField
                label="Value"
                size="small"
                value={taint.value ?? ''}
                onChange={e => handleChange(i, 'value', e.target.value)}
                sx={{ flex: 2 }}
              />
              <FormControl size="small" sx={{ flex: 2 }}>
                <InputLabel>Effect</InputLabel>
                <Select
                  label="Effect"
                  value={taint.effect}
                  onChange={e => handleChange(i, 'effect', e.target.value)}
                >
                  {TAINT_EFFECTS.map(effect => (
                    <MenuItem key={effect} value={effect}>{effect}</MenuItem>
                  ))}
                </Select>
              </FormControl>
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
            Add Taint
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
