import { describe, expect, it } from 'vitest';
import { Timestamp } from '@bufbuild/protobuf';
import {
  BurnAttachment,
  BurnAttachmentViewerStatus
} from '@chatto/api-types/api/v1/message_types_pb';
import { burnAttachmentView } from './burnAttachments.js';
import { attachmentView } from './roomTimeline.js';
import { isBurnAttachment, supportsBurnAttachment } from '../timeline/messageAttachments.js';

describe('burn attachment metadata', () => {
  it('preserves viewer deadlines, requests and server permissions at the message boundary', () => {
    const burn = new BurnAttachment({
      viewerStatus: BurnAttachmentViewerStatus.BURNED,
      unopenedExpiresAt: Timestamp.fromDate(new Date('2026-10-11T12:00:00Z')),
      deleteAt: Timestamp.fromDate(new Date('2026-10-12T12:00:00Z')),
      canMakePermanent: true,
      canRequestPermanent: false,
      permanenceRequested: true,
      requesterIds: ['alice', 'bob'],
      requiresPermanenceConfirmation: true
    });
    const mapped = attachmentView({
      id: 'file',
      filename: 'image.png',
      contentType: 'image/png',
      width: 1,
      height: 1,
      burn
    });
    expect(mapped.burn).toEqual({
      viewerStatus: 'burned',
      unopenedExpiresAt: '2026-10-11T12:00:00.000Z',
      deleteAt: '2026-10-12T12:00:00.000Z',
      viewExpiresAt: null,
      canMakePermanent: true,
      canRequestPermanent: false,
      permanenceRequested: true,
      requesterIds: ['alice', 'bob'],
      requiresPermanenceConfirmation: true
    });
    burn.requesterIds.push('charlie');
    expect(mapped.burn?.requesterIds).toEqual(['alice', 'bob']);
    expect(isBurnAttachment(mapped)).toBe(true);
  });
  it('does not grant opening for an unknown viewer state', () => {
    expect(
      burnAttachmentView(new BurnAttachment({ viewerStatus: 999 as BurnAttachmentViewerStatus }))
        ?.viewerStatus
    ).toBe('unavailable');
  });
  it('renders permanent conversions as ordinary attachments', () => {
    const burn = burnAttachmentView(
      new BurnAttachment({ viewerStatus: BurnAttachmentViewerStatus.PERMANENT })
    );
    expect(isBurnAttachment({ burn })).toBe(false);
    expect(isBurnAttachment({})).toBe(false);
  });
  it.each([
    'image/png',
    'IMAGE/JPEG; charset=binary',
    'image/webp',
    'image/gif',
    'image/avif',
    'image/bmp',
    'audio/mpeg',
    'video/mp4',
    'text/plain; charset=utf-8',
    'text/markdown',
    'application/pdf',
    'application/json',
    'application/xml'
  ])('supports an in-app viewer for %s', (type) => {
    expect(supportsBurnAttachment(type)).toBe(true);
  });
  it.each([
    'application/zip',
    'application/octet-stream',
    'text/html',
    'application/xhtml+xml',
    'image/svg+xml',
    'image/tiff',
    'image/heic'
  ])('does not consume sessions for %s', (type) => {
    expect(supportsBurnAttachment(type)).toBe(false);
  });
  it.each(['', 'application/octet-stream; charset=binary'])(
    'accepts generic Markdown MIME %s by filename only',
    (type) => {
      expect(supportsBurnAttachment(type, 'README.MD')).toBe(true);
      expect(supportsBurnAttachment(type, 'notes.markdown')).toBe(true);
      expect(supportsBurnAttachment(type, 'archive.zip')).toBe(false);
      expect(supportsBurnAttachment(type, 'web.html')).toBe(false);
    }
  );
  it('does not allow HTML disguised with a Markdown filename', () => {
    expect(supportsBurnAttachment('text/html', 'README.md')).toBe(false);
  });
});
