'use client';

import { useCallback } from 'react';
import useSWR from 'swr';
import { Collaborator, AddCollaboratorRequest } from '@/src/types/container';
import { Server } from '@/src/types/server';
import { getClient } from '@/src/lib/api/client';

/**
 * Hook for managing collaborators for a specific container
 */
export function useCollaborators(server: Server | null, ownerUsername: string | null) {
  const fetcher = async (): Promise<Collaborator[]> => {
    if (!server || !ownerUsername) return [];
    const client = getClient(server);
    return client.listCollaborators(ownerUsername);
  };

  const swrKey = server && ownerUsername ? `collaborators-${server.id}-${ownerUsername}` : null;

  const { data, error, isLoading, mutate } = useSWR<Collaborator[]>(
    swrKey,
    fetcher,
    {
      refreshInterval: 0,
      revalidateOnFocus: true,
      dedupingInterval: 5000,
    }
  );

  const addCollaborator = useCallback(
