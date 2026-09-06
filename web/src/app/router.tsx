import {
  createRootRoute,
  createRoute,
  createRouter,
  Navigate,
} from "@tanstack/react-router";
import { AuthBoundary } from "../features/auth/auth";
import { SitesPage } from "../features/sites/sites-page";
import { SuppliersPage } from "../features/suppliers/suppliers-page";
import { DeploymentsPage } from "../features/deployments/deployments-page";
import { AutoPage } from "../features/auto/auto-page";
import { PricingPage } from "../features/pricing/pricing-page";
import { PlansPage } from "../features/operations/plans-page";
import { OperationsPage } from "../features/operations/operations-page";
import { AuditPage } from "../features/operations/audit-page";
import { ObservabilityPage } from "../features/observability/observability-page";
import { RuntimeHealthPage } from "../features/runtime-health/runtime-health-page";
import { Button, Empty } from "../components/ui";
import { ScopeProvider } from "./scope";
import { Shell } from "./shell";

const rootRoute = createRootRoute({
  component: () => (
    <AuthBoundary>
      <ScopeProvider>
        <Shell />
      </ScopeProvider>
    </AuthBoundary>
  ),
  notFoundComponent: () => (
    <Empty
      title="页面不存在"
      action={
        <Button
          onClick={() => {
            window.location.href = "/sites";
          }}
        >
          返回站点
        </Button>
      }
    />
  ),
  errorComponent: () => (
    <Empty
      title="页面加载失败"
      action={
        <Button onClick={() => window.location.reload()}>重新加载</Button>
      }
    />
  ),
});
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: () => <Navigate to="/sites" />,
});
const siteRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sites",
  component: SitesPage,
});
const supplierRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/suppliers",
  component: SuppliersPage,
});
const deploymentRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/deployments",
  component: DeploymentsPage,
});
const autoRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/auto",
  component: AutoPage,
});
const pricingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/pricing",
  component: PricingPage,
});
const planRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/plans",
  component: PlansPage,
});
const operationRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/operations",
  component: OperationsPage,
});
const observabilityRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/observability",
  component: ObservabilityPage,
});
const auditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/audit",
  component: AuditPage,
});
const runtimeHealthRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/runtime-health",
  component: RuntimeHealthPage,
});

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    indexRoute,
    siteRoute,
    supplierRoute,
    deploymentRoute,
    autoRoute,
    pricingRoute,
    planRoute,
    observabilityRoute,
    runtimeHealthRoute,
    operationRoute,
    auditRoute,
  ]),
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
