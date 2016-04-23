export interface AuditLogEntry {
  id: number;
  timestamp: string;
  username: string;
  action: string;
  resourceType: string;
  resourceId: string;
  detail: string;
  sourceIp: string;
  statusCode: number;
}

export interface AuditLogsResponse {
  logs: AuditLogEntry[];
  totalCount: number;
}

