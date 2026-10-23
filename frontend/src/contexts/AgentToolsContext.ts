import { createContext } from 'react';
import type { ToolAggregate } from '@/types/tools.types';

export const AgentToolsContext = createContext<ToolAggregate[] | null>(null);
