'use client';

import useSWR from 'swr';
import { Server } from '@/src/types/server';
import { ClamavSummaryResponse } from '@/src/types/security';
import { getClient } from '@/src/lib/api/client';
import { usePageVisibility } from './usePageVisibility';

export interface SecurityBadge {
  critical: number;
  high: number;
  medium: number;
  low: number;
}

export function useSecurity(server: Server | null) {
  const isVisible = usePageVisibility();
  const fetcher = async (): Promise<ClamavSummaryResponse> => {
    if (!server) throw new Error('No server');
    const client = getClient(server);
    return client.getClamavSummary();
  };

  const swrKey = server ? `security-summary-${server.id}` : null;

  const { data, error, isLoading, mutate } = useSWR<ClamavSummaryResponse>(
    swrKey,
    fetcher,
    {
      refreshInterval: isVisible ? 60000 : 0,
      revalidateOnFocus: true,
      dedupingInterval: 10000,
    }
  );

  return {
    summary: data || null,
    isLoading,
    error,
    refresh: () => mutate(),
  };
}

export function useContainerSecurityBadges(server: Server | null, containerNames: string[]) {
