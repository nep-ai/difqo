import { defineRailway, github, postgres, project, service } from "railway/iac";

export default defineRailway((ctx) => {
  const database = postgres("RelayPostgres");
  const relay = service("Connections", {
    source: github("nep-ai/difqo", { branch: "main" }),
    preDeploy: "/connections -migrate",
    healthcheck: "/readyz",
    healthcheckTimeout: 60,
    replicas: 1,
    env: {
      PORT: "8080",
      RAILWAY_DOCKERFILE_PATH: "containers/connections/Dockerfile",
      DIFQO_RELAY_DATABASE_URL: database.env.DATABASE_URL,
      DIFQO_CONNECTIONS_ORIGIN: ctx.shared.DIFQO_CONNECTIONS_ORIGIN,
      DIFQO_SLACK_APP_ID: ctx.shared.DIFQO_SLACK_APP_ID,
      DIFQO_SLACK_WORKSPACE_ID: ctx.shared.DIFQO_SLACK_WORKSPACE_ID,
      DIFQO_SLACK_OPERATOR_ID: ctx.shared.DIFQO_SLACK_OPERATOR_ID,
      DIFQO_SLACK_CLIENT_ID: ctx.shared.DIFQO_SLACK_CLIENT_ID,
      DIFQO_SLACK_CLIENT_SECRET: ctx.shared.DIFQO_SLACK_CLIENT_SECRET,
      DIFQO_SLACK_SIGNING_SECRET: ctx.shared.DIFQO_SLACK_SIGNING_SECRET,
      DIFQO_SLACK_TOKEN_KEY: ctx.shared.DIFQO_SLACK_TOKEN_KEY,
      DIFQO_SLACK_ENROLLMENT_TOKEN: ctx.shared.DIFQO_SLACK_ENROLLMENT_TOKEN,
    },
  });
  return project("difqo-connections", { resources: [relay, database] });
});
