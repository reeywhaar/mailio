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

/** An address mail is delivered to. Never signs in here. */
export type User = {
  id: string;
  address: string;
  domain_id: string;
  created_at: number;
};

/** Somebody who signs in here: a name, not an address. */
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

export const createUser = (address: string, password: string) =>
  request<User>("/api/users", {
    method: "POST",
    body: { address, password },
  });

export const setUserPassword = (id: string, password: string) =>
  request<User>(`/api/users/${id}`, { method: "PATCH", body: { password } });

export const deleteUser = (id: string) =>
  request<void>(`/api/users/${id}`, { method: "DELETE" });

export const listAdmins = () => request<Admin[]>("/api/admins");

export const createAdmin = (name: string, password: string) =>
  request<Admin>("/api/admins", { method: "POST", body: { name, password } });

export const setAdminPassword = (id: string, password: string) =>
  request<Admin>(`/api/admins/${id}`, { method: "PATCH", body: { password } });

export const deleteAdmin = (id: string) =>
  request<void>(`/api/admins/${id}`, { method: "DELETE" });
