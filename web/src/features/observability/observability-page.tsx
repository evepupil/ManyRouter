import { useScope } from "../../app/scope";
import { Page, SiteRequired } from "../../components/page";
import { TabSet } from "../../components/tabs";
import "../../styles/observability.css";
import { CollectionView } from "./collection-view";
import { EvaluationView } from "./evaluation-view";
import { ScoreView } from "./score-view";

export function ObservabilityPage() {
  const { siteId, site } = useScope();
  return (
    <Page title={site ? `观测评分 · ${site.name}` : "观测评分"}>
      <SiteRequired>
        <TabSet
          key={siteId}
          label="观测评分视图"
          defaultValue="scores"
          items={[
            {
              value: "scores",
              label: "评分",
              content: <ScoreView siteId={siteId} />,
            },
            {
              value: "collection",
              label: "采集状态",
              content: (
                <CollectionView
                  siteId={siteId}
                  siteEnabled={site?.status === "enabled"}
                />
              ),
            },
            {
              value: "evaluation",
              label: "主动测评",
              content: <EvaluationView siteId={siteId} />,
            },
          ]}
        />
      </SiteRequired>
    </Page>
  );
}
