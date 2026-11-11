import { createContext } from 'react';

// Inside this context a ToolCard renders only its title as inline text, so a
// tool component can stand in as a one-line label (a folded step group's live
// action) without each tool repeating its title logic.
export const ToolCardCompactContext = createContext(false);
