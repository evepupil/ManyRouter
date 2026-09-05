import i18n from "i18next";
import { initReactI18next } from "react-i18next";

void i18n.use(initReactI18next).init({
  lng: "zh-CN",
  fallbackLng: "zh-CN",
  interpolation: { escapeValue: false },
  resources: {
    "zh-CN": {
      translation: {
        sites: "站点",
        suppliers: "供应商",
        deployments: "站点投放",
        auto: "人工 Auto",
        pricing: "售价",
        plans: "线路版本",
        observability: "观测评分",
        operations: "同步操作",
        audit: "审计",
        allSites: "选择站点",
        logout: "退出登录",
        login: "登录",
        initialize: "创建所有者",
      },
    },
  },
});

export default i18n;
