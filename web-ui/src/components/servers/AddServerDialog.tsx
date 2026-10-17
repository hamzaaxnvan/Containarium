'use client';

import { useState, useEffect } from 'react';
import { Loader2, CheckCircle, XCircle, Plug } from 'lucide-react';
import { Modal, ModalBtn, FormField, Input, Textarea } from '@/src/components/ui/Modal';
import { getClient } from '@/src/lib/api/client';
import { Server } from '@/src/types/server';

interface AddServerDialogProps {
  open: boolean;
  onClose: () => void;
  onAdd: (name: string, endpoint: string, token: string) => Promise<void>;
  onUpdate?: (serverId: string, name: string, endpoint: string, token: string) => Promise<void>;
  editServer?: Server | null;
}

export default function AddServerDialog({ open, onClose, onAdd, onUpdate, editServer }: AddServerDialogProps) {
  const [name, setName] = useState('');
  const [endpoint, setEndpoint] = useState('');
  const [token, setToken] = useState('');
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<'success' | 'error' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const isEditMode = !!editServer;

  useEffect(() => {
    if (editServer) {
      setName(editServer.name);
      setEndpoint(editServer.endpoint);
      setToken(editServer.token);
    }
  }, [editServer]);

  const resetForm = () => {
    setName(''); setEndpoint(''); setToken('');
    setTesting(false); setTestResult(null); setError(null); setSubmitting(false);
  };

  const handleClose = () => { resetForm(); onClose(); };

  const handleTest = async () => {
    if (!endpoint || !token) { setError('Please enter endpoint and token'); return; }
    setTesting(true); setTestResult(null); setError(null);
    try {
      const client = getClient({ id: 'test', name: 'test', endpoint, token, addedAt: Date.now() });
      const ok = await client.testConnection();
      setTestResult(ok ? 'success' : 'error');
      if (!ok) setError('Failed to connect to server');
    } catch (err) {
      setTestResult('error');
      setError('Connection failed: ' + String(err));
    } finally {
      setTesting(false);
    }
  };

  const handleSubmit = async () => {
    if (!endpoint || !token) { setError('Please enter endpoint and token'); return; }
    setSubmitting(true); setError(null);
    try {
      const serverName = name || new URL(endpoint.startsWith('http') ? endpoint : 'http://' + endpoint).hostname;
      if (isEditMode && onUpdate && editServer) {
        await onUpdate(editServer.id, serverName, endpoint, token);
      } else {
        await onAdd(serverName, endpoint, token);
      }
      handleClose();
    } catch (err) {
      setError(`Failed to ${isEditMode ? 'update' : 'add'} server: ` + String(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={isEditMode ? 'Edit Server' : 'Add Server'}
      footer={
        <>
          <ModalBtn onClick={handleClose}>Cancel</ModalBtn>
          <ModalBtn variant="primary" onClick={handleSubmit} disabled={submitting || !endpoint || !token}>
            {submitting && <Loader2 size={13} className="animate-spin" />}
            {isEditMode ? 'Save Changes' : 'Add Server'}
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
        {testResult === 'success' && (
          <div className="flex items-center gap-2 rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-xs text-[var(--c-emerald)]">
            <CheckCircle size={14} />
            Connection successful!
          </div>
        )}

        <FormField label="Server Name (optional)">
          <Input value={name} onChange={e => setName(e.target.value)} placeholder="My Server" />
        </FormField>

        <FormField label="Server URL *" hint="Full API URL including /v1 path (e.g., http://localhost:8080/v1)">
          <Input value={endpoint} onChange={e => setEndpoint(e.target.value)} placeholder="http://192.168.1.10:8080/v1" />
        </FormField>

        <FormField label="JWT Token *">
          <Textarea value={token} onChange={e => setToken(e.target.value)} placeholder="eyJhbGciOiJIUzI1NiIs..." rows={3} />
        </FormField>

        <button
          onClick={handleTest}
          disabled={testing || !endpoint || !token}
          className="flex items-center justify-center gap-1.5 rounded-md border border-[var(--border)] py-2 text-xs text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-2)] disabled:opacity-50"
        >
          {testing ? <Loader2 size={13} className="animate-spin" /> : <Plug size={13} />}
          Test Connection
        </button>
      </div>
    </Modal>
  );
}
