import { request } from "@app/api/transport";

/** What a domain does with mail for an address nobody has. */
export type Unknown = "reject" | "drop" | "junk";

export type Domain = {
  id: string;
  name: string;
  unknown: Unknown;
  /** Who gets the junk, set exactly when unknown is "junk". */
  unknown_user_id: string | null;
  created_at: number;
};

export type User = {
  id: string;
  address: string;
  domain_id: string;
  admin: boolean;
  created_at: number;
};

export const getMe = () => request<User>("/api/me");

export const signIn = (address: string, password: string) =>
  request<void>("/api/session", {
    method: "POST",
    body: { address, password },
  });

export const signOut = () =>
  request<void>("/api/session", { method: "DELETE" });

export const checkAdminLink = (token: string) =>
  request<{ expires_at: number }>("/api/admin-link/check", {
    method: "POST",
    body: { token },
  });

export const spendAdminLink = (
  token: string,
  address: string,
  password: string,
) =>
  request<void>("/api/admin-link", {
    method: "POST",
    body: { token, address, password },
  });

export const listDomains = () => request<Domain[]>("/api/domains");

export const createDomain = (name: string) =>
  request<Domain>("/api/domains", { method: "POST", body: { name } });

export const setUnknown = (
  id: string,
  unknown: Unknown,
  unknownUserID: string | null,
) =>
  request<Domain>(`/api/domains/${id}`, {
    method: "PATCH",
    body: { unknown, unknown_user_id: unknownUserID },
  });

export const deleteDomain = (id: string) =>
  request<void>(`/api/domains/${id}`, { method: "DELETE" });

export const listUsers = () => request<User[]>("/api/users");

export const createUser = (address: string, password: string, admin: boolean) =>
  request<User>("/api/users", {
    method: "POST",
    body: { address, password, admin },
  });

export const updateUser = (
  id: string,
  change: { password?: string; admin?: boolean },
) => request<User>(`/api/users/${id}`, { method: "PATCH", body: change });

export const deleteUser = (id: string) =>
  request<void>(`/api/users/${id}`, { method: "DELETE" });
