import type { components } from "./operations.gen";

type Schemas = components["schemas"];
export type ListResponse<T> = Schemas["OpPagination"] & { items: T[] };
export type Session = Schemas["OpAuthSession"];
export type Site = Schemas["OpSite"];
export type SupplierModel = Schemas["OpModelInput"];
export type Supplier = Schemas["OpSupplier"];
export type Relation = Schemas["OpRelation"];
export type StrategyKind = Schemas["OpStrategyKind"];
export type Strategy = Schemas["OpStrategy"];
export type Price = Schemas["OpPrice"];
export type Plan = Schemas["OpPlan"];
export type SyncStep = Schemas["OpSyncStep"];
export type SyncOperation = Schemas["OpSyncOperation"];
export type AuditEvent = Schemas["OpAudit"];
