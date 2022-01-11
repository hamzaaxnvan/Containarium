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
