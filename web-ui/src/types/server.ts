/**
 * Server configuration stored in localStorage
 */
export interface Server {
  id: string;
  name: string;
  endpoint: string; // Full API URL: http://192.168.1.10/api
  token: string;    // JWT token
  addedAt: number;  // Unix timestamp
}

/**
 * Server connection status
 */
export type ServerStatus = 'connected' | 'disconnected' | 'error' | 'connecting';

/**
 * Server with runtime status
 */
export interface ServerWithStatus extends Server {
  status: ServerStatus;
  errorMessage?: string;
}
