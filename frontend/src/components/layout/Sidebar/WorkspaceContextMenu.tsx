import { useState, type Ref } from 'react';
import { ArrowDown, ArrowUp, ChevronRight, Edit2, Palette, SquarePen, Trash2 } from 'lucide-react';
import { FolderAppearancePicker, type FolderAppearance } from './FolderAppearancePicker';
import { SidebarMenu, SidebarMenuDivider, SidebarMenuItem } from './SidebarMenu';

interface WorkspaceContextMenuProps {
  ref?: Ref<HTMLDivElement>;
  position: { top: number; left: number };
  appearance: FolderAppearance;
  // The project's name, heading the phone action sheet.
  name?: string;
  // Reordering without dragging (touch, keyboard); absent at either end.
  onMoveUp?: () => void;
  onMoveDown?: () => void;
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
  name,
  onMoveUp,
  onMoveDown,
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
        title={name}
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
    <SidebarMenu key="menu" ref={ref} position={position} onClose={onClose} title={name}>
      <SidebarMenuItem icon={SquarePen} onClick={onNewThread}>
        New thread in project
      </SidebarMenuItem>
      <SidebarMenuItem icon={Edit2} onClick={onRename}>
        Rename project
      </SidebarMenuItem>
      <SidebarMenuItem
        icon={Palette}
        onClick={() => setPicking(true)}
        trailing={<ChevronRight aria-hidden />}
      >
        Icon &amp; color…
      </SidebarMenuItem>
      {(onMoveUp || onMoveDown) && <SidebarMenuDivider />}
      {onMoveUp && (
        <SidebarMenuItem icon={ArrowUp} onClick={onMoveUp}>
          Move up
        </SidebarMenuItem>
      )}
      {onMoveDown && (
        <SidebarMenuItem icon={ArrowDown} onClick={onMoveDown}>
          Move down
        </SidebarMenuItem>
      )}
      <SidebarMenuDivider />
      <SidebarMenuItem icon={Trash2} onClick={onDelete} danger>
        Delete project
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
