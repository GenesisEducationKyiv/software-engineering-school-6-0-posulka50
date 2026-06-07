const FAKE_RESEND_URL = process.env.FAKE_RESEND_URL ?? 'http://localhost:3000';

export interface SentEmail {
  from: string;
  to: string[];
  subject: string;
  html: string;
}

export async function clearEmails(): Promise<void> {
  await fetch(`${FAKE_RESEND_URL}/emails`, { method: 'DELETE' });
}

export async function getEmails(): Promise<SentEmail[]> {
  const res = await fetch(`${FAKE_RESEND_URL}/emails`);
  return res.json();
}

/** Polls fake-resend until an email to the given address arrives (max ~5 s). */
export async function waitForEmail(toEmail: string): Promise<SentEmail> {
  for (let i = 0; i < 10; i++) {
    const all = await getEmails();
    const found = all.find((e) => e.to?.includes(toEmail));
    if (found) return found;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`No email received for ${toEmail} within 5 s`);
}

/** Extracts the confirm URL from the confirmation email HTML. */
export function extractConfirmUrl(html: string): string {
  const match = html.match(/href="([^"]*\/api\/confirm\/[^"]+)"/);
  if (!match) throw new Error('Confirm URL not found in email HTML');
  return match[1];
}
