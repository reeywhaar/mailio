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
import { Accounts } from "@app/pages/admin/Accounts";
import { Admins } from "@app/pages/admin/Admins";
import { Defaults } from "@app/pages/admin/Defaults";
import { Domains } from "@app/pages/admin/Domains";
import { MailServer } from "@app/pages/admin/MailServer";
import { Queue } from "@app/pages/admin/Queue";
import { Sending } from "@app/pages/admin/Sending";

/**
 * Everything an admin decides, a section each. Every change happens in a dialog that holds it
 * until Save, so the page itself only ever shows what the server has.
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
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-10 px-4 py-6">
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
        <p className="text-sm text-accent">{messageOf(failed)}</p>
      ) : null}
      {me.data &&
      domains.data &&
      accounts.data &&
      admins.data &&
      mail.data &&
      relays.data &&
      queue.data &&
      settings.data ? (
        <>
          <MailServer mail={mail.data} />
          <Sending settings={settings.data} relays={relays.data} />
          <Defaults settings={settings.data} accounts={accounts.data} />
          <Domains
            domains={domains.data}
            settings={settings.data}
            accounts={accounts.data}
            relays={relays.data}
            dnsAPI={mail.data.dns_api}
          />
          <Accounts accounts={accounts.data} />
          <Queue queue={queue.data} />
          <Admins admins={admins.data} me={me.data} />
        </>
      ) : null}
    </div>
  );
}
