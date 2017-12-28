'use client';

import { useState, useMemo, useEffect, useCallback } from 'react';
import { Bot, ExternalLink, RefreshCw, Loader2, MessagesSquare, SlidersHorizontal } from 'lucide-react';
import { ProxyRoute } from '@/src/types/app';
import { Server } from '@/src/types/server';
import { getClient } from '@/src/lib/api/client';

/**
 * WorkspaceView embeds an agent-workspace box's web chat UI (OpenHands,
 * deployed via the `agent-workspace` recipe) in an iframe, so users can
 * co-work with a coding agent without leaving the console.
 *
 * The box exposes its in-box auth proxy on the `workspace` subdomain (recipe
 * container port 8080); we discover those routes from the network route list.
 *
 * Zero-click auth: the daemon's GetWorkspaceAccess returns a bootstrap URL
 * (https://<box>-workspace.<domain>/__ws_login?t=<token>) that sets the in-box
 * SameSite=None session cookie and redirects to the workspace UI, so no sign-in
 * prompt is shown.
 *
 * Model provider + key: rather than reimplement OpenHands' (internal, fast-
 * moving, session-key-gated) settings API in our chrome, the "Model setup"
 * view deep-links the iframe to OpenHands' own Settings → LLM page
 * (/settings/llm), which already supports Anthropic, OpenAI/Codex, Gemini,
 * Mistral, and any LiteLLM provider, plus saved profiles. The bootstrap cookie
 * set by the chat view authenticates that page too.
 */
export default function WorkspaceView({ server, routes }: { server: Server; routes: ProxyRoute[] }) {
  // Identify agent-workspace boxes by the recipe's `workspace` subdomain, not by
  // a generic port (8080 is a common app port — matching it would misclassify
  // unrelated apps as workspaces).
  const workspaces = useMemo(
    () => (routes || []).filter((r) => r.active && !!r.subdomain?.includes('workspace')),
