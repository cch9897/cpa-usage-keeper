import { useCallback, useEffect, useState } from 'react'
import { ApiError, fetchUsageQuotaInspectionStatus, startUsageQuotaInspection } from '@/lib/api'
import type { UsageQuotaInspectionStatusResponse } from '@/lib/types'

export const QUOTA_INSPECTION_REFRESH_INTERVAL_MS = 3_000

interface UseQuotaInspectionOptions {
  enabled: boolean
  onAuthRequired?: () => void
}

export interface QuotaInspectionState {
  quotaInspectionStatus: UsageQuotaInspectionStatusResponse | null
  quotaInspectionLoading: boolean
  quotaInspectionStarting: boolean
  quotaInspectionError: string
  refreshQuotaInspectionStatus: () => Promise<void>
  startQuotaInspection: () => Promise<void>
}

export function useQuotaInspection({ enabled, onAuthRequired }: UseQuotaInspectionOptions): QuotaInspectionState {
  const [quotaInspectionStatus, setQuotaInspectionStatus] = useState<UsageQuotaInspectionStatusResponse | null>(null)
  const [quotaInspectionLoading, setQuotaInspectionLoading] = useState(false)
  const [quotaInspectionStarting, setQuotaInspectionStarting] = useState(false)
  const [quotaInspectionError, setQuotaInspectionError] = useState('')

  const handleInspectionError = useCallback((error: unknown) => {
    if (error instanceof ApiError && error.status === 401) {
      onAuthRequired?.()
      return
    }
    setQuotaInspectionError(error instanceof Error ? error.message : 'Failed to load quota inspection status')
  }, [onAuthRequired])

  const refreshQuotaInspectionStatus = useCallback(async (signal?: AbortSignal) => {
    setQuotaInspectionLoading(true)
    setQuotaInspectionError('')
    try {
      const response = await fetchUsageQuotaInspectionStatus(signal)
      setQuotaInspectionStatus(response)
    } catch (error) {
      if (signal?.aborted) {
        return
      }
      handleInspectionError(error)
    } finally {
      if (!signal?.aborted) {
        setQuotaInspectionLoading(false)
      }
    }
  }, [handleInspectionError])

  useEffect(() => {
    if (!enabled) {
      return
    }
    let cancelled = false
    let timer: number | undefined
    const controller = new AbortController()
    const poll = async () => {
      await refreshQuotaInspectionStatus(controller.signal)
      if (cancelled) {
        return
      }
      timer = window.setTimeout(() => {
        void poll()
      }, QUOTA_INSPECTION_REFRESH_INTERVAL_MS)
    }
    void poll()
    return () => {
      cancelled = true
      controller.abort()
      if (timer !== undefined) {
        window.clearTimeout(timer)
      }
    }
  }, [enabled, refreshQuotaInspectionStatus])

  const startQuotaInspection = useCallback(async () => {
    setQuotaInspectionStarting(true)
    setQuotaInspectionError('')
    try {
      const response = await startUsageQuotaInspection()
      setQuotaInspectionStatus(response)
    } catch (error) {
      handleInspectionError(error)
    } finally {
      setQuotaInspectionStarting(false)
    }
  }, [handleInspectionError])

  return {
    quotaInspectionStatus,
    quotaInspectionLoading,
    quotaInspectionStarting,
    quotaInspectionError,
    refreshQuotaInspectionStatus: () => refreshQuotaInspectionStatus(),
    startQuotaInspection,
  }
}
