import { request } from "@app/api/transport";

/** What happens to mail for an address nobody has. */
export type Unknown = "reject" | "drop" | "junk";

/** What receivers do with mail forging a domain, which mailio publishes as its DMARC. */
export type DMARC = "reject" | "quarantine" | "none";

/** How a domain's SPF ends: what it says about mail from servers it does not name. */
export type SPF = "-all" | "~all" | "?all";

/** A DNS record mailio keeps for a domain. */
export type DNSRecord = {
  type: string;
  name: string;
  data: string;
};

/**
 * The defaults every domain follows unless it has its own. The junk account is set exactly when
 * unknown is "junk"; a null relay is built in, straight to each recipient's server.
 */
export type Settings = {
  unknown: Unknown;
  unknown_account_id: string | null;
  dmarc: DMARC;
  spf: SPF;
  relay_id: string | null;
};

/** A domain's relay that is not one of the list: straight to each recipient's server. */
export const BUILT_IN = "builtin";

/** A domain. Each of its own settings is null where it follows the default. */
export type Domain = {
  id: string;
  name: string;
  unknown: Unknown | null;
  /** Who gets the junk, set exactly when unknown is "junk". */
  unknown_account_id: string | null;
  dmarc: DMARC | null;
  spf: SPF | null;
  /** BUILT_IN, a relay's id, or null for the default. */
  relay: string | null;
  records: DNSRecord[];
  created_at: number;
};

/** A domain's settings as its dialog saves them: "" returns one to the default. */
export type DomainChange = {
  unknown?: Unknown | "";
  unknown_account_id?: string | null;
  dmarc?: DMARC | "";
  spf?: SPF | "";
  relay?: string;
};

export type MailStatus = {
  host: string;
  dns_api: boolean;
  certificate: { issuer: "self" | "letsencrypt"; not_after: number } | null;
};

export type Security = "starttls" | "implicit";

/**
 * A relay as the server shows it: never the password, only whether there is one. Its label is
 * unique.
 */
export type Relay = {
  id: string;
  label: string;
  host: string;
  port: number;
  security: Security;
  username: string;
  has_password: boolean;
};

/** A relay as a form sends it. An empty password keeps the stored one. */
export type RelayInput = {
  label: string;
  host: string;
  port: number;
  security: Security;
  username: string;
  password: string;
};

export type Queued = {
  id: number;
  sender: string;
  recipient: string;
  attempts: number;
  last_error: string;
  next_at: number;
  created_at: number;
};

/** What an account does with mail sent to it. */
export type Incoming = "keep" | "reject" | "forward";

/**
 * One of an account's logins, without its password: only the hash is kept. The login is the
 * address, tagged for every credential but the default.
 */
export type Credential = {
  id: string;
  label: string;
  login: string;
  created_at: number;
  last_used_at: number | null;
};

/** A credential with its password, as the one answer that makes or replaces it carries it. */
export type MadeCredential = Credential & { password: string };

/** An address mail is delivered to, and the credentials that sign in as it. Never signs in here. */
export type Account = {
  id: string;
  address: string;
  domain_id: string;
  incoming: Incoming;
  /** Who gets the mail, set exactly when incoming is "forward". */
  forward_account_id: string | null;
  credentials: Credential[];
  created_at: number;
};

/** Somebody who signs in here: a name, not an address, and never an account. */
export type Admin = {
  id: string;
  name: string;
  created_at: number;
};

export const getMe = () => request<Admin>("/api/me");

export const signIn = (name: string, password: string) =>
  request<void>("/api/session", {
    method: "POST",
    body: { name, password },
  });

export const signOut = () =>
  request<void>("/api/session", { method: "DELETE" });

export const checkAdminLink = (token: string) =>
  request<{ expires_at: number }>("/api/admin-link/check", {
    method: "POST",
    body: { token },
  });

export const spendAdminLink = (token: string, name: string, password: string) =>
  request<void>("/api/admin-link", {
    method: "POST",
    body: { token, name, password },
  });

export const getMail = () => request<MailStatus>("/api/mail");

export const getSettings = () => request<Settings>("/api/settings");

export const setSettings = (settings: Settings) =>
  request<Settings>("/api/settings", { method: "PUT", body: settings });

export const listDomains = () => request<Domain[]>("/api/domains");

export const createDomain = (name: string) =>
  request<Domain>("/api/domains", { method: "POST", body: { name } });

export const updateDomain = (id: string, change: DomainChange) =>
  request<Domain>(`/api/domains/${id}`, { method: "PATCH", body: change });

export const deleteDomain = (id: string) =>
  request<void>(`/api/domains/${id}`, { method: "DELETE" });

export const listAccounts = () => request<Account[]>("/api/accounts");

/** Makes an account from its address; its default credential's password is generated. */
export const createAccount = (address: string) =>
  request<Account & { password: string }>("/api/accounts", {
    method: "POST",
    body: { address },
  });

export const setIncoming = (
  id: string,
  incoming: Incoming,
  forwardAccountID: string | null,
) =>
  request<Account>(`/api/accounts/${id}`, {
    method: "PATCH",
    body: { incoming, forward_account_id: forwardAccountID },
  });

export const deleteAccount = (id: string) =>
  request<void>(`/api/accounts/${id}`, { method: "DELETE" });

/** Adds a credential with a generated password, shown in this answer only. */
export const addCredential = (accountID: string, label: string) =>
  request<MadeCredential>(`/api/accounts/${accountID}/credentials`, {
    method: "POST",
    body: { label },
  });

/** Replaces a credential's password with a generated one, keeping its login. */
export const resetCredential = (accountID: string, id: string) =>
  request<MadeCredential>(
    `/api/accounts/${accountID}/credentials/${id}/reset`,
    { method: "POST" },
  );

export const deleteCredential = (accountID: string, id: string) =>
  request<void>(`/api/accounts/${accountID}/credentials/${id}`, {
    method: "DELETE",
  });

export const listAdmins = () => request<Admin[]>("/api/admins");

export const createAdmin = (name: string, password: string) =>
  request<Admin>("/api/admins", { method: "POST", body: { name, password } });

export const setAdminPassword = (id: string, password: string) =>
  request<Admin>(`/api/admins/${id}`, { method: "PATCH", body: { password } });

export const deleteAdmin = (id: string) =>
  request<void>(`/api/admins/${id}`, { method: "DELETE" });

export const listRelays = () => request<Relay[]>("/api/relays");

export const createRelay = (relay: RelayInput) =>
  request<Relay>("/api/relays", { method: "POST", body: relay });

export const updateRelay = (id: string, relay: RelayInput) =>
  request<Relay>(`/api/relays/${id}`, { method: "PUT", body: relay });

export const deleteRelay = (id: string) =>
  request<void>(`/api/relays/${id}`, { method: "DELETE" });

export const testRelay = (id: string) =>
  request<void>(`/api/relays/${id}/test`, { method: "POST" });

export const listQueue = () => request<Queued[]>("/api/queue");

export const retryQueue = () =>
  request<void>("/api/queue/retry", { method: "POST" });
