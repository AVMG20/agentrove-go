export const extractResultText = (result: unknown): string | undefined => {
  if (typeof result === 'string') return result || undefined;
  if (Array.isArray(result)) {
    const texts = result
      .filter(
        (block): block is { type: string; text: string } =>
          typeof block === 'object' &&
          block !== null &&
          block.type === 'text' &&
          typeof block.text === 'string',
      )
      .map((b) => b.text);
    return texts.length > 0 ? texts.join('\n') : undefined;
  }
  return undefined;
};
