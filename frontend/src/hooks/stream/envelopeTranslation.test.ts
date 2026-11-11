import { describe, expect, it } from 'vitest';
import { envelopeToRenderEvent } from './envelopeTranslation';
import type { StreamEnvelope } from '@/types/stream.types';

const envelope = (kind: string, payload: Record<string, unknown>) =>
  ({ kind, payload }) as unknown as StreamEnvelope;

describe('envelopeToRenderEvent', () => {
  it('carries the emit time onto the render event', () => {
    expect(envelopeToRenderEvent(envelope('assistant_text', { text: 'hi', ts: 42 }))).toEqual({
      type: 'assistant_text',
      text: 'hi',
      ts: 42,
    });
  });

  it('leaves ts off when the backend sent none', () => {
    expect(envelopeToRenderEvent(envelope('assistant_text', { text: 'hi' }))).toEqual({
      type: 'assistant_text',
      text: 'hi',
    });
  });
});
