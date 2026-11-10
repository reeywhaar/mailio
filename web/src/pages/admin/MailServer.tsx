import type { MailStatus } from "@app/api/admin";
import { Section } from "@app/pages/admin/shared";

const day = 24 * 60 * 60;

/** The server itself: its name, its certificate, and whether DNS looks after itself. */
export function MailServer({ mail }: { mail: MailStatus }) {
  const cert = mail.certificate;
  const days = cert
    ? Math.floor((cert.not_after - Date.now() / 1000) / day)
    : 0;
  return (
    <Section title="Mail server">
      <div className="flex flex-col gap-1 text-sm">
        <span>
          <span className="font-mono">{mail.host}</span>
          <span className="text-muted"> · IMAP 993 · SMTP 465 and 587</span>
        </span>
        <span className="text-muted">
          {!cert
            ? "No certificate yet."
            : cert.issuer === "letsencrypt"
              ? `Certificate from Let's Encrypt, ${days} days left.`
              : "Self-signed certificate: mail clients will warn until Let's Encrypt issues one."}
        </span>
        <span className="text-muted">
          {mail.dns_api
            ? "DNS records are published through the DNS API."
            : "No DNS API: add each domain's records by hand, from its settings."}
        </span>
      </div>
    </Section>
  );
}
