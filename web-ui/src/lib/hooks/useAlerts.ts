'use client';

import useSWR from 'swr';
import { Server } from '@/src/types/server';
import { AlertRulesResponse, AlertingInfoResponse, WebhookDeliveriesResponse } from '@/src/types/alerts';
import { getClient } from '@/src/lib/api/client';
import { usePageVisibility } from './usePageVisibility';

/**
 * Hook for fetching alert rules
 */
export function useAlerts(server: Server | null) {
  const isVisible = usePageVisibility();
  const fetcher = async (): Promise<AlertRulesResponse> => {
    if (!server) throw new Error('No server');
    const client = getClient(server);
    return client.listAlertRules();
  };

  const swrKey = server ? `alert-rules-${server.id}` : null;

  const { data, error, isLoading, mutate } = useSWR<AlertRulesResponse>(
    swrKey,
    fetcher,
    {
      refreshInterval: isVisible ? 30000 : 0,
      revalidateOnFocus: true,
      dedupingInterval: 5000,
    }
  );

  return {
    rules: data?.rules || [],
    isLoading,
    error,
    refresh: () => mutate(),
  };
}

/**
 * Hook for fetching alerting system info
 */
export function useAlertingInfo(server: Server | null) {
  const isVisible = usePageVisibility();
  const fetcher = async (): Promise<AlertingInfoResponse> => {
    if (!server) throw new Error('No server');
    const client = getClient(server);
    return client.getAlertingInfo();
  };

  const swrKey = server ? `alerting-info-${server.id}` : null;

  const { data, error, isLoading, mutate } = useSWR<AlertingInfoResponse>(
    swrKey,
    fetcher,
    {
      refreshInterval: isVisible ? 60000 : 0,
      revalidateOnFocus: true,
      dedupingInterval: 10000,
    }
  );

  return {
    info: data || null,
