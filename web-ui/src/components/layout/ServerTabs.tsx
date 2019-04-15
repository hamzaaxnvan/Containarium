'use client';

import { X, Pencil } from 'lucide-react';
import { ServerWithStatus } from '@/src/types/server';

interface ServerTabsProps {
  servers: ServerWithStatus[];
  activeServerId: string | null;
  onServerChange: (serverId: string) => void;
  onRemoveServer: (serverId: string) => void;
  onEditServer: (serverId: string) => void;
}

function statusDot(status: ServerWithStatus['status']) {
  switch (status) {
    case 'connected':  return 'bg-emerald-500';
    case 'error':      return 'bg-red-500';
