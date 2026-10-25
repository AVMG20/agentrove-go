import type { ProviderKind } from '@/types/profile.types';
import { providerIconComponent } from './providerIcons';

interface ProviderIconProps {
  provider: ProviderKind;
  className?: string;
}

export function ProviderIcon({ provider, className }: ProviderIconProps) {
  const Icon = providerIconComponent(provider);
  return <Icon aria-hidden className={className} />;
}
