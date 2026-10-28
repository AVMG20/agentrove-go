import { describe, expect, it, vi } from 'vitest';

import { AuthenticationError, NetworkError, ServiceError } from './ServiceError';
import { serviceCall } from './BaseService';

describe('serviceCall', () => {
  it('passes NetworkError through unchanged', async () => {
    const error = new NetworkError();
    const request = () => Promise.reject(error);

    await expect(serviceCall(request, { maxRetries: 0 })).rejects.toBe(error);
  });

  it('does not retry a 502 response', async () => {
    const request = vi.fn(() =>
      Promise.reject(new ServiceError('Docker unavailable', undefined, {}, 502)),
    );

    await expect(serviceCall(request)).rejects.toMatchObject({ status: 502 });
    expect(request).toHaveBeenCalledOnce();
  });

  it('treats only a 401 status as an expired session, not a message mentioning one', async () => {
    const upstream = Object.assign(new Error('Provider said: 401 Unauthorized'), { status: 502 });
    await expect(serviceCall(() => Promise.reject(upstream))).rejects.toMatchObject({
      name: 'ServiceError',
      status: 502,
    });

    const expired = Object.assign(new Error('Could not validate credentials'), { status: 401 });
    await expect(serviceCall(() => Promise.reject(expired))).rejects.toBeInstanceOf(
      AuthenticationError,
    );
  });
});
