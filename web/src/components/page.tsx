import type { ReactNode } from "react";
import { Button, Empty, Loading, Notice } from "./ui";
import { useScope } from "../app/scope";

export function Page({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="page">
      <div className="page-heading">
        <h1>{title}</h1>
        {action}
      </div>
      {children}
    </section>
  );
}

export function SiteRequired({ children }: { children: ReactNode }) {
  const { state, error, retry, fetching } = useScope();
  if (state === "loading") return <Loading />;
  if (state === "error")
    return (
      <div className="form-stack">
        <Notice error={error ?? new Error("站点列表读取失败")} />
        <Button onClick={retry} pending={fetching}>
          重新读取站点
        </Button>
      </div>
    );
  if (state === "empty") return <Empty title="尚未接入站点" />;
  return state === "ready" ? children : <Empty title="请选择站点" />;
}
