/** Letters and digits a person can read back without asking which one it was. */
const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";

/**
 * A password for a mail client to remember, not a person: 20 characters is about 116 bits.
 *
 * Rejection sampling, so no character is likelier than another.
 */
export function generatePassword(length = 20): string {
  const out: string[] = [];
  const limit = 256 - (256 % alphabet.length);
  const bytes = new Uint8Array(length * 2);
  while (out.length < length) {
    crypto.getRandomValues(bytes);
    for (const b of bytes) {
      if (b < limit && out.length < length) {
        out.push(alphabet[b % alphabet.length]!);
      }
    }
  }
  return out.join("");
}
