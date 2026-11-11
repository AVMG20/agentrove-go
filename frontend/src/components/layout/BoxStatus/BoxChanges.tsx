import styles from './BoxChanges.module.scss';

interface BoxChangesProps {
  intro: string;
  removes: string[];
  keeps: string[];
}

// What a box reset or prune removes and keeps, for its confirmation.
export function BoxChanges({ intro, removes, keeps }: BoxChangesProps) {
  return (
    <div className={styles.changes}>
      <p>{intro}</p>
      <div>
        <h3 className={styles.heading}>Removes</h3>
        <ul className={styles.list}>
          {removes.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      </div>
      <div>
        <h3 className={styles.heading}>Keeps</h3>
        <ul className={styles.list}>
          {keeps.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      </div>
    </div>
  );
}
