'use client';

import useSWR from 'swr';
import { Server } from '@/src/types/server';
import { AuditLogsResponse, AuditLogsParams } from '@/src/types/audit';
import { getClient } from '@/src/lib/api/client';
import { usePageVisibility } from './usePageVisibility';

/**
 * Hook for fetching audit logs with filtering and pagination
 */
export function useAudit(server: Server | null, params?: AuditLogsParams) {
  const isVisible = usePageVisibility();
  const fetcher = async (): Promise<AuditLogsResponse> => {
    if (!server) throw new Error('No server');
    const client = getClient(server);
    return client.getAuditLogs(params);
  };

  // Include params in the SWR key so changes trigger refetch
  const swrKey = server
    ? `audit-logs-${server.id}-${JSON.stringify(params || {})}`
    : null;

  const { data, error, isLoading, mutate } = useSWR<AuditLogsResponse>(
