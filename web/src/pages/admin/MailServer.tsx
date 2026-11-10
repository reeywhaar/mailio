import type { MailStatus } from "@app/api/admin";
import { Facts, Section } from "@app/pages/admin/shared";

/** The server itself: its name, its certificate, and whether DNS looks after itself. */
export function MailServer({ mail }: { mail: MailStatus }) {
  const cert = mail.certificate;
  return (
    <Section title="Mail server">
      <Facts
        items={[
          ["Host", <span className="font-mono">{mail.host}</span>],
          ["IMAP", "993"],
          ["SMTP", "465, 587"],
          [
            "Certificate",
            !cert
              ? "None yet"
              : cert.issuer === "letsencrypt"
                ? `Let's Encrypt, until ${new Date(cert.not_after * 1000).toLocaleDateString(undefined, { dateStyle: "medium" })}`
                : "Self-signed",
          ],
          ["DNS records", mail.dns_api ? "DNS API" : "By hand"],
        ]}
      />
    </Section>
  );
}
