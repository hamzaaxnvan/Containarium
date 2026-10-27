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

function IconBtn({ title, onClick, disabled, className = '', children }: { title: string; onClick: () => void; disabled?: boolean; className?: string; children: React.ReactNode }) {
  return (
    <button
      title={title}
      onClick={onClick}
      disabled={disabled}
      className={`rounded p-1 text-[var(--text-muted)] transition-colors hover:text-[var(--text)] hover:bg-[var(--surface-2)] disabled:opacity-40 ${className}`}
    >
      {children}
    </button>
  );
}

export default function AppCard({ app, onStop, onStart, onRestart, onDelete, onViewLogs }: AppCardProps) {
  const isRunning = app.state === 'APP_STATE_RUNNING';
  const isStopped = app.state === 'APP_STATE_STOPPED';
  const isFailed = app.state === 'APP_STATE_FAILED';
  const isTransitioning = ['APP_STATE_UPLOADING', 'APP_STATE_BUILDING', 'APP_STATE_RESTARTING'].includes(app.state);

  return (
    <div className={`flex flex-col rounded-xl border border-[var(--border-subtle)] border-l-4 bg-[var(--surface)] shadow-sm ${stateBorder(app.state)}`}>
      {/* Header */}
      <div className="flex items-start justify-between gap-2 p-4 pb-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold text-[var(--text)]">{app.name}</p>
          <p className="text-xs text-[var(--text-muted)]">{app.username}</p>
        </div>
        <span className={`shrink-0 rounded-full border px-2 py-0.5 text-[10px] font-medium ${stateBadge(app.state)}`}>
          {getAppStateName(app.state)}
        </span>
      </div>

      <div className="border-t border-[var(--border-subtle)]" />

      {/* Details */}
      <div className="flex flex-col gap-2 p-4 pb-3 text-xs">
        <div className="flex items-center gap-2">
          <Globe size={13} className="text-[var(--text-muted)] shrink-0" />
          {isRunning ? (
            <a href={`https://${app.fullDomain}`} target="_blank" rel="noopener noreferrer" className="truncate text-[var(--c-blue)] hover:underline">
              {app.fullDomain}
            </a>
          ) : (
            <span className="truncate text-[var(--text-secondary)]">{app.fullDomain}</span>
          )}
        </div>
        {app.containerIp && (
          <div className="flex items-center gap-2">
            <Server size={13} className="text-[var(--text-muted)] shrink-0" />
            <span className="font-mono text-[var(--text-secondary)]">{app.containerIp}:{app.port}</span>
          </div>
        )}
        <div className="flex items-center gap-2">
          <Shield size={13} className="text-[var(--text-muted)] shrink-0" />
          <span title="Firewall is managed at container level" className={`rounded-full border px-2 py-0.5 text-[10px] ${app.aclPreset === 'ACL_PRESET_FULL_ISOLATION' ? 'border-emerald-500/30 bg-emerald-500/10 text-[var(--c-emerald)]' : 'border-[var(--border)] bg-[var(--surface-2)] text-[var(--text-secondary)]'}`}>
            {getACLPresetName(app.aclPreset || 'ACL_PRESET_UNSPECIFIED')}
          </span>
        </div>
        {isFailed && app.errorMessage && (
          <div className="rounded bg-red-500/10 p-2 text-[var(--c-red)]">{app.errorMessage}</div>
        )}
        {app.resources && (
          <p className="text-[var(--text-muted)]">{app.resources.cpu} CPU · {app.resources.memory} RAM</p>
        )}
      </div>

      {/* Actions */}
      <div className="flex items-center justify-end gap-0.5 border-t border-[var(--border-subtle)] px-3 py-2">
        {onViewLogs && (
          <IconBtn title="View Logs" onClick={() => onViewLogs(app.username, app.name)}>
            <Terminal size={14} />
          </IconBtn>
        )}
        {isRunning && (
          <IconBtn title="Stop" onClick={() => onStop(app.username, app.name)} className="hover:text-[var(--c-amber)]">
            <Square size={14} />
          </IconBtn>
        )}
        {isStopped && (
          <IconBtn title="Start" onClick={() => onStart(app.username, app.name)} className="hover:text-[var(--c-emerald)]">
            <Play size={14} />
          </IconBtn>
        )}
        {(isRunning || isStopped) && (
          <IconBtn title="Restart" onClick={() => onRestart(app.username, app.name)} disabled={isTransitioning}>
            <RotateCcw size={14} />
          </IconBtn>
        )}
        <IconBtn title="Delete" onClick={() => onDelete(app.username, app.name)} className="hover:text-[var(--c-red)] hover:bg-red-500/10">
          <Trash2 size={14} />
        </IconBtn>
      </div>
    </div>
  );
}
