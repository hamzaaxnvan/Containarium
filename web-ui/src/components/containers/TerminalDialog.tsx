'use client';

import { useEffect, useRef, useState, useCallback } from 'react';
import { X, Loader2 } from 'lucide-react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';

interface TerminalDialogProps {
  open: boolean;
  onClose: () => void;
  containerName: string;
  username: string;
  serverEndpoint: string;
  token: string;
}

interface TerminalMessage {
  type: 'input' | 'output' | 'resize' | 'error';
  data?: string;
  cols?: number;
  rows?: number;
}

export default function TerminalDialog({ open, onClose, containerName, username, serverEndpoint, token }: TerminalDialogProps) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const terminalInstance = useRef<Terminal | null>(null);
  const fitAddon = useRef<FitAddon | null>(null);
