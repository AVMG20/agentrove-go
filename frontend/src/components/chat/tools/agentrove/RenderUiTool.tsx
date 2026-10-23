import { memo, useMemo } from 'react';
import type { ToolAggregate } from '@/types/tools.types';
import { McpAppFrame } from '@/components/ui/McpAppFrame/McpAppFrame';
import { useSetChatInputMessage } from '@/hooks/useChatInputMessageContext';
import { AgentRoveTool } from './AgentRoveTool';
import { buildRenderUiDocument } from './renderUiDocument';

export const RenderUiTool = memo(function RenderUiTool({ tool }: { tool: ToolAggregate }) {
  const setInputMessage = useSetChatInputMessage();
  const html = typeof tool.input?.html === 'string' ? tool.input.html : null;
  const title = typeof tool.input?.title === 'string' ? tool.input.title : 'Interactive UI';
  const appHtml = useMemo(() => (html ? buildRenderUiDocument(html) : null), [html]);

  // Executable UI only renders from arguments of a call that actually completed.
  if (tool.status !== 'completed' || !appHtml) return <AgentRoveTool tool={tool} />;
  return <McpAppFrame html={appHtml} title={title} onMessage={setInputMessage} />;
});
