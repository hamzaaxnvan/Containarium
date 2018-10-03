'use client';

import { useState, useEffect } from 'react';
import { Loader2, XCircle, Moon } from 'lucide-react';
import { Modal, ModalBtn, FormField, Input } from '@/src/components/ui/Modal';

interface AutoSleepDialogProps {
  open: boolean;
  onClose: () => void;
  username: string;
  initialEnabled: boolean;
  initialIdleThresholdMinutes: number;
  onSave: (enabled: boolean, idleThresholdMinutes: number) => Promise<void>;
}

export default function AutoSleepDialog({
  open, onClose, username, initialEnabled, initialIdleThresholdMinutes, onSave,
}: AutoSleepDialogProps) {
  const [enabled, setEnabled] = useState(false);
  const [threshold, setThreshold] = useState(15);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open) {
      setEnabled(initialEnabled);
      setThreshold(initialIdleThresholdMinutes > 0 ? initialIdleThresholdMinutes : 15);
      setError(null);
    }
  }, [open, initialEnabled, initialIdleThresholdMinutes]);

  const hasChanges = enabled !== initialEnabled || (enabled && threshold !== initialIdleThresholdMinutes);

  const handleSave = async () => {
    if (enabled && (threshold < 1 || threshold > 1440)) {
      setError('Threshold must be between 1 and 1440 minutes');
