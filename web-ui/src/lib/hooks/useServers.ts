'use client';

import { useState, useEffect, useCallback } from 'react';
import { Server, ServerWithStatus } from '@/src/types/server';
import { getServers, saveServers, addServer as addServerToStorage, removeServer as removeServerFromStorage, updateServer as updateServerInStorage } from '@/src/lib/storage';
import { getClient } from '@/src/lib/api/client';

/**
 * Hook for managing servers
 */
export function useServers() {
  const [servers, setServers] = useState<ServerWithStatus[]>([]);
  const [activeServerId, setActiveServerId] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  // Load servers from localStorage on mount
  useEffect(() => {
    const loadedServers = getServers();
    const serversWithStatus: ServerWithStatus[] = loadedServers.map(s => ({
      ...s,
      status: 'disconnected' as const,
    }));
    setServers(serversWithStatus);

    // Set first server as active if any exist
    if (serversWithStatus.length > 0 && !activeServerId) {
      setActiveServerId(serversWithStatus[0].id);
    }

    setIsLoading(false);
  }, []);

  // Test connection for all servers on load
  useEffect(() => {
    servers.forEach(server => {
      if (server.status === 'disconnected') {
        testServerConnection(server.id);
      }
    });
  }, [servers.length]);

  /**
   * Test connection to a specific server
   */
  const testServerConnection = useCallback(async (serverId: string) => {
    setServers(prev => prev.map(s =>
      s.id === serverId ? { ...s, status: 'connecting' as const } : s
    ));

    const server = servers.find(s => s.id === serverId);
    if (!server) return false;

    try {
      const client = getClient(server);
      const connected = await client.testConnection();

      setServers(prev => prev.map(s =>
        s.id === serverId
          ? { ...s, status: connected ? 'connected' as const : 'error' as const }
          : s
      ));

      return connected;
    } catch (error) {
      setServers(prev => prev.map(s =>
        s.id === serverId
          ? { ...s, status: 'error' as const, errorMessage: String(error) }
          : s
      ));
      return false;
    }
  }, [servers]);

  /**
   * Add a new server
   */
  const addServer = useCallback(async (name: string, endpoint: string, token: string): Promise<Server> => {
    const newServer = addServerToStorage(name, endpoint, token);
    const serverWithStatus: ServerWithStatus = {
      ...newServer,
      status: 'connecting',
    };

    setServers(prev => [...prev, serverWithStatus]);
    setActiveServerId(newServer.id);

    // Test connection
    setTimeout(() => testServerConnection(newServer.id), 100);

    return newServer;
  }, [testServerConnection]);

  /**
   * Remove a server
   */
  const removeServer = useCallback((serverId: string) => {
    removeServerFromStorage(serverId);
    setServers(prev => {
      const filtered = prev.filter(s => s.id !== serverId);

      // If we removed the active server, switch to another one
      if (activeServerId === serverId && filtered.length > 0) {
        setActiveServerId(filtered[0].id);
      } else if (filtered.length === 0) {
        setActiveServerId(null);
      }

      return filtered;
    });
  }, [activeServerId]);

  /**
   * Update a server
   */
  const updateServer = useCallback(async (serverId: string, name: string, endpoint: string, token: string): Promise<Server | null> => {
    const updated = updateServerInStorage(serverId, { name, endpoint, token });
    if (!updated) return null;

    setServers(prev => prev.map(s =>
      s.id === serverId
        ? { ...s, name, endpoint, token, status: 'connecting' as const }
        : s
    ));

    // Test connection after update
    setTimeout(() => testServerConnection(serverId), 100);

    return updated;
  }, [testServerConnection]);

  /**
   * Get the currently active server
   */
  const activeServer = servers.find(s => s.id === activeServerId) || null;

  return {
    servers,
    activeServer,
    activeServerId,
    setActiveServerId,
    addServer,
    removeServer,
    updateServer,
    testServerConnection,
    isLoading,
  };
}
