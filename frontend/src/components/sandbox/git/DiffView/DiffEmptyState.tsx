import type { ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';
import { EmptyState } from '@/components/ui/shared/EmptyState/EmptyState';

export function DiffEmptyState({
  icon,
  label,
  sublabel,
  children,
}: {
  icon: LucideIcon;
  label: string;
  sublabel?: string;
  children?: ReactNode;
}) {
  return (
    <EmptyState icon={icon} title={label} description={sublabel}>
      {children}
    </EmptyState>
  );
}
