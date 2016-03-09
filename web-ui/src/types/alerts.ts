export interface AlertRule {
  id: string;
  name: string;
  expr: string;
  duration: string;
  severity: string;
  description: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface AlertRulesResponse {
  rules: AlertRule[];
}

export interface AlertingInfoResponse {
  enabled: boolean;
  vmalertStatus: string;
  alertmanagerStatus: string;
  webhookUrl: string;
  totalRules: number;
  customRules: number;
  webhookSecretConfigured: boolean;
}

export interface CreateAlertRuleRequest {
  name: string;
  expr: string;
  duration: string;
  severity: string;
  description?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  enabled: boolean;
}

export interface UpdateAlertRuleRequest {
  name?: string;
  expr?: string;
  duration?: string;
  severity?: string;
  description?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  enabled?: boolean;
}

