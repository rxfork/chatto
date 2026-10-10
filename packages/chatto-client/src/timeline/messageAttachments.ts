import type { ExpiringAssetUrl } from '../attachments/attachmentUrls.js';

/** HTML documents use an opt-in sandboxed viewer instead of opening a window. */
export function isHtmlAttachment(contentType: string): boolean {
  const mediaType = contentType.split(';', 1)[0].trim().toLowerCase();
  return mediaType === 'text/html' || mediaType === 'application/xhtml+xml';
}

export enum VideoProcessingStatus {
  Completed = 'COMPLETED',
  Failed = 'FAILED',
  Pending = 'PENDING',
  Processing = 'PROCESSING'
}

export type VideoVariantView = {
  quality: string;
  width: number;
  height: number;
  size: number;
  assetUrl?: ExpiringAssetUrl | null;
};

export type VideoProcessingView = {
  status: VideoProcessingStatus;
  durationMs?: number | string | null;
  width?: number | null;
  height?: number | null;
  thumbnailAssetUrl?: ExpiringAssetUrl | null;
  sourceAvailable: boolean;
  variants: VideoVariantView[];
  hlsMasterPlaylistUrl?: ExpiringAssetUrl | null;
  reasonCode?: string | null;
};

export type MessageAttachmentView = {
  id: string;
  filename: string;
  contentType: string;
  description?: string | null;
  width: number;
  height: number;
  assetUrl?: ExpiringAssetUrl | null;
  thumbnailAssetUrl?: ExpiringAssetUrl | null;
  videoProcessing?: VideoProcessingView | null;
  /** View-once access state for this viewer; absent on ordinary attachments. */
  burn?: BurnAttachmentView | null;
};

/** A server-authorized per-person viewing state. Unknown states deny opening. */
export type BurnAttachmentViewerState =
  | 'available'
  | 'viewing'
  | 'burned'
  | 'expired'
  | 'ineligible'
  | 'purged'
  | 'permanent'
  | 'unavailable';

/** Burn metadata contains no file URL and never grants access by itself. */
export type BurnAttachmentView = {
  viewerStatus: BurnAttachmentViewerState;
  unopenedExpiresAt: string | null;
  deleteAt: string | null;
  viewExpiresAt: string | null;
  canMakePermanent: boolean;
  canRequestPermanent: boolean;
  permanenceRequested: boolean;
  requesterIds: string[];
  requiresPermanenceConfirmation: boolean;
};

/** Permanent conversions use the ordinary attachment renderer and URL policy. */
export function isBurnAttachment(attachment: Pick<MessageAttachmentView, 'burn'>): boolean {
  return !!attachment.burn && attachment.burn.viewerStatus !== 'permanent';
}

/** Only formats with an in-app viewer can be sent with one viewing session. */
export function supportsBurnAttachment(contentType: string, filename = ''): boolean {
  const type = contentType.split(';', 1)[0].trim().toLowerCase();
  if (isHtmlAttachment(type)) return false;
  return (
    /^(audio|video)\//.test(type) ||
    [
      'image/jpeg',
      'image/png',
      'image/webp',
      'image/gif',
      'image/avif',
      'image/bmp',
      'application/pdf',
      'application/json',
      'application/xml',
      'text/xml',
      'text/plain',
      'text/markdown',
      'text/x-markdown'
    ].includes(type) ||
    ((!type || type === 'application/octet-stream') && /\.(md|markdown)$/i.test(filename))
  );
}
