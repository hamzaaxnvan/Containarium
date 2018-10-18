'use client';

import { useState } from 'react';
import { Loader2, UserPlus, Trash2, XCircle, CheckCircle } from 'lucide-react';
import { Modal, ModalBtn, FormField, Input, Textarea } from '@/src/components/ui/Modal';
import { Collaborator, AddCollaboratorRequest } from '@/src/types/container';

interface CollaboratorsDialogProps {
  open: boolean;
  onClose: () => void;
  ownerUsername: string;
  collaborators: Collaborator[];
  isLoading: boolean;
  onAdd: (req: AddCollaboratorRequest) => Promise<{ sshCommand: string }>;
  onRemove: (collaboratorUsername: string) => Promise<void>;
}

function formatDate(ts: number): string {
  if (!ts) return '—';
  return new Date(ts * 1000).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

// Key types the backend's ValidateSSHPublicKey
// (pkg/core/container/ssh_validate.go) accepts via
// golang.org/x/crypto/ssh.ParseAuthorizedKey, enumerated exactly rather
// than matched by prefix — a bare "ssh-" or a typo'd "ecdsa-whatever"
// previously passed the old startsWith check with no key body at all
// (caught in review of #1914).
const SSH_KEY_TYPES = new Set([
  'ssh-rsa',
  'ssh-dss',
  'ssh-ed25519',
  'ecdsa-sha2-nistp256',
  'ecdsa-sha2-nistp384',
  'ecdsa-sha2-nistp521',
  'sk-ssh-ed25519@openssh.com',
  'sk-ecdsa-sha2-nistp256@openssh.com',
  'ssh-rsa-cert-v01@openssh.com',
  'ssh-dss-cert-v01@openssh.com',
  'ssh-ed25519-cert-v01@openssh.com',
  'ecdsa-sha2-nistp256-cert-v01@openssh.com',
  'ecdsa-sha2-nistp384-cert-v01@openssh.com',
  'ecdsa-sha2-nistp521-cert-v01@openssh.com',
  'sk-ssh-ed25519-cert-v01@openssh.com',
  'sk-ecdsa-sha2-nistp256-cert-v01@openssh.com',
]);

// A floor on the base64 body's length, not a real payload check (that's
// ssh.ParseAuthorizedKey's job, server-side) — just enough to catch an
// obviously truncated or missing body. Even a 256-bit ed25519 key's
// body is already ~68 base64 characters.
const MIN_KEY_BODY_LENGTH = 20;
const BASE64_RE = /^[A-Za-z0-9+/]+=*$/;

function isValidSSHPublicKey(key: string): boolean {
  const [type, body] = key.trim().split(/\s+/, 2);
  if (!type || !SSH_KEY_TYPES.has(type)) return false;
  if (!body || body.length < MIN_KEY_BODY_LENGTH) return false;
  return BASE64_RE.test(body);
}
