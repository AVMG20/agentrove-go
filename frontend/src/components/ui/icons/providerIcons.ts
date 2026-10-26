import type { ComponentType, SVGProps } from 'react';
import { Orbit } from 'lucide-react';
import type { ProviderKind } from '@/types/profile.types';
import { ClaudeIcon } from './ClaudeIcon';
import { CodexIcon } from './CodexIcon';

type IconComponent = ComponentType<SVGProps<SVGSVGElement>>;

// The glyph of a provider's agent: Claude's spark, OpenAI's blossom for Codex
// and an orbit for Antigravity.
const PROVIDER_ICONS: Record<ProviderKind, IconComponent> = {
  claude: ClaudeIcon,
  codex: CodexIcon,
  antigravity: Orbit,
};

export function providerIconComponent(provider: ProviderKind): IconComponent {
  return PROVIDER_ICONS[provider] ?? ClaudeIcon;
}
