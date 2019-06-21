'use client';

import { useEffect, useRef, useCallback, useState, useMemo } from 'react';
import { Server } from '@/src/types/server';
import {
  ServerEvent,
  ConnectionStatus,
  EventStreamOptions,
} from '@/src/types/events';

// Grace period before showing error status (ms)
// This prevents UI flicker during expected reconnections
const ERROR_GRACE_PERIOD_MS = 5000;

// Max reconnect attempts before showing persistent error
const MAX_SILENT_RECONNECTS = 3;

// Global connection manager to share SSE connections
const connectionManager = new Map<string, {
  eventSource: EventSource;
  listeners: Set<(event: ServerEvent) => void>;
  status: ConnectionStatus;
  reconnectTimeout: NodeJS.Timeout | null;
  reconnectAttempts: number;
  lastConnectedTime: number;
}>();

/**
 * Get a unique key for a server connection
 */
function getConnectionKey(server: Server): string {
  return `${server.id}-${server.endpoint}`;
}

/**
 * Build SSE URL for a server
 */
function buildEventUrl(server: Server): string {
  if (server.endpoint === '__demo__') return '';

  // Normalize base URL - the endpoint already includes /v1
  let baseUrl = server.endpoint.trim();

  // Remove trailing slashes
  while (baseUrl.endsWith('/')) {
    baseUrl = baseUrl.slice(0, -1);
  }

  // Build URL - endpoint should be like https://example.com/v1
  // We append /events/subscribe to get https://example.com/v1/events/subscribe
  const fullUrl = `${baseUrl}/events/subscribe`;
  const url = new URL(fullUrl);

  url.searchParams.set('token', server.token);
  // Subscribe to all resource types
  url.searchParams.append('resourceTypes', 'CONTAINER');
  url.searchParams.append('resourceTypes', 'APP');
  url.searchParams.append('resourceTypes', 'ROUTE');

  return url.toString();
}
