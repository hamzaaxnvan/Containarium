'use client';

import { useState } from 'react';
import { Loader2, Trash2 } from 'lucide-react';
import { Modal, ModalBtn } from '@/src/components/ui/Modal';

interface DeleteConfirmDialogProps {
  open: boolean;
  containerName: string;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}

export default function DeleteConfirmDialog({ open, containerName, onClose, onConfirm }: DeleteConfirmDialogProps) {
  const [deleting, setDeleting] = useState(false);

  const handleConfirm = async () => {
    setDeleting(true);
    try {
      await onConfirm();
      onClose();
    } finally {
      setDeleting(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Delete Container"
      size="sm"
      footer={
        <>
          <ModalBtn onClick={onClose} disabled={deleting}>Cancel</ModalBtn>
          <ModalBtn variant="danger" onClick={handleConfirm} disabled={deleting}>
            {deleting ? <Loader2 size={13} className="animate-spin" /> : <Trash2 size={13} />}
            Delete
          </ModalBtn>
        </>
      }
    >
      <p className="text-sm text-[var(--text)]">
        Are you sure you want to delete container <strong className="text-[var(--text)]">{containerName}</strong>?
      </p>
      <p className="mt-2 text-xs text-[var(--text-muted)]">
        This action cannot be undone. All data in the container will be lost.
      </p>
    </Modal>
  );
}
