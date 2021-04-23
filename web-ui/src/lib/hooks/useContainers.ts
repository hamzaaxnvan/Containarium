'use client';

import { useCallback } from 'react';
import useSWR from 'swr';
import { Container, CreateContainerRequest, SystemInfo, BackendInfo } from '@/src/types/container';
import { Server } from '@/src/types/server';
import { getClient, CoreService } from '@/src/lib/api/client';
import { useEventStream } from '@/src/lib/events/useEventStream';
import { ServerEvent, isContainerEvent, ConnectionStatus } from '@/src/types/events';
import { usePageVisibility } from './usePageVisibility';

export interface CreateContainerProgress {
  state: string;
  message: string;
}

/**
 * Hook for managing containers for a specific server
 */
export function useContainers(server: Server | null) {
  const isVisible = usePageVisibility();
  // Create a stable fetcher for this server
  const fetcher = async (): Promise<Container[]> => {
    if (!server) return [];
    const client = getClient(server);
    return client.listContainers();
  };

  // SWR key is based on server endpoint to ensure proper caching
  const swrKey = server ? 'containers-' + server.id : null;

  const { data, error, isLoading, mutate } = useSWR<Container[]>(
    swrKey,
    fetcher,
    {
      // No polling - rely on SSE for real-time updates
      refreshInterval: 0,
      revalidateOnFocus: true,
      dedupingInterval: 5000,
    }
  );

  // Handle container events from SSE
  const handleEvent = useCallback(
    (event: ServerEvent) => {
      if (!isContainerEvent(event)) return;

      // Revalidate container list on any container event
      mutate();
    },
    [mutate]
  );

  // Subscribe to container events via SSE
  const { status: eventStatus, reconnect } = useEventStream(server, {
    resourceTypes: ['RESOURCE_TYPE_CONTAINER'],
    onEvent: handleEvent,
  });

  // Fetch system info (including network CIDR)
  const systemInfoFetcher = async (): Promise<SystemInfo | null> => {
    if (!server) return null;
    const client = getClient(server);
    try {
      return await client.getSystemInfo();
    } catch {
      return null;
    }
  };

  const systemInfoKey = server ? 'systeminfo-' + server.id : null;
  const { data: systemInfo } = useSWR<SystemInfo | null>(
    systemInfoKey,
    systemInfoFetcher,
    {
      refreshInterval: isVisible ? 60000 : 0, // Refresh every minute (paused when tab hidden)
      revalidateOnFocus: false,
      dedupingInterval: 30000,
    }
  );

