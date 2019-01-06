'use client';

import { useRef, useMemo } from 'react';
import useSWR from 'swr';
import { ContainerMetrics, ContainerMetricsWithRate } from '@/src/types/container';
import { Server } from '@/src/types/server';
import { getClient } from '@/src/lib/api/client';
import { usePageVisibility } from './usePageVisibility';

interface MetricsSnapshot {
  metrics: Record<string, ContainerMetrics>;
  timestamp: number;
}

/**
 * Hook for fetching container metrics with polling and CPU rate calculation
 */
export function useMetrics(server: Server | null, enabled: boolean = true) {
  const isVisible = usePageVisibility();
  // Store previous metrics for CPU rate calculation
  const prevSnapshot = useRef<MetricsSnapshot | null>(null);

  const fetcher = async (): Promise<ContainerMetrics[]> => {
    if (!server) return [];
    const client = getClient(server);
    return client.getMetrics();
  };

  const swrKey = server && enabled ? 'metrics-' + server.id : null;

  const { data, error, isLoading, mutate } = useSWR<ContainerMetrics[]>(
    swrKey,
    fetcher,
    {
      refreshInterval: (() => { const interval = isVisible ? 5000 : 0; console.log('[useMetrics] refreshInterval:', interval, 'isVisible:', isVisible); return interval; })(),
      revalidateOnFocus: true,
      dedupingInterval: 2000,
    }
  );

  // Calculate CPU rate by comparing with previous snapshot
  const metricsWithRate = useMemo((): ContainerMetricsWithRate[] => {
    if (!data || data.length === 0) return [];

    const now = Date.now();
    const currentMap: Record<string, ContainerMetrics> = {};
    for (const m of data) {
      currentMap[m.name] = m;
    }

    const result: ContainerMetricsWithRate[] = [];

    for (const m of data) {
