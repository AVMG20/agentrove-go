import { describe, expect, it } from 'vitest';
import { TOUCH_MEDIA_QUERY } from '@/utils/device';
// The SCSS breakpoints must describe the same devices as utils/device.ts.
import scss from './globals/_responsive.scss?raw';

describe('_responsive.scss and utils/device.ts', () => {
  it("share the definition of 'touch'", () => {
    expect(scss).toContain(`'touch': '${TOUCH_MEDIA_QUERY}'`);
  });

  it('key the layout modes off the attributes applyLayoutAttributes sets', () => {
    expect(scss).toContain(`'mobile': 'html[data-mobile]'`);
    expect(scss).toContain(`'desktop': 'html:not([data-mobile])'`);
    expect(scss).toContain(`'phone-landscape': 'html[data-phone-landscape]'`);
    expect(scss).not.toMatch(/max-height: #\{\$phone/);
  });
});
