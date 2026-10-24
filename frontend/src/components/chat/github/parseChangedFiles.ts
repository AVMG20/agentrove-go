const DIFF_HEADER_RE = /^a\/(.+?) b\//;

export interface ChangedFile {
  path: string;
  additions: number;
  deletions: number;
}

// Counts +/- lines per file from a unified diff. Only lines inside hunks
// count: before the first @@ come the ---/+++ file headers, while a content
// line may itself start with ++ or -- (e.g. "--flag", "++i").
export function parseChangedFiles(diff: string): ChangedFile[] {
  const files: ChangedFile[] = [];
  for (const chunk of diff.split(/^diff --git /m)) {
    if (!chunk.trim()) continue;
    const headerMatch = chunk.match(DIFF_HEADER_RE);
    if (!headerMatch) continue;
    let additions = 0;
    let deletions = 0;
    let inHunk = false;
    for (const line of chunk.split('\n')) {
      if (line.startsWith('@@')) inHunk = true;
      else if (!inHunk) continue;
      else if (line[0] === '+') additions++;
      else if (line[0] === '-') deletions++;
    }
    files.push({ path: headerMatch[1], additions, deletions });
  }
  return files;
}
