'use client';

import { useState } from 'react';
import { RefreshCw, Loader2, Trash2 } from 'lucide-react';
import { App } from '@/src/types/app';
import AppCard from './AppCard';
import { Modal, ModalBtn, FormField, Input } from '@/src/components/ui/Modal';

interface AppsViewProps {
  apps: App[];
  isLoading: boolean;
  error?: Error | null;
  onStopApp: (username: string, appName: string) => Promise<void>;
  onStartApp: (username: string, appName: string) => Promise<void>;
  onRestartApp: (username: string, appName: string) => Promise<void>;
  onDeleteApp: (username: string, appName: string) => Promise<void>;
  onViewLogs?: (username: string, appName: string) => void;
  onRefresh: () => void;
}

export default function AppsView({ apps, isLoading, error, onStopApp, onStartApp, onRestartApp, onDeleteApp, onViewLogs, onRefresh }: AppsViewProps) {
  const [deleteDialog, setDeleteDialog] = useState<{ open: boolean; username: string; appName: string }>({ open: false, username: '', appName: '' });
  const [confirmText, setConfirmText] = useState('');
  const [deleting, setDeleting] = useState(false);

  const handleDeleteConfirm = async () => {
    if (confirmText !== deleteDialog.appName) return;
    setDeleting(true);
    try {
      await onDeleteApp(deleteDialog.username, deleteDialog.appName);
      setDeleteDialog({ open: false, username: '', appName: '' });
    } finally {
      setDeleting(false);
    }
  };

  if (isLoading && apps.length === 0) {
    return (
      <div className="flex min-h-[300px] items-center justify-center">
        <Loader2 size={24} className="animate-spin text-[var(--text-secondary)]" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center gap-2 py-16">
        <p className="text-sm font-medium text-[var(--c-red)]">Failed to load apps</p>
        <p className="text-xs text-[var(--text-muted)]">{error.message}</p>
        <button onClick={onRefresh} className="mt-2 rounded-md border border-[var(--border)] px-3 py-1.5 text-xs text-[var(--text-secondary)] hover:bg-[var(--surface-2)] transition-colors">
          Retry
        </button>
      </div>
    );
  }

  return (
    <div className="p-6">
      {/* Toolbar */}
      <div className="mb-6 flex items-center gap-3">
        <h1 className="text-base font-semibold text-[var(--text)] mr-auto">
          Applications
          <span className="ml-2 text-sm font-normal text-[var(--text-muted)]">{apps.length}</span>
        </h1>
        <button
          onClick={onRefresh}
          disabled={isLoading}
          className="flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-2)] disabled:opacity-50"
        >
          <RefreshCw size={12} className={isLoading ? 'animate-spin' : ''} />
          Refresh
        </button>
      </div>

      {apps.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-3 py-16">
          <p className="text-sm text-[var(--text-secondary)]">No applications found</p>
          <p className="text-xs text-[var(--text-muted)]">Deploy your first app using the CLI:</p>
          <pre className="rounded-lg border border-[var(--border-subtle)] bg-[var(--surface)] px-4 py-2 text-xs text-[var(--text-secondary)]">
            containarium app deploy myapp --source .
          </pre>
        </div>
      ) : (
        <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(350px, 1fr))' }}>
          {apps.map(app => (
            <AppCard
              key={app.id}
              app={app}
              onStop={onStopApp}
              onStart={onStartApp}
              onRestart={onRestartApp}
              onDelete={(u, n) => { setDeleteDialog({ open: true, username: u, appName: n }); setConfirmText(''); }}
              onViewLogs={onViewLogs}
            />
          ))}
        </div>
      )}

      {/* Delete confirmation */}
      <Modal
        open={deleteDialog.open}
        onClose={() => setDeleteDialog({ open: false, username: '', appName: '' })}
        title="Delete Application"
        size="sm"
        footer={
          <>
            <ModalBtn onClick={() => setDeleteDialog({ open: false, username: '', appName: '' })} disabled={deleting}>Cancel</ModalBtn>
            <ModalBtn variant="danger" onClick={handleDeleteConfirm} disabled={confirmText !== deleteDialog.appName || deleting}>
              {deleting ? <Loader2 size={13} className="animate-spin" /> : <Trash2 size={13} />}
              Delete
            </ModalBtn>
          </>
        }
      >
        <p className="mb-1 text-sm text-[var(--text)]">
          Are you sure you want to delete <strong>{deleteDialog.appName}</strong>?
        </p>
        <p className="mb-4 text-xs text-[var(--text-muted)]">This action cannot be undone. Type the app name to confirm:</p>
        <FormField label={`Type "${deleteDialog.appName}" to confirm`}>
          <Input value={confirmText} onChange={e => setConfirmText(e.target.value)} placeholder={deleteDialog.appName} />
        </FormField>
      </Modal>
    </div>
  );
}
