import { logger } from '@/utils/logger';
import { safeGetItem, safeSetItem } from '@/utils/storage';

interface BlobUrlMap {
  [key: string]: {
    url: string;
    timestamp: number;
  };
}

const STORAGE_KEY = 'pdf_blob_urls';
const ONE_DAY_MS = 24 * 60 * 60 * 1000;

function getStoredBlobs(): BlobUrlMap {
  const data = safeGetItem(STORAGE_KEY);
  if (!data) return {};
  try {
    return JSON.parse(data);
  } catch {
    return {};
  }
}

function setStoredBlobs(blobs: BlobUrlMap): void {
  safeSetItem(STORAGE_KEY, JSON.stringify(blobs));
}

export function cleanupExpiredPdfBlobs(): void {
  try {
    const storedBlobs = getStoredBlobs();
    const now = Date.now();
    const updated: BlobUrlMap = {};

    Object.entries(storedBlobs).forEach(([key, entry]) => {
      if (now - entry.timestamp <= ONE_DAY_MS) {
        updated[key] = entry;
      } else {
        URL.revokeObjectURL(entry.url);
      }
    });

    if (Object.keys(updated).length !== Object.keys(storedBlobs).length) {
      setStoredBlobs(updated);
    }
  } catch (error) {
    logger.error('PDF blob cleanup failed', 'cleanupExpiredPdfBlobs', error);
  }
}

export function storePdfBlobUrl(file: File, blobUrl: string): void {
  try {
    const storedBlobs = getStoredBlobs();
    const fileKey = `${file.name}_${file.lastModified}`;

    storedBlobs[fileKey] = {
      url: blobUrl,
      timestamp: Date.now(),
    };

    setStoredBlobs(storedBlobs);
  } catch (error) {
    logger.error('PDF blob storage failed', 'storePdfBlobUrl', error);
  }
}
