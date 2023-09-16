import { Server } from '@/src/types/server';

const STORAGE_KEY = 'containarium_servers';

/**
 * Generate a unique ID for a server
 */
function generateId(): string {
  return Date.now().toString(36) + Math.random().toString(36).substring(2);
}

/**
 * Get all servers from localStorage
 */
export function getServers(): Server[] {
  if (typeof window === 'undefined') {
    return [];
  }

  try {
    const data = localStorage.getItem(STORAGE_KEY);
    if (!data) {
      return [];
    }
    return JSON.parse(data) as Server[];
  } catch (error) {
    console.error('Failed to load servers from localStorage:', error);
    return [];
  }
}

/**
 * Save servers to localStorage
 */
export function saveServers(servers: Server[]): void {
  if (typeof window === 'undefined') {
    return;
  }

  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(servers));
  } catch (error) {
    console.error('Failed to save servers to localStorage:', error);
  }
}

/**
 * Add a new server
 */
export function addServer(name: string, endpoint: string, token: string): Server {
  const servers = getServers();

  const newServer: Server = {
    id: generateId(),
    name,
    endpoint,
    token,
    addedAt: Date.now(),
  };
