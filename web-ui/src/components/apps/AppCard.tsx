'use client';

import { Play, Square, RotateCcw, Trash2, Terminal, Shield, Globe, Server } from 'lucide-react';
import { App, AppState, getAppStateName, getAppStateColor, getACLPresetName } from '@/src/types/app';

interface AppCardProps {
  app: App;
  onStop: (username: string, appName: string) => void;
  onStart: (username: string, appName: string) => void;
  onRestart: (username: string, appName: string) => void;
  onDelete: (username: string, appName: string) => void;
  onViewLogs?: (username: string, appName: string) => void;
}

function stateBorder(state: string) {
  switch (getAppStateColor(state as AppState)) {
    case 'success': return 'border-l-emerald-500';
    case 'error':   return 'border-l-red-500';
    case 'warning': return 'border-l-amber-500';
    default:        return 'border-l-zinc-600';
  }
}

function stateBadge(state: string) {
  switch (getAppStateColor(state as AppState)) {
    case 'success': return 'border-emerald-500/30 bg-emerald-500/15 text-[var(--c-emerald)]';
    case 'error':   return 'border-red-500/30 bg-red-500/15 text-[var(--c-red)]';
    case 'warning': return 'border-amber-500/30 bg-amber-500/15 text-[var(--c-amber)]';
    default:        return 'border-zinc-500/30 bg-zinc-500/15 text-zinc-400';
  }
}
