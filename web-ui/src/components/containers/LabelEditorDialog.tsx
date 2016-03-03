'use client';

import { useState, useEffect } from 'react';
import { Plus, Loader2, X, XCircle } from 'lucide-react';
import { Modal, ModalBtn, FormField, Input } from '@/src/components/ui/Modal';

interface LabelEditorDialogProps {
  open: boolean;
  onClose: () => void;
  containerName: string;
  username: string;
  currentLabels: Record<string, string>;
  onSave: (labels: Record<string, string>) => Promise<void>;
  onRemove: (key: string) => Promise<void>;
}

export default function LabelEditorDialog({ open, onClose, containerName, currentLabels, onSave, onRemove }: LabelEditorDialogProps) {
  const [labels, setLabels] = useState<Record<string, string>>({});
  const [newKey, setNewKey] = useState('');
  const [newValue, setNewValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [removingKey, setRemovingKey] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setLabels({ ...currentLabels });
      setNewKey(''); setNewValue(''); setError(null);
    }
  }, [open, currentLabels]);

  const handleAdd = () => {
    const k = newKey.trim(), v = newValue.trim();
    if (!k) { setError('Key cannot be empty'); return; }
    if (!v) { setError('Value cannot be empty'); return; }
    if (!/^[a-zA-Z0-9._-]+$/.test(k)) { setError('Key can only contain letters, numbers, dots, hyphens, and underscores'); return; }
    setLabels(p => ({ ...p, [k]: v }));
    setNewKey(''); setNewValue(''); setError(null);
  };

  const handleRemove = async (key: string) => {
    if (currentLabels[key] !== undefined) {
      setRemovingKey(key);
      try {
        await onRemove(key);
        setLabels(p => { const u = { ...p }; delete u[key]; return u; });
      } catch (err) {
        setError(`Failed to remove label: ${err}`);
      } finally {
        setRemovingKey(null);
      }
    } else {
      setLabels(p => { const u = { ...p }; delete u[key]; return u; });
    }
  };

  const handleSave = async () => {
    const toSave: Record<string, string> = {};
    for (const [k, v] of Object.entries(labels)) {
      if (currentLabels[k] !== v) toSave[k] = v;
    }
    if (Object.keys(toSave).length === 0) { onClose(); return; }
    setSaving(true); setError(null);
    try {
      await onSave(toSave);
      onClose();
    } catch (err) {
      setError(`Failed to save labels: ${err}`);
    } finally {
      setSaving(false);
    }
  };

  const hasChanges = () => {
    const ck = Object.keys(currentLabels), nk = Object.keys(labels);
    if (ck.length !== nk.length) return true;
    return nk.some(k => currentLabels[k] !== labels[k]);
  };

  return (
    <Modal
      open={open}
      onClose={() => { if (!saving) onClose(); }}
      title={`Edit Labels — ${containerName}`}
