'use client';

import { useState, useEffect, useCallback } from 'react';
import { RefreshCw, Loader2, CheckCircle2, AlertTriangle, ArrowUpCircle } from 'lucide-react';
import { Server } from '@/src/types/server';
import { BackendInfo } from '@/src/types/container';
import { getClient } from '@/src/lib/api/client';

interface VersionsViewProps { server: Server; }

interface LatestRelease { latestRelease: string; currentVersion: string; updateAvailable: boolean; }

// normalizeVersion strips a leading "v" so "v0.22.0" and "0.22.0" compare equal.
function normalizeVersion(v: string): string {
  return (v || '').replace(/^v/, '').trim();
}

export default function VersionsView({ server }: VersionsViewProps) {
  const [backends, setBackends] = useState<BackendInfo[]>([]);
  const [latest, setLatest] = useState<LatestRelease | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [upgrading, setUpgrading] = useState<Record<string, boolean>>({});
  const [notice, setNotice] = useState<string>('');

  const load = useCallback(async () => {
    try {
      const client = getClient(server);
      const [bs, lr] = await Promise.all([
        client.listBackends(),
        client.getLatestRelease().catch(() => null),
      ]);
      setBackends(bs);
      setLatest(lr);
      setError('');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load versions');
    } finally {
      setLoading(false);
    }
  }, [server]);

  useEffect(() => {
    setLoading(true);
    load();
  }, [load]);

  const latestTag = latest?.latestRelease || '';

  // Trigger an upgrade, then poll list_backends until this backend's version
  // changes and it's healthy again (the daemon restarts mid-upgrade). #354.
  const handleUpgrade = useCallback(async (backendId: string, fromVersion: string) => {
    const key = backendId || '(local)';
    setUpgrading((u) => ({ ...u, [key]: true }));
    setNotice('');
    try {
      const client = getClient(server);
      const res = await client.triggerBackendUpgrade(backendId, false);
      setNotice(
        `Upgrade ${res.status} on ${key} (job ${res.upgradeId}). ` +
        `The daemon restarts if a new binary is applied — this view refreshes as the version changes.`,
      );

      const startedAt = Date.now();
      const poll = setInterval(async () => {
        try {
          const bs = await getClient(server).listBackends();
          setBackends(bs);
          const b = bs.find((x) => x.id === backendId) ||
            (backendId === '' ? bs.find((x) => x.type === 'local') : undefined);
          const changed = b && b.healthy &&
            normalizeVersion(b.version || '') !== normalizeVersion(fromVersion);
          if (changed || Date.now() - startedAt > 5 * 60 * 1000) {
            clearInterval(poll);
            setUpgrading((u) => ({ ...u, [key]: false }));
            load();
          }
        } catch {
          /* ignore transient poll errors — the daemon may be restarting */
        }
      }, 3000);
    } catch (e) {
      setNotice(e instanceof Error ? e.message : 'Upgrade failed');
      setUpgrading((u) => ({ ...u, [key]: false }));
    }
  }, [server, load]);

  if (loading) {
    return (
      <div className="flex items-center gap-2 p-6 text-[var(--text-muted)]">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading versions…
      </div>
