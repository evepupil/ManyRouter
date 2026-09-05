import {
  createContext,
  useContext,
  useEffect,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LogIn, UserPlus } from "lucide-react";
import { ApiError, request, setCsrfToken } from "../../api/client";
import type { Session } from "../../api/types";
import { Button, Field, Input, Loading, Notice } from "../../components/ui";

const SessionContext = createContext<Session | null>(null);

export function useSession(): Session {
  const session = useContext(SessionContext);
  if (!session) throw new Error("登录状态不可用");
  return session;
}

export function AuthBoundary({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const status = useQuery({
    queryKey: ["auth", "status"],
    queryFn: ({ signal }) =>
      request<{ initialized: boolean }>("/auth/status", { signal }),
    retry: false,
  });
  const session = useQuery<Session | null>({
    queryKey: ["auth", "session"],
    queryFn: ({ signal }) => request<Session>("/auth/session", { signal }),
    enabled: status.data?.initialized === true,
    retry: false,
    staleTime: 60_000,
  });
  useEffect(() => {
    setCsrfToken(session.data?.csrf_token ?? "");
  }, [session.data]);
  useEffect(() => {
    const expire = () => {
      setCsrfToken("");
      client.removeQueries({ queryKey: ["ops"] });
      void client.resetQueries({ queryKey: ["auth", "session"] });
    };
    window.addEventListener("manyrouter:session-expired", expire);
    return () =>
      window.removeEventListener("manyrouter:session-expired", expire);
  }, [client]);
  if (status.isPending || (status.data?.initialized && session.isPending))
    return (
      <AuthFrame>
        <Loading />
      </AuthFrame>
    );
  const error =
    status.error ??
    (session.error instanceof ApiError && session.error.status === 401
      ? null
      : session.error);
  if (error)
    return (
      <AuthFrame>
        <Notice error={error} />
        <Button
          onClick={() => {
            void status.refetch();
            void session.refetch();
          }}
        >
          重新连接
        </Button>
      </AuthFrame>
    );
  if (session.data && status.data?.initialized)
    return (
      <SessionContext.Provider value={session.data}>
        {children}
      </SessionContext.Provider>
    );
  return <AuthForm setup={status.data?.initialized === false} />;
}

function AuthFrame({ children }: { children: ReactNode }) {
  return (
    <div className="auth-page">
      <div className="brand auth-brand">
        <img src="/favicon.svg" alt="" />
        <span>ManyRouter</span>
      </div>
      <main className="auth-main">
        <div className="auth-form">{children}</div>
      </main>
    </div>
  );
}

function AuthForm({ setup }: { setup: boolean }) {
  const client = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const action = useMutation({
    mutationFn: () =>
      request<Session>(setup ? "/auth/setup" : "/auth/login", {
        method: "POST",
        body: {
          username,
          password,
          ...(setup ? { setup_token: setupToken } : {}),
        },
      }),
    onSuccess: (session) => {
      setPassword("");
      setSetupToken("");
      setCsrfToken(session.csrf_token);
      client.setQueryData(["auth", "session"], session);
      client.setQueryData(["auth", "status"], { initialized: true });
    },
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate();
  };
  return (
    <AuthFrame>
      <h1>{setup ? "创建所有者" : "登录"}</h1>
      <form className="form-stack" onSubmit={submit}>
        <Field label="用户名">
          <Input
            autoComplete="username"
            required
            minLength={3}
            maxLength={80}
            pattern={setup ? "[A-Za-z0-9][A-Za-z0-9._\\-]{2,79}" : undefined}
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </Field>
        <Field label="密码">
          <Input
            type="password"
            autoComplete={setup ? "new-password" : "current-password"}
            required
            minLength={setup ? 12 : undefined}
            maxLength={128}
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </Field>
        {setup && (
          <Field label="初始化凭证">
            <Input
              type="password"
              autoComplete="off"
              required
              minLength={32}
              value={setupToken}
              onChange={(event) => setSetupToken(event.target.value)}
            />
          </Field>
        )}
        <Notice error={action.error} />
        <Button
          type="submit"
          variant="primary"
          pending={action.isPending}
          icon={setup ? <UserPlus /> : <LogIn />}
        >
          {setup ? "创建所有者" : "登录"}
        </Button>
      </form>
    </AuthFrame>
  );
}

export function useLogout() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => request("/auth/logout", { method: "POST" }),
    onSuccess: async () => {
      await client.cancelQueries();
      setCsrfToken("");
      client.removeQueries({ queryKey: ["ops"] });
      client.setQueryData<Session | null>(["auth", "session"], null);
    },
  });
}
