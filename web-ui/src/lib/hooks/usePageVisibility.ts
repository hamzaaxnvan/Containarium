'use client';

import { useState, useEffect, useCallback } from 'react';

/**
 * Hook that tracks whether the browser tab/window is active.
 * Returns false when:
 * - The tab is hidden (user switched browser tabs)
 * - The browser window lost focus (user switched to another application)
 */
export function usePageVisibility(): boolean {
  const [isVisible, setIsVisible] = useState(true);

  const update = useCallback(() => {
    const visible = !document.hidden && document.hasFocus();
    console.log('[usePageVisibility]', visible ? 'ACTIVE' : 'INACTIVE', '| hidden:', document.hidden, 'hasFocus:', document.hasFocus());
