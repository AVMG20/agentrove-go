import { Hash } from 'lucide-react';
import { useMatch } from 'react-router-dom';
import { TitleBarTab, TitleBarTabs } from '@/components/layout/TitleBarTab/TitleBarTab';
import { useChannelQuery } from '@/hooks/queries/useChannelQueries';
import styles from './ChannelHeader.module.scss';

export function ChannelHeader() {
  const channelId = useMatch('/channels/:channelId')?.params.channelId;
  const { data: channel } = useChannelQuery(channelId);

  if (!channel) return null;

  return (
    <div className={styles.header}>
      <TitleBarTabs>
        <TitleBarTab
          icon={<Hash className={styles['tab-icon']} />}
          title={channel.name}
          isActive={true}
        />
      </TitleBarTabs>
    </div>
  );
}
