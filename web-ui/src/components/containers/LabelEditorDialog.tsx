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
      footer={
        <>
          <ModalBtn onClick={onClose} disabled={saving}>Cancel</ModalBtn>
          <ModalBtn variant="primary" onClick={handleSave} disabled={saving || !hasChanges()}>
            {saving && <Loader2 size={13} className="animate-spin" />}
            Save Changes
          </ModalBtn>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {error && (
          <div className="flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-[var(--c-red)]">
            <XCircle size={14} className="mt-0.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        {/* Current labels */}
        <div>
          <p className="mb-2 text-xs font-medium text-[var(--text-secondary)]">Current Labels</p>
          {Object.keys(labels).length === 0 ? (
            <p className="text-xs text-[var(--text-muted)]">No labels set</p>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(labels).map(([k, v]) => (
                <span
                  key={k}
                  className={`flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-[11px] ${currentLabels[k] === undefined ? 'border-blue-500/30 bg-blue-500/10 text-[var(--c-blue)]' : 'border-[var(--border)] bg-[var(--surface-2)] text-[var(--text-secondary)]'}`}
                >
                  {k}={v}
                  <button
                    onClick={() => handleRemove(k)}
                    disabled={removingKey === k}
                    className="ml-0.5 rounded-full p-0.5 hover:bg-red-500/20 hover:text-[var(--c-red)] transition-colors disabled:opacity-50"
                  >
                    {removingKey === k ? <Loader2 size={10} className="animate-spin" /> : <X size={10} />}
                  </button>
                </span>
              ))}
            </div>
          )}
        </div>

        <div className="border-t border-[var(--border-subtle)]" />

        {/* Add new label */}
        <div>
          <p className="mb-2 text-xs font-medium text-[var(--text-secondary)]">Add Label</p>
          <div className="flex gap-2">
            <FormField label="Key">
              <Input
                value={newKey}
                onChange={e => setNewKey(e.target.value)}
                placeholder="team"
                disabled={saving}
                onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); handleAdd(); } }}
              />
            </FormField>
            <FormField label="Value">
              <Input
                value={newValue}
                onChange={e => setNewValue(e.target.value)}
                placeholder="backend"
                disabled={saving}
                onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); handleAdd(); } }}
              />
            </FormField>
            <div className="flex items-end">
              <button
                onClick={handleAdd}
                disabled={saving || !newKey.trim() || !newValue.trim()}
                className="flex items-center gap-1 rounded-md bg-[var(--accent)] px-2.5 py-2 text-xs text-white hover:bg-[var(--accent-hover)] transition-colors disabled:opacity-50"
              >
                <Plus size={13} />
              </button>
            </div>
          </div>
          <p className="mt-1.5 text-[10px] text-[var(--text-muted)]">Common keys: team, env, project, owner</p>
        </div>
      </div>
    </Modal>
  );
}
