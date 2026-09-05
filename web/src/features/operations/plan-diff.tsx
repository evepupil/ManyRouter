import { DataTable } from "../../components/data-table";
import { Notice } from "../../components/ui";
import { compareSnapshots } from "./snapshot-diff";

export function PlanDiff({
  previous,
  current,
}: {
  previous: unknown;
  current: unknown;
}) {
  const result = compareSnapshots(previous, current);
  if (result.error) return <Notice error={new Error(result.error)} />;
  return (
    <>
      <h2>{result.initial ? "初次配置" : "版本差异"}</h2>
      <DataTable
        items={result.changes}
        empty="业务配置没有变化"
        columns={[
          {
            key: "object",
            title: "对象",
            render: (row) => <div className="cell-title">{row.object}</div>,
          },
          { key: "field", title: "修改项", render: (row) => row.field },
          {
            key: "before",
            title: "修改前",
            render: (row) => <span className="cell-sub">{row.before}</span>,
          },
          {
            key: "after",
            title: "修改后",
            render: (row) => <span className="cell-title">{row.after}</span>,
          },
        ]}
      />
    </>
  );
}
