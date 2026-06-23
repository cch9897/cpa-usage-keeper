import { describe, expect, it } from 'vitest';
import type { OverviewRealtimeBlock, OverviewRealtimeWindow } from '@/lib/types';
import { resolveDisplayRealtime } from './useOverviewRealtimeData';

const realtimeForWindow = (window: OverviewRealtimeWindow): OverviewRealtimeBlock => ({
  window,
  bucket_seconds: window === '60m' ? 120 : window === '30m' ? 60 : 30,
  token_velocity: [],
  response_level: [],
  response_distribution: {
    ttft: { average_line: [], particles: [] },
    latency: { average_line: [], particles: [] },
  },
  current_usage: {
    models: [],
    api_keys: [],
    auth_files: [],
    ai_providers: [],
  },
  request_level: [],
  cache_level: [],
});

describe('resolveDisplayRealtime', () => {
  it('keeps the previous realtime block visible while a new window is loading', () => {
    expect(resolveDisplayRealtime({
      realtime: realtimeForWindow('15m'),
      loading: true,
      lastRealtimeQueryKey: ':15m',
      realtimeQueryKey: ':60m',
    })?.window).toBe('15m');
  });

  it('hides stale realtime data after loading has finished for a different query', () => {
    expect(resolveDisplayRealtime({
      realtime: realtimeForWindow('15m'),
      loading: false,
      lastRealtimeQueryKey: ':15m',
      realtimeQueryKey: ':60m',
    })).toBeNull();
  });
});
