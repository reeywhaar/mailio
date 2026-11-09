import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import {
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";

import "@app/main.css";
import { ApiError } from "@app/api/transport";
import { leaveFor } from "@app/leave";
import { Admin } from "@app/pages/Admin";
import { AdminLink, SignIn } from "@app/pages/Login";

/**
 * Three pages, told apart by path. The server answers every path that is not /api or a file with
 * this one document.
 */
function page() {
  const path = window.location.pathname;
  if (path === "/admin/password") {
    const token = new URLSearchParams(window.location.hash.slice(1)).get(
      "token",
    );
    return <AdminLink token={token ?? ""} />;
  }
  if (path === "/login") return <SignIn />;

  // A read answering 401 means the session ended, and everything on screen answers it at once.
  let leaving = false;
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
    queryCache: new QueryCache({
      onError: (err) => {
        if (leaving || !(err instanceof ApiError) || err.status !== 401) return;
        leaving = true;
        leaveFor("/login");
      },
    }),
  });
  return (
    <QueryClientProvider client={client}>
      <Admin />
    </QueryClientProvider>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>{page()}</StrictMode>,
);
