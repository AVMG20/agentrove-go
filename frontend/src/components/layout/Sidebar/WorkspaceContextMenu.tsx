import { useState, type Ref } from 'react';
import { Edit2, Palette, SquarePen, Trash2 } from 'lucide-react';
import { FolderAppearancePicker, type FolderAppearance } from './FolderAppearancePicker';
import { SidebarMenu, SidebarMenuDivider, SidebarMenuItem } from './SidebarMenu';

interface WorkspaceContextMenuProps {
  ref?: Ref<HTMLDivElement>;
  position: { top: number; left: number };
  appearance: FolderAppearance;
  onNewThread: () => void;
  onRename: () => void;
  onChangeAppearance: (change: Partial<FolderAppearance>) => void;
  onDelete: () => void;
  onClose?: () => void;
}

// Project folder menu (also opened from project badges in the flat list).
// "Icon & color" swaps the items for the preset picker in the same panel.
export function WorkspaceContextMenu({
  ref,
  position,
  appearance,
  onNewThread,
  onRename,
  onChangeAppearance,
  onDelete,
  onClose,
}: WorkspaceContextMenuProps) {
  const [picking, setPicking] = useState(false);

  // Keyed so each view mounts fresh and takes focus (Escape closes it).
  if (picking) {
    return (
      <SidebarMenu
        key="picker"
        ref={ref}
        position={position}
        onClose={onClose}
        role="dialog"
        aria-label="Project icon and color"
      >
        <FolderAppearancePicker
          value={appearance}
          onChange={onChangeAppearance}
          onBack={() => setPicking(false)}
        />
      </SidebarMenu>
    );
  }

  return (
    <SidebarMenu key="menu" ref={ref} position={position} onClose={onClose}>
      <SidebarMenuItem icon={SquarePen} onClick={onNewThread}>
        New thread in project
      </SidebarMenuItem>
      <SidebarMenuItem icon={Edit2} onClick={onRename}>
        Rename project
      </SidebarMenuItem>
      <SidebarMenuItem icon={Palette} onClick={() => setPicking(true)}>
        Icon &amp; color…
      </SidebarMenuItem>
      <SidebarMenuDivider />
      <SidebarMenuItem icon={Trash2} onClick={onDelete} danger>
        Delete project
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
