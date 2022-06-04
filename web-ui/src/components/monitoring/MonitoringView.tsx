'use client';

import { useState, useEffect, useCallback } from 'react';
import { Loader2, RefreshCw } from 'lucide-react';
import { Server } from '@/src/types/server';
import { getClient } from '@/src/lib/api/client';

export default function MonitoringView({ server }: { server: Server }) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [grafanaUrl, setGrafanaUrl] = useState('');
  const [enabled, setEnabled] = useState(false);

  const fetchInfo = useCallback(async () => {
    try {
      setLoading(true); setError(null);
      const client = getClient(server);
      const info = await client.getMonitoringInfo();
      // Mint the same-origin session cookie BEFORE the iframe paints,
      // so the iframe's request to /grafana/* carries auth. Iframes
      // can't attach an Authorization header from localStorage, so
      // without this the embedded Grafana 401s. Issue #338.
      if (info.enabled && info.grafanaUrl) {
        try {
          await client.setSessionCookie();
        } catch (e) {
          // Older daemons (pre-#338) don't have this endpoint. Don't
          // block the monitoring page render — the iframe will surface
          // its own 401, which is no worse than the pre-fix behavior.
          console.warn('Failed to set session cookie; embedded Grafana may 401', e);
