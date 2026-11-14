import { useQuery } from "@tanstack/react-query";

import {
  getMail,
  getMe,
  getSettings,
  listAccounts,
  listAdmins,
  listDomains,
  listQueue,
  listRelays,
  signOut,
} from "@app/api/admin";
import { ApiError, messageOf } from "@app/api/transport";
import { Button } from "@app/components/Button";
import { Wordmark } from "@app/components/Wordmark";
import { leaveFor } from "@app/leave";
import { Admins } from "@app/pages/admin/Admins";
import { Defaults } from "@app/pages/admin/Defaults";
import { Domains } from "@app/pages/admin/Domains";
import { MailServer } from "@app/pages/admin/MailServer";
import { Queue } from "@app/pages/admin/Queue";
import { Sending } from "@app/pages/admin/Sending";

/**
 * Everything an admin decides, a section each. Every change happens in a dialog that holds it
 * until Save, so the page itself only ever shows what the server has.
 *
 * One column on a phone, the page scrolling as one. On a wide screen the page is the window's
 * height: the server's own sections are a narrow column and the domains, the longest and most
 * used, the rest, each column scrolling on its own. Each scrollbar has a lane of its own beside
 * its column, outside the cards, rather than drawn over their edge. Each column is positioned, so
 * what sits in it absolutely, the words a row has for screen readers alone, scrolls with it rather
 * than lengthening the page. The space under the header and at the foot of the page is inside the
 * columns, so what scrolls reaches the window's edge and the space comes after the last row.
 */
export function Admin() {
  const me = useQuery({ queryKey: ["me"], queryFn: getMe });
  const domains = useQuery({ queryKey: ["domains"], queryFn: listDomains });
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: listAccounts });
  const admins = useQuery({ queryKey: ["admins"], queryFn: listAdmins });
  const mail = useQuery({ queryKey: ["mail"], queryFn: getMail });
  const relays = useQuery({ queryKey: ["relays"], queryFn: listRelays });
  const queue = useQuery({ queryKey: ["queue"], queryFn: listQueue });
  const settings = useQuery({ queryKey: ["settings"], queryFn: getSettings });

  const failed =
    me.error ??
    domains.error ??
    accounts.error ??
    admins.error ??
    mail.error ??
    relays.error ??
    queue.error ??
    settings.error;
  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-4 px-4 pt-3 pb-6 lg:h-dvh lg:max-w-6xl lg:gap-0 lg:pb-0">
      <header className="flex items-center gap-4">
        <h1 className="text-xl font-semibold">
          <Wordmark />
        </h1>
        <span className="flex-1" />
        {me.data ? (
          <span className="truncate text-sm text-muted">{me.data.name}</span>
        ) : null}
        <Button
          variant="link"
          onClick={() => signOut().finally(() => leaveFor("/login"))}
        >
          Sign out
        </Button>
      </header>
      {failed && !(failed instanceof ApiError && failed.status === 401) ? (
        <p className="text-sm text-accent lg:pt-4">{messageOf(failed)}</p>
      ) : null}
      {me.data &&
      domains.data &&
      accounts.data &&
      admins.data &&
      mail.data &&
      relays.data &&
      queue.data &&
      settings.data ? (
        <div className="flex flex-col gap-10 lg:grid lg:min-h-0 lg:flex-1 lg:grid-cols-[24rem_minmax(0,1fr)] lg:grid-rows-[minmax(0,1fr)] lg:gap-x-3">
          {/* Only a box on a wide screen: on a phone its sections are the page's own, the
              queue and the admins after the domains. */}
          <div className="contents lg:flex lg:relative lg:min-h-0 lg:flex-col lg:gap-10 lg:overflow-y-auto lg:pt-4 lg:pr-3 lg:pb-6 lg:[scrollbar-gutter:stable]">
            <MailServer mail={mail.data} accounts={accounts.data} />
            <Sending settings={settings.data} relays={relays.data} />
            <Defaults settings={settings.data} />
            <div className="order-1 flex flex-col gap-10 lg:order-none">
              <Queue queue={queue.data} />
              <Admins admins={admins.data} me={me.data} />
            </div>
          </div>
          <div className="lg:relative lg:-mr-3 lg:min-h-0 lg:overflow-y-auto lg:pt-4 lg:pr-3 lg:pb-6 lg:[scrollbar-gutter:stable]">
            <Domains
              domains={domains.data}
              settings={settings.data}
              accounts={accounts.data}
              relays={relays.data}
              dnsAPI={mail.data.dns_api}
              staging={mail.data.staging}
            />
          </div>
        </div>
      ) : null}
    </div>
  );
}
