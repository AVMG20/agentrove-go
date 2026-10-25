import type { ComponentType, SVGProps } from 'react';
import { Orbit, SquareTerminal } from 'lucide-react';
import type { ProviderKind } from '@/types/profile.types';
import { ClaudeIcon } from './ClaudeIcon';

type IconComponent = ComponentType<SVGProps<SVGSVGElement>>;

// The glyph of a provider's agent: Claude's spark, a terminal for Codex and
// an orbit for Antigravity (neutral marks rather than vendor logos).
const PROVIDER_ICONS: Record<ProviderKind, IconComponent> = {
  claude: ClaudeIcon,
  codex: SquareTerminal,
  antigravity: Orbit,
};

export function providerIconComponent(provider: ProviderKind): IconComponent {
  return PROVIDER_ICONS[provider] ?? ClaudeIcon;
}
