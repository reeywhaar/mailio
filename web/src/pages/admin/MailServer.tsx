import type { ReactNode } from "react";

import type { Account, MailStatus } from "@app/api/admin";
import { Facts, Section, sizeText, stored } from "@app/pages/admin/shared";

/**
 * The server itself: its name, its certificate, whether DNS looks after itself, and the mail it
 * holds for every account.
 */
export function MailServer({
  mail,
  accounts,
}: {
  mail: MailStatus;
  accounts: Account[];
}) {
  const cert = mail.certificate;
  const all = stored(accounts);
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
          ["Messages", all.messages.toLocaleString()],
          ["Size", sizeText(all.bytes)],
          ...(mail.staging
            ? ([
                [
                  "Staging",
                  <span className="text-warn">
                    Let's Encrypt, DNS API and DNS checks off
                  </span>,
                ],
              ] as [string, ReactNode][])
            : []),
        ]}
      />
    </Section>
  );
}
