import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  ApiError,
  createZenMuxCredential,
  deleteZenMuxCredential,
  fetchZenMuxCredentials,
  updateZenMuxCredential,
  verifyZenMuxCredential,
} from '@/lib/api'
import type { UsageIdentity, ZenMuxCredential, ZenMuxCredentialUpdateInput } from '@/lib/types'
import { Modal } from '@/components/ui/Modal'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Select, type SelectOption } from '@/components/ui/Select'
import {
  CredentialBadge,
  CredentialSectionShell,
  MetricPill,
  TonePercent,
  cacheReadRateTone,
  formatCredentialNumber,
  successRateTone,
} from './CredentialSectionShell'
import { formatUsd } from '@/utils/usage'
import sharedStyles from './CredentialSections.module.scss'
import styles from './ZenMuxCredentialsCard.module.scss'

export const ZENMUX_DEFAULT_ENDPOINT = 'https://zenmux.ai/api/v1/management/payg/balance'

const UNBOUND_AUTH_INDEX = ''

interface ZenMuxCredentialFormChange {
  name?: string
  apiKey?: string
  endpoint?: string
  authIndex?: string
}

export interface ZenMuxCredentialFormProps {
  mode: 'create' | 'edit'
  name: string
  apiKey: string
  endpoint: string
  authIndex: string
  apiKeyPreview?: string
  authFileOptions: SelectOption[]
  disabled?: boolean
  onChange: (patch: ZenMuxCredentialFormChange) => void
}

export function ZenMuxCredentialForm({
  mode,
  name,
  apiKey,
  endpoint,
  authIndex,
  apiKeyPreview,
  authFileOptions,
  disabled = false,
  onChange,
}: ZenMuxCredentialFormProps) {
  const { t } = useTranslation()
  const bindOptions = useMemo<SelectOption[]>(() => [
    { value: UNBOUND_AUTH_INDEX, label: t('usage_stats.credentials_zenmux_bind_none') },
    ...authFileOptions,
  ], [authFileOptions, t])

  return (
    <div className={styles.zenmuxFormFields}>
      <Input
        label={t('usage_stats.credentials_zenmux_name')}
        value={name}
        onChange={(event) => onChange({ name: event.target.value })}
        disabled={disabled}
        placeholder={t('usage_stats.credentials_zenmux_name_placeholder')}
        autoComplete="off"
      />
      <Input
        label={t('usage_stats.credentials_zenmux_api_key')}
        value={apiKey}
        onChange={(event) => onChange({ apiKey: event.target.value })}
        disabled={disabled}
        type="password"
        autoComplete="new-password"
        placeholder={mode === 'edit' ? apiKeyPreview : t('usage_stats.credentials_zenmux_api_key_placeholder')}
        hint={mode === 'edit' ? t('usage_stats.credentials_zenmux_api_key_keep') : undefined}
      />
      <Input
        label={t('usage_stats.credentials_zenmux_endpoint')}
        value={endpoint}
        onChange={(event) => onChange({ endpoint: event.target.value })}
        disabled={disabled}
        placeholder={ZENMUX_DEFAULT_ENDPOINT}
        autoComplete="off"
      />
      <div className={styles.zenmuxFormField}>
        <label>{t('usage_stats.credentials_zenmux_bind_identity')}</label>
        <Select
          value={authIndex}
          options={bindOptions}
          onChange={(value) => onChange({ authIndex: value })}
          disabled={disabled}
          ariaLabel={t('usage_stats.credentials_zenmux_bind_identity')}
        />
      </div>
    </div>
  )
}

interface ZenMuxCredentialsCardProps {
  authFileRows: UsageIdentity[]
  initialItems?: ZenMuxCredential[]
  onNotice?: (kind: 'success' | 'info' | 'error', message: string) => void
  onAuthRequired?: () => void
}

export function ZenMuxCredentialsCard({ authFileRows, initialItems, onNotice, onAuthRequired }: ZenMuxCredentialsCardProps) {
  const { t } = useTranslation()
  const [items, setItems] = useState<ZenMuxCredential[]>(initialItems ?? [])
  const [loading, setLoading] = useState(initialItems === undefined)
  const [loadError, setLoadError] = useState('')
  const [verifyingId, setVerifyingId] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ZenMuxCredential | null>(null)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState('')
  const [formName, setFormName] = useState('')
  const [formApiKey, setFormApiKey] = useState('')
  const [formEndpoint, setFormEndpoint] = useState(ZENMUX_DEFAULT_ENDPOINT)
  const [formAuthIndex, setFormAuthIndex] = useState(UNBOUND_AUTH_INDEX)
  const [deleteConfirmId, setDeleteConfirmId] = useState('')
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()
    void (async () => {
      try {
        const response = await fetchZenMuxCredentials(controller.signal)
        if (!cancelled) {
          setItems(response.items)
          setLoadError('')
        }
      } catch (error) {
        if (cancelled) return
        if (error instanceof ApiError && error.status === 401) {
          onAuthRequired?.()
          return
        }
        setLoadError(error instanceof Error ? error.message : String(error))
        onNotice?.('error', t('usage_stats.credentials_zenmux_load_failed'))
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    })()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [onAuthRequired, onNotice, t])

  const identityNameByIndex = useMemo(() => {
    const map = new Map<string, string>()
    for (const row of authFileRows) {
      if (!map.has(row.identity)) {
        map.set(row.identity, row.displayName || row.identity)
      }
    }
    return map
  }, [authFileRows])

  const authFileOptions = useMemo<SelectOption[]>(() => authFileRows.map((row) => ({
    value: row.identity,
    label: row.displayName || row.identity,
  })), [authFileRows])

  const boundIdentityName = useCallback((credential: ZenMuxCredential): string => {
    if (!credential.auth_index) {
      return t('usage_stats.credentials_zenmux_bind_none')
    }
    return identityNameByIndex.get(credential.auth_index) ?? credential.auth_index
  }, [identityNameByIndex, t])

  const formatCheckedAt = useCallback((checkedAt: string | null): string => {
    if (!checkedAt) {
      return t('usage_stats.credentials_zenmux_never_verified')
    }
    const date = new Date(checkedAt)
    if (Number.isNaN(date.getTime())) {
      return checkedAt
    }
    return date.toLocaleString()
  }, [t])

  const formatBalanceUsd = useCallback((value: number | null | undefined): string => (
    typeof value === 'number' && Number.isFinite(value) ? formatUsd(value) : '-'
  ), [])

  const openCreateModal = useCallback(() => {
    setEditing(null)
    setFormName('')
    setFormApiKey('')
    setFormEndpoint(ZENMUX_DEFAULT_ENDPOINT)
    setFormAuthIndex(UNBOUND_AUTH_INDEX)
    setFormError('')
    setModalOpen(true)
  }, [])

  const openEditModal = useCallback((credential: ZenMuxCredential) => {
    setEditing(credential)
    setFormName(credential.name)
    setFormApiKey('')
    setFormEndpoint(credential.endpoint)
    setFormAuthIndex(credential.auth_index ?? UNBOUND_AUTH_INDEX)
    setFormError('')
    setModalOpen(true)
  }, [])

  const closeModal = useCallback(() => {
    if (saving) return
    setModalOpen(false)
    setEditing(null)
    setFormError('')
  }, [saving])

  const handleFormChange = useCallback((patch: ZenMuxCredentialFormChange) => {
    if (patch.name !== undefined) setFormName(patch.name)
    if (patch.apiKey !== undefined) setFormApiKey(patch.apiKey)
    if (patch.endpoint !== undefined) setFormEndpoint(patch.endpoint)
    if (patch.authIndex !== undefined) setFormAuthIndex(patch.authIndex)
  }, [])

  const handleSave = useCallback(async () => {
    const name = formName.trim()
    if (!name || (!editing && !formApiKey.trim())) {
      setFormError(t('usage_stats.credentials_zenmux_required'))
      return
    }
    setSaving(true)
    setFormError('')
    try {
      const authIndex = formAuthIndex === UNBOUND_AUTH_INDEX ? null : formAuthIndex
      const endpoint = formEndpoint.trim() || ZENMUX_DEFAULT_ENDPOINT
      if (editing) {
        const input: ZenMuxCredentialUpdateInput = {
          name,
          endpoint,
          auth_index: authIndex,
        }
        const apiKey = formApiKey.trim()
        if (apiKey) {
          input.api_key = apiKey
        }
        const updated = await updateZenMuxCredential(editing.id, input)
        setItems((current) => current.map((item) => (item.id === updated.id ? updated : item)))
      } else {
        const created = await createZenMuxCredential({
          name,
          api_key: formApiKey.trim(),
          endpoint,
          auth_index: authIndex,
        })
        setItems((current) => [created, ...current])
      }
      setModalOpen(false)
      setEditing(null)
      onNotice?.('success', t('usage_stats.credentials_zenmux_save_success'))
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onAuthRequired?.()
      }
      setFormError(error instanceof Error ? error.message : String(error))
      onNotice?.('error', t('usage_stats.credentials_zenmux_save_failed'))
    } finally {
      setSaving(false)
    }
  }, [editing, formApiKey, formAuthIndex, formEndpoint, formName, onAuthRequired, onNotice, t])

  const handleVerify = useCallback(async (credential: ZenMuxCredential) => {
    setVerifyingId(credential.id)
    try {
      const updated = await verifyZenMuxCredential(credential.id)
      setItems((current) => current.map((item) => (item.id === updated.id ? updated : item)))
      if (updated.check?.status === 'failed') {
        onNotice?.('error', t('usage_stats.credentials_zenmux_verify_failed_action'))
      } else {
        onNotice?.('success', t('usage_stats.credentials_zenmux_verify_success'))
      }
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onAuthRequired?.()
        return
      }
      onNotice?.('error', t('usage_stats.credentials_zenmux_verify_failed_action'))
    } finally {
      setVerifyingId('')
    }
  }, [onAuthRequired, onNotice, t])

  const handleDelete = useCallback(async () => {
    if (!deleteConfirmId) return
    setDeleting(true)
    setDeleteError('')
    try {
      await deleteZenMuxCredential(deleteConfirmId)
      setItems((current) => current.filter((item) => item.id !== deleteConfirmId))
      setDeleteConfirmId('')
      onNotice?.('success', t('usage_stats.credentials_zenmux_delete_success'))
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onAuthRequired?.()
        setDeleteConfirmId('')
        return
      }
      setDeleteError(error instanceof Error ? error.message : String(error))
      onNotice?.('error', t('usage_stats.credentials_zenmux_delete_failed'))
    } finally {
      setDeleting(false)
    }
  }, [deleteConfirmId, onAuthRequired, onNotice, t])

  const deleteTarget = items.find((item) => item.id === deleteConfirmId)

  return (
    <>
      <CredentialSectionShell
        title={t('usage_stats.credentials_zenmux_title')}
        subtitle={t('usage_stats.credentials_zenmux_subtitle')}
        countLabel={t('usage_stats.credentials_count', { count: items.length })}
        actions={(
          <Button type="button" variant="primary" appearance="action" size="sm" onClick={openCreateModal}>
            {t('usage_stats.credentials_zenmux_add')}
          </Button>
        )}
      >
        {loading && <div className={sharedStyles.credentialEmptyState}>{t('common.loading')}</div>}
        {!loading && loadError && <div className={sharedStyles.credentialInlineError}>{loadError}</div>}
        {!loading && !loadError && items.length === 0 && (
          <div className={sharedStyles.credentialEmptyState}>{t('usage_stats.credentials_zenmux_empty')}</div>
        )}
        {!loading && !loadError && items.length > 0 && (
          <>
            <div className={`${sharedStyles.credentialTableHeader} ${styles.zenmuxTableHeader}`}>
              <span className={sharedStyles.credentialTableHeaderName}>{t('usage_stats.credentials_column_name')}</span>
              <div className={sharedStyles.credentialMetricHeaderGroup}>
                <span className={sharedStyles.credentialMetricHeaderCell}>{t('usage_stats.credentials_zenmux_balance_total')}</span>
                <span className={sharedStyles.credentialMetricHeaderCell}>{t('usage_stats.success_rate')}</span>
                <span className={sharedStyles.credentialMetricHeaderCell}>{t('usage_stats.total_tokens')}</span>
                <span className={sharedStyles.credentialMetricHeaderCell}>{t('usage_stats.cache_rate')}</span>
              </div>
              <span className={sharedStyles.credentialTableHeaderSide}>{t('usage_stats.credentials_zenmux_last_verified')}</span>
            </div>
            {items.map((credential) => (
              <article key={credential.id} className={`${sharedStyles.credentialRow} ${styles.zenmuxCredentialRow}`}>
                <div className={sharedStyles.credentialIdentityBlock}>
                  <div className={sharedStyles.credentialIdentityContent}>
                    <div className={sharedStyles.credentialNameRow}>
                      <span className={sharedStyles.credentialDisplayName}>{credential.name}</span>
                      <CredentialBadge>{credential.api_key_preview}</CredentialBadge>
                    </div>
                    <span className={styles.zenmuxCredentialEndpoint} title={credential.endpoint}>{credential.endpoint}</span>
                    <span className={sharedStyles.credentialIdentityText}>
                      {t('usage_stats.credentials_zenmux_bind_identity')}: {boundIdentityName(credential)}
                    </span>
                    {credential.check?.status === 'failed' && credential.check.error && (
                      <div className={`${sharedStyles.credentialQuotaErrorSummary} ${styles.zenmuxCheckError}`}>
                        <span className={sharedStyles.credentialQuotaErrorCode}>{t('usage_stats.credentials_zenmux_verify_failed')}</span>
                        <span className={sharedStyles.credentialQuotaErrorMessage}>{credential.check.error}</span>
                      </div>
                    )}
                  </div>
                </div>
                <div className={sharedStyles.credentialMetricGroup}>
                  <MetricPill value={(
                    <span className={styles.zenmuxBalanceCell}>
                      <strong>{formatBalanceUsd(credential.check?.total_balance)}</strong>
                      <small>
                        {t('usage_stats.credentials_zenmux_balance_topup')} {formatBalanceUsd(credential.check?.top_up_credits)}
                        {' · '}
                        {t('usage_stats.credentials_zenmux_balance_bonus')} {formatBalanceUsd(credential.check?.bonus_credits)}
                      </small>
                    </span>
                  )} />
                  <MetricPill value={credential.stats ? <TonePercent value={credential.stats.success_rate * 100} tone={successRateTone(credential.stats.success_rate * 100)} /> : '-'} />
                  <MetricPill value={credential.stats ? formatCredentialNumber(credential.stats.total_tokens) : '-'} />
                  <MetricPill value={credential.stats ? <TonePercent value={credential.stats.cache_read_rate * 100} tone={cacheReadRateTone(credential.stats.cache_read_rate * 100)} /> : '-'} />
                </div>
                <div className={sharedStyles.credentialSidePanel}>
                  <span className={styles.zenmuxLastVerified}>
                    {t('usage_stats.credentials_zenmux_last_verified')}: {formatCheckedAt(credential.check?.checked_at ?? null)}
                  </span>
                  <div className={styles.zenmuxRowActions}>
                    <Button
                      type="button"
                      variant="secondary"
                      appearance="action"
                      size="sm"
                      loading={verifyingId === credential.id}
                      disabled={verifyingId !== '' && verifyingId !== credential.id}
                      onClick={() => void handleVerify(credential)}
                    >
                      {verifyingId === credential.id ? t('usage_stats.credentials_zenmux_verifying') : t('usage_stats.credentials_zenmux_verify')}
                    </Button>
                    <Button type="button" variant="secondary" appearance="action" size="sm" onClick={() => openEditModal(credential)}>
                      {t('common.edit')}
                    </Button>
                    <Button
                      type="button"
                      variant="danger"
                      appearance="action"
                      size="sm"
                      onClick={() => {
                        setDeleteConfirmId(credential.id)
                        setDeleteError('')
                      }}
                    >
                      {t('common.delete')}
                    </Button>
                  </div>
                </div>
              </article>
            ))}
          </>
        )}
      </CredentialSectionShell>

      <Modal
        open={modalOpen}
        title={editing ? t('usage_stats.credentials_zenmux_edit_title') : t('usage_stats.credentials_zenmux_add')}
        onClose={closeModal}
        closeDisabled={saving}
        footer={(
          <>
            <Button type="button" variant="secondary" appearance="action" onClick={closeModal} disabled={saving}>
              {t('common.cancel')}
            </Button>
            <Button type="button" variant="primary" appearance="action" onClick={() => void handleSave()} loading={saving}>
              {saving ? t('common.loading') : t('common.save')}
            </Button>
          </>
        )}
      >
        {formError && <div className={styles.zenmuxFormError}>{formError}</div>}
        <ZenMuxCredentialForm
          mode={editing ? 'edit' : 'create'}
          name={formName}
          apiKey={formApiKey}
          endpoint={formEndpoint}
          authIndex={formAuthIndex}
          apiKeyPreview={editing?.api_key_preview}
          authFileOptions={authFileOptions}
          disabled={saving}
          onChange={handleFormChange}
        />
      </Modal>

      <Modal
        open={deleteConfirmId !== ''}
        title={t('usage_stats.credentials_zenmux_delete_confirm_title')}
        onClose={() => {
          if (!deleting) setDeleteConfirmId('')
        }}
        closeDisabled={deleting}
        footer={(
          <>
            <Button type="button" variant="secondary" appearance="action" onClick={() => setDeleteConfirmId('')} disabled={deleting}>
              {t('common.cancel')}
            </Button>
            <Button type="button" variant="danger" appearance="action" onClick={() => void handleDelete()} loading={deleting}>
              {deleting ? t('common.loading') : t('common.delete')}
            </Button>
          </>
        )}
      >
        <p className={styles.zenmuxDeleteConfirmText}>
          {t('usage_stats.credentials_zenmux_delete_confirm', { name: deleteTarget?.name ?? '' })}
        </p>
        {deleteError && <div className={styles.zenmuxFormError}>{deleteError}</div>}
      </Modal>
    </>
  )
}
