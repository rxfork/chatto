import {
  BurnAttachmentViewerStatus,
  type BurnAttachment
} from '@chatto/api-types/api/v1/message_types_pb';
import type {
  BurnAttachmentView,
  BurnAttachmentViewerState
} from '../timeline/messageAttachments.js';

/** Normalize generated burn metadata once at the API boundary. */
export function burnAttachmentView(value?: BurnAttachment): BurnAttachmentView | null {
  if (!value) return null;
  const states: Partial<Record<BurnAttachmentViewerStatus, BurnAttachmentViewerState>> = {
    [BurnAttachmentViewerStatus.AVAILABLE]: 'available',
    [BurnAttachmentViewerStatus.VIEWING]: 'viewing',
    [BurnAttachmentViewerStatus.BURNED]: 'burned',
    [BurnAttachmentViewerStatus.EXPIRED]: 'expired',
    [BurnAttachmentViewerStatus.INELIGIBLE]: 'ineligible',
    [BurnAttachmentViewerStatus.PURGED]: 'purged',
    [BurnAttachmentViewerStatus.PERMANENT]: 'permanent'
  };
  const viewerStatus = states[value.viewerStatus] ?? 'unavailable';
  const preview = value.previewAssetUrl;
  return {
    viewerStatus,
    previewAssetUrl:
      preview?.url && !['ineligible', 'purged', 'unavailable'].includes(viewerStatus)
        ? {
            url: preview.url,
            expiresAt: preview.expiresAt?.toDate().toISOString() ?? new Date(0).toISOString()
          }
        : null,
    unopenedExpiresAt: value.unopenedExpiresAt?.toDate().toISOString() ?? null,
    deleteAt: value.deleteAt?.toDate().toISOString() ?? null,
    viewExpiresAt: value.viewExpiresAt?.toDate().toISOString() ?? null,
    canMakePermanent: value.canMakePermanent,
    canRequestPermanent: value.canRequestPermanent,
    permanenceRequested: value.permanenceRequested,
    requesterIds: [...value.requesterIds],
    requiresPermanenceConfirmation: value.requiresPermanenceConfirmation
  };
}
