'use client';

import { useState, useEffect, useCallback } from 'react';
import { X, RefreshCw, Loader2, CheckCircle2 } from 'lucide-react';
import { Server } from '@/src/types/server';
import { PentestFinding, PentestScanRun } from '@/src/types/security';
import { getClient } from '@/src/lib/api/client';

interface Props {
  open: boolean;
  onClose: () => void;
  containerName: string;
  username: string;
  server: Server;
}

type FixState = { status: 'idle' } | { status: 'loading' } | { status: 'done'; oldVersion: string; newVersion: string; packageName: string } | { status: 'error'; message: string };

const SEVERITY_ORDER = ['critical', 'high', 'medium', 'low', 'info'];

function severityColor(s: string) {
  if (s === 'critical' || s === 'high') return 'text-red-400';
  if (s === 'medium') return 'text-amber-400';
  return 'text-zinc-400';
}

function severityBg(s: string) {
  if (s === 'critical' || s === 'high') return 'border-red-500/20 bg-red-500/5';
  if (s === 'medium') return 'border-amber-500/20 bg-amber-500/5';
  return 'border-zinc-700 bg-zinc-800/40';
}

function relativeTime(iso: string): string {
  if (!iso) return 'never';
  const diff = Date.now() - new Date(iso).getTime();
  const m = Math.floor(diff / 60000);
  if (m < 2) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export default function ContainerSecurityDialog({ open, onClose, containerName, username, server }: Props) {
  const [findings, setFindings] = useState<PentestFinding[]>([]);
  const [lastRun, setLastRun] = useState<PentestScanRun | null>(null);
  const [loadingFindings, setLoadingFindings] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [fixStates, setFixStates] = useState<Record<number, FixState>>({});
  const [fixingAll, setFixingAll] = useState(false);

  const client = getClient(server);

  const fetchFindings = useCallback(async () => {
