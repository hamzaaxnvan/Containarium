'use client';

import { useState, useEffect } from 'react';
import { Server, Plus, Sun, Moon } from 'lucide-react';

interface AppBarProps {
  onAddServer: () => void;
}

function ThemeToggle() {
  const [isDark, setIsDark] = useState(true);

  useEffect(() => {
    setIsDark(!document.documentElement.classList.contains('light'));
  }, []);

  const toggle = () => {
    const html = document.documentElement;
    if (html.classList.contains('light')) {
      html.classList.remove('light');
      localStorage.setItem('theme', 'dark');
      setIsDark(true);
    } else {
      html.classList.add('light');
      localStorage.setItem('theme', 'light');
      setIsDark(false);
    }
  };

  return (
    <button
      onClick={toggle}
      title={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
      className="rounded-md border border-[var(--border-subtle)] bg-[var(--surface)] p-2 text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-2)] hover:text-[var(--text)]"
    >
      {isDark ? <Sun size={14} /> : <Moon size={14} />}
    </button>
  );
}

export default function AppBar({ onAddServer }: AppBarProps) {
  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b border-[var(--border-subtle)] bg-[var(--surface)] px-4">
      <Server size={18} className="text-[var(--accent)]" />
      <span className="text-sm font-semibold tracking-tight text-[var(--text)]">
        Containarium
      </span>
      <div className="flex-1" />
      <ThemeToggle />
      <button
        onClick={onAddServer}
        className="flex items-center gap-1.5 rounded-md bg-[var(--accent)] px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-[var(--accent-hover)] active:opacity-90"
      >
        <Plus size={14} />
        Add Server
      </button>
    </header>
  );
}
