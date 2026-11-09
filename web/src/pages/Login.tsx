import { useEffect, useState, type FormEvent, type ReactNode } from "react";

import { checkAdminLink, signIn, spendAdminLink } from "@app/api/admin";
import { messageOf } from "@app/api/transport";
import { Button } from "@app/components/Button";
import { TextField } from "@app/components/TextField";
import { Wordmark } from "@app/components/Wordmark";
import { leaveFor } from "@app/leave";

export function SignIn() {
  const [address, setAddress] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await signIn(address, password);
      leaveFor("/");
    } catch (err) {
      setError(messageOf(err));
      setBusy(false);
    }
  };

  return (
    <Shell title={<Wordmark />}>
      <form className="flex flex-col gap-3" onSubmit={submit}>
        <TextField
          name="address"
          type="email"
          autoComplete="username"
          placeholder="Address"
          className="w-full"
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          autoFocus
        />
        <TextField
          name="password"
          type="password"
          autoComplete="current-password"
          placeholder="Password"
          className="w-full"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error ? <p className="text-sm text-accent">{error}</p> : null}
        <Button type="submit" variant="solid" disabled={busy}>
          Sign in
        </Button>
      </form>
    </Shell>
  );
}

/**
 * What an admin link opens. Using it signs you in: being shown a sign-in form afterwards is
 * asking somebody to prove what they just proved.
 */
export function AdminLink({ token }: { token: string }) {
  const [live, setLive] = useState<boolean | null>(null);
  const [address, setAddress] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // Whether the link is live, before somebody types a password into it.
  useEffect(() => {
    if (!token) {
      setLive(false);
      return;
    }
    checkAdminLink(token).then(
      () => setLive(true),
      () => setLive(false),
    );
  }, [token]);

  if (live === false) {
    return (
      <Shell title={<Wordmark />}>
        <p className="text-sm text-muted">
          That link has been used or has expired. Run{" "}
          <code className="text-fg">mailio admin reset</code> on the server for
          a new one.
        </p>
      </Shell>
    );
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await spendAdminLink(token, address, password);
      leaveFor("/");
    } catch (err) {
      setError(messageOf(err));
      setBusy(false);
    }
  };

  return (
    <Shell title="Set an admin's password">
      <p className="text-sm text-muted">
        A new address becomes a user, and its domain is added if it is not here
        yet. An address that already exists gets this password. Either way it
        becomes an admin.
      </p>
      <form className="flex flex-col gap-3" onSubmit={submit}>
        <TextField
          name="address"
          type="email"
          autoComplete="username"
          placeholder="inbox@example.com"
          className="w-full"
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          autoFocus
        />
        <TextField
          name="password"
          type="password"
          autoComplete="new-password"
          placeholder="Password, at least 8 characters"
          className="w-full"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error ? <p className="text-sm text-accent">{error}</p> : null}
        <Button type="submit" variant="solid" disabled={busy || !live}>
          Save and sign in
        </Button>
      </form>
    </Shell>
  );
}

function Shell({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <main className="mx-auto flex min-h-dvh w-full max-w-sm flex-col justify-center gap-6 px-4">
      <h1 className="text-2xl font-semibold">{title}</h1>
      {children}
    </main>
  );
}
