import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { Tooltip } from "@base-ui/react/tooltip";
import { router } from "./app/router";
import "./i18n";
import "./styles/tokens.css";
import "./styles/components.css";
import "./styles/layout.css";
import "./styles/runtime-health.css";

const client = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 10_000, retry: 1, refetchOnWindowFocus: true },
    mutations: { retry: false },
  },
});
const root = document.getElementById("root");
if (!root) throw new Error("页面入口不存在");
createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <RouterProvider router={router} />
      </Tooltip.Provider>
    </QueryClientProvider>
  </StrictMode>,
);
