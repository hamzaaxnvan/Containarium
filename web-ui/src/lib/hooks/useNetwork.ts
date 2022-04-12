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
    {
      refreshInterval: 0,
      revalidateOnFocus: true,
      dedupingInterval: 5000,
    }
  );

  const addPassthroughRoute = async (
    externalPort: number,
    targetIp: string,
    targetPort: number,
    protocol?: string,
    containerName?: string,
    description?: string
  ) => {
    if (!server) throw new Error('No server selected');
    const client = getClient(server);
    const route = await client.addPassthroughRoute(externalPort, targetIp, targetPort, protocol, containerName, description);
    await mutate();
    return route;
  };

  const deletePassthroughRoute = async (externalPort: number, protocol?: string) => {
    if (!server) throw new Error('No server selected');
    const client = getClient(server);
    await client.deletePassthroughRoute(externalPort, protocol);
    await mutate();
  };

  const updatePassthroughRoute = async (externalPort: number, protocol: string, options: { active?: boolean; targetIp?: string; targetPort?: number }) => {
    if (!server) throw new Error('No server selected');
    const client = getClient(server);
    const route = await client.updatePassthroughRoute(externalPort, protocol, options);
    await mutate();
    return route;
  };

  return {
    routes: data || [],
    isLoading,
    error,
    addPassthroughRoute,
    deletePassthroughRoute,
    updatePassthroughRoute,
    refresh: () => mutate(),
  };
}

/**
 * Hook for managing network topology
 */
export function useNetworkTopology(server: Server | null, includeStopped: boolean = false) {
  const fetcher = async (): Promise<NetworkTopology> => {
    if (!server) return { nodes: [], edges: [], networkCidr: '', gatewayIp: '' };
    const client = getClient(server);
    return client.getNetworkTopology(includeStopped);
  };

  const swrKey = server ? `topology-${server.id}-${includeStopped}` : null;

  const { data, error, isLoading, mutate } = useSWR<NetworkTopology>(
    swrKey,
    fetcher,
    {
      // No polling - rely on SSE for real-time updates
      refreshInterval: 0,
      revalidateOnFocus: true,
      dedupingInterval: 5000,
    }
  );

  // Handle events that affect topology (containers and routes)
  const handleEvent = useCallback(
    () => {
      // Revalidate topology on any container or route event
      mutate();
    },
    [mutate]
  );

  // Subscribe to container and route events via SSE
  const { status: eventStatus, reconnect } = useEventStream(server, {
    resourceTypes: ['RESOURCE_TYPE_CONTAINER', 'RESOURCE_TYPE_ROUTE'],
    onEvent: handleEvent,
  });

  return {
    topology: data || { nodes: [], edges: [], networkCidr: '', gatewayIp: '' },
    isLoading,
    error,
    refresh: () => mutate(),
    // Event stream status
    eventStatus,
    reconnectEvents: reconnect,
  };
}

/**
 * Hook for managing ACL for a container (DevBox)
 * Firewall is applied at the container level, not per-app
 */
export function useContainerACL(server: Server | null, username: string) {
