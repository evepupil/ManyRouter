import { Tabs } from "@base-ui/react/tabs";
import type { ReactNode } from "react";

export interface TabItem {
  value: string;
  label: string;
  content: ReactNode;
}

export function TabSet({
  label,
  defaultValue,
  items,
}: {
  label: string;
  defaultValue: string;
  items: TabItem[];
}) {
  return (
    <Tabs.Root className="tabs" defaultValue={defaultValue}>
      <Tabs.List className="tabs-list" aria-label={label} activateOnFocus>
        {items.map((item) => (
          <Tabs.Tab className="tab" key={item.value} value={item.value}>
            {item.label}
          </Tabs.Tab>
        ))}
      </Tabs.List>
      {items.map((item) => (
        <Tabs.Panel className="tab-panel" key={item.value} value={item.value}>
          {item.content}
        </Tabs.Panel>
      ))}
    </Tabs.Root>
  );
}
