import { CSSProperties, ReactNode, useState } from 'react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { Plus, Loader2, LucideIcon, Edit2, Trash2 } from 'lucide-react';
import { logger } from '@/utils/logger';
import styles from './ListManagementTab.module.scss';

// Delay-reveal spinner (300ms); forwards keeps it visible after the animation.
const SPINNER_STYLE: CSSProperties = { animationDelay: '300ms', animationFillMode: 'forwards' };

interface ListManagementTabProps<T> {
  title: string;
  description: string;
  items: T[] | null;
  emptyIcon: LucideIcon;
  emptyText: string;
  // Without onAdd (items created elsewhere) neither add button renders.
  emptyButtonText?: string;
  addButtonText?: string;
  deleteConfirmTitle: string;
  deleteConfirmMessage: (item: T) => string;
  getItemKey: (item: T, index: number) => string;
  onAdd?: () => void;
  onEdit?: (index: number) => void;
  onDelete: (index: number) => void | Promise<void>;
  renderItem: (item: T, index: number) => ReactNode;
  renderItemActions?: (item: T, index: number) => ReactNode;
  // Between the header and the list, e.g. a search field.
  toolbar?: ReactNode;
  footerContent?: ReactNode;
  logContext: string;
}

export function ListManagementTab<T>({
  title,
  description,
  items,
  emptyIcon: EmptyIcon,
  emptyText,
  emptyButtonText,
  addButtonText,
  deleteConfirmTitle,
  deleteConfirmMessage,
  getItemKey,
  onAdd,
  onEdit,
  onDelete,
  renderItem,
  renderItemActions,
  toolbar,
  footerContent,
  logContext,
}: ListManagementTabProps<T>) {
  const [pendingDeleteIndex, setPendingDeleteIndex] = useState<number | null>(null);
  // By key, not index: an optimistic delete shifts the rows up while it runs.
  const [deletingKey, setDeletingKey] = useState<string | null>(null);

  const handleCloseDeleteDialog = () => {
    setPendingDeleteIndex(null);
  };

  const handleConfirmDelete = async () => {
    if (pendingDeleteIndex === null) return;
    const target = items?.[pendingDeleteIndex];
    setDeletingKey(target ? getItemKey(target, pendingDeleteIndex) : null);
    try {
      await onDelete(pendingDeleteIndex);
      setPendingDeleteIndex(null);
    } catch (error) {
      logger.error(`Failed to delete item`, logContext, error);
    } finally {
      setDeletingKey(null);
    }
  };

  const deleteTargetItem =
    pendingDeleteIndex !== null && items?.[pendingDeleteIndex] ? items[pendingDeleteIndex] : null;

  return (
    <div className={styles['list-management-tab']}>
      <div>
        <div className={styles['header-row']}>
          <div>
            <h2 className={styles.title}>{title}</h2>
            <p className={styles.description}>{description}</p>
          </div>
          {onAdd && (
            <Button
              type="button"
              onClick={onAdd}
              variant="outline"
              size="sm"
              className={clsx(styles['add-btn'], items?.length === 0 && styles['add-btn--empty'])}
            >
              <Plus className={styles['add-icon']} />
              {addButtonText}
            </Button>
          )}
        </div>

        {toolbar}

        {items === null ? (
          // null means the list is still loading — stay invisible for 300ms so
          // fast loads render nothing instead of flashing a spinner
          <div className={styles.loading} style={SPINNER_STYLE}>
            <Loader2 className={styles['loading-icon']} />
          </div>
        ) : items.length === 0 ? (
          <div className={styles.empty}>
            <EmptyIcon className={styles['empty-icon']} />
            <p className={styles['empty-text']}>{emptyText}</p>
            {onAdd && (
              <Button type="button" onClick={onAdd} variant="outline" size="sm">
                {emptyButtonText}
              </Button>
            )}
          </div>
        ) : (
          <div className={styles.items}>
            {items.map((item, index) => (
              <div key={getItemKey(item, index)} className={styles.card}>
                <div className={styles['card-row']}>
                  <div className={styles['card-content']}>{renderItem(item, index)}</div>
                  <div className={styles['card-actions']}>
                    {renderItemActions?.(item, index)}
                    {onEdit && (
                      <Button
                        type="button"
                        onClick={() => onEdit(index)}
                        variant="ghost"
                        size="icon"
                        className={styles['action-btn']}
                        aria-label="Edit item"
                      >
                        <Edit2 className={styles['action-icon']} />
                      </Button>
                    )}
                    <Button
                      type="button"
                      onClick={() => setPendingDeleteIndex(index)}
                      variant="ghost"
                      size="icon"
                      className={styles['action-btn']}
                      aria-label="Delete item"
                      disabled={deletingKey === getItemKey(item, index)}
                    >
                      {deletingKey === getItemKey(item, index) ? (
                        <Loader2 className={clsx(styles['action-icon'], styles.spin)} />
                      ) : (
                        <Trash2 className={styles['action-icon']} />
                      )}
                    </Button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}

        {footerContent}
      </div>

      <ConfirmDialog
        isOpen={pendingDeleteIndex !== null}
        onClose={handleCloseDeleteDialog}
        onConfirm={handleConfirmDelete}
        title={deleteConfirmTitle}
        message={
          deleteTargetItem
            ? deleteConfirmMessage(deleteTargetItem)
            : 'Are you sure you want to delete this item? This action cannot be undone.'
        }
        confirmLabel="Delete"
        cancelLabel="Cancel"
      />
    </div>
  );
}
