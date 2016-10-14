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
