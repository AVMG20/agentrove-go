export const base64ToUint8Array = (base64: string): Uint8Array => {
  const binaryString = atob(base64);
  return Uint8Array.from(binaryString, (c) => c.charCodeAt(0));
};
