'use client';

import { useCallback } from 'react';
import useSWR from 'swr';
import { NetworkACL, ProxyRoute, NetworkTopology, ACLPresetInfo, DNSRecord, RouteProtocol, PassthroughRoute } from '@/src/types/app';
import { Server } from '@/src/types/server';
import { getClient } from '@/src/lib/api/client';
import { useEventStream } from '@/src/lib/events/useEventStream';
import { ServerEvent, isRouteEvent } from '@/src/types/events';

/**
 * Hook for managing network routes
 */
export function useRoutes(server: Server | null, username?: string) {
  const fetcher = async (): Promise<ProxyRoute[]> => {
    if (!server) return [];
    const client = getClient(server);
    return client.getRoutes(username);
  };

  const swrKey = server ? `routes-${server.id}-${username || 'all'}` : null;

  const { data, error, isLoading, mutate } = useSWR<ProxyRoute[]>(
    swrKey,
    fetcher,
    {
      // No polling - rely on SSE for real-time updates
      refreshInterval: 0,
      revalidateOnFocus: true,
      dedupingInterval: 5000,
    }
  );

  // Handle route events from SSE
  const handleEvent = useCallback(
    (event: ServerEvent) => {
      if (!isRouteEvent(event)) return;

      // Revalidate route list on any route event
      mutate();
    },
    [mutate]
  );

  // Subscribe to route events via SSE
  const { status: eventStatus, reconnect } = useEventStream(server, {
    resourceTypes: ['RESOURCE_TYPE_ROUTE'],
    onEvent: handleEvent,
  });

  const addRoute = async (domain: string, targetIp: string, targetPort: number, protocol?: RouteProtocol) => {
    if (!server) throw new Error('No server selected');
    const client = getClient(server);
    const route = await client.addRoute(domain, targetIp, targetPort, protocol);
    await mutate();
    return route;
  };

  const deleteRoute = async (domain: string) => {
    if (!server) throw new Error('No server selected');
    const client = getClient(server);
    await client.deleteRoute(domain);
    await mutate();
  };

  const updateRoute = async (domain: string, options: { active?: boolean; targetIp?: string; targetPort?: number; protocol?: string }) => {
    if (!server) throw new Error('No server selected');
    const client = getClient(server);
    const route = await client.updateRoute(domain, options);
    await mutate();
    return route;
  };

  return {
    routes: data || [],
    isLoading,
    error,
    addRoute,
    deleteRoute,
    updateRoute,
    refresh: () => mutate(),
    // Event stream status
    eventStatus,
    reconnectEvents: reconnect,
  };
}

/**
 * Hook for managing passthrough routes (TCP/UDP port forwarding)
 */
export function usePassthroughRoutes(server: Server | null) {
  const fetcher = async (): Promise<PassthroughRoute[]> => {
    if (!server) return [];
    const client = getClient(server);
    return client.getPassthroughRoutes();
  };

  const swrKey = server ? `passthrough-routes-${server.id}` : null;

  const { data, error, isLoading, mutate } = useSWR<PassthroughRoute[]>(
    swrKey,
    fetcher,
