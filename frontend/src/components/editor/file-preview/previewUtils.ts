import type { FileStructure } from '@/types/file-system.types';

const PATH_SEPARATOR = '/';
const BASE64_REGEX = /^[A-Za-z0-9+/]*={0,2}$/;

export const getDisplayFileName = (file: FileStructure, fallback = 'document'): string => {
  const name = file.path.split(PATH_SEPARATOR).pop();
  return name && name.length > 0 ? name : fallback;
};

export const isValidBase64 = (content?: string | null): content is string => {
  if (!content) return false;
  return BASE64_REGEX.test(content.trim());
};
