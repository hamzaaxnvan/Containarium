/**
 * Network protocol types
 */
export type Protocol = 'UNSPECIFIED' | 'TCP' | 'UDP' | 'ICMP';

/**
 * Connection state (primarily for TCP)
 */
export type ConnectionState =
  | 'UNSPECIFIED'
  | 'NEW'
  | 'ESTABLISHED'
  | 'RELATED'
  | 'TIME_WAIT'
  | 'CLOSE_WAIT'
  | 'FIN_WAIT'
  | 'CLOSED'
  | 'SYN_SENT'
  | 'SYN_RECV';

/**
 * Traffic direction relative to the container
 */
export type TrafficDirection = 'UNSPECIFIED' | 'INGRESS' | 'EGRESS';

/**
 * Traffic event type for real-time updates
 */
export type TrafficEventType = 'UNSPECIFIED' | 'NEW' | 'UPDATE' | 'DESTROY';

/**
 * Active network connection
 */
export interface Connection {
  id: string;
  containerName: string;
  containerIp: string;
  protocol: Protocol;
  sourceIp: string;
  sourcePort: number;
  destIp: string;
  destPort: number;
  state: ConnectionState;
  direction: TrafficDirection;
  bytesSent: number;
  bytesReceived: number;
  packetsSent: number;
  packetsReceived: number;
  firstSeen: string; // ISO timestamp
  lastSeen: string; // ISO timestamp
  timeoutSeconds: number;
}

/**
 * Real-time traffic event
 */
export interface TrafficEvent {
  type: TrafficEventType;
  connection: Connection;
  timestamp: string; // ISO timestamp
}

