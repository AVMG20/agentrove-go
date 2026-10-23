import { type Ref } from 'react';
import { Edit2, SquarePen, Trash2 } from 'lucide-react';
import { SidebarMenu, SidebarMenuItem } from './SidebarMenu';

interface WorkspaceContextMenuProps {
  ref?: Ref<HTMLDivElement>;
  position: { top: number; left: number };
  onNewThread: () => void;
  onRename: () => void;
  onDelete: () => void;
  onClose?: () => void;
}

// Project badge menu (the flat list has no group headers to hang it on).
export function WorkspaceContextMenu({
  ref,
  position,
  onNewThread,
  onRename,
  onDelete,
  onClose,
}: WorkspaceContextMenuProps) {
  return (
    <SidebarMenu ref={ref} position={position} onClose={onClose}>
      <SidebarMenuItem icon={SquarePen} onClick={onNewThread}>
        New thread in project
      </SidebarMenuItem>
      <SidebarMenuItem icon={Edit2} onClick={onRename}>
        Rename project
      </SidebarMenuItem>
      <SidebarMenuItem icon={Trash2} onClick={onDelete} danger>
        Delete project
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
