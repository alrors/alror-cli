// Compile-only type tests (run by `npm run typecheck`). Never executed.
import {
  Alror,
  AlrorError,
  type AlrorErrorCode,
  type Config,
  type Deployment,
  type Event,
  type Job,
  type Level,
  type MetricResult,
  type Plan,
  type RiskReport,
  type Status,
  type Step,
  type Verdict,
  type WhoAmI,
  type Factor,
  type DeployPayload,
  type RiskOverride,
  type DeploymentSource,
  bakeMs,
} from '@alror/sdk';

type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;
const assertType = <T extends true>(_: T) => {};

assertType<Equal<Status, 'pending' | 'rolling' | 'promoted' | 'rolled_back' | 'failed'>>(true);
assertType<Equal<Level, 'low' | 'medium' | 'high'>>(true);
assertType<Equal<AlrorErrorCode, 'unauthorized' | 'forbidden' | 'not_found' | 'conflict' | 'lease_lost' | 'validation' | 'rate_limited' | 'server' | 'network' | 'timeout'>>(true);
assertType<Equal<Step['bake'], number>>(true);
assertType<Equal<Deployment['step_index'], number>>(true);
assertType<Equal<RiskReport['ai_authored'], boolean>>(true);
assertType<Equal<MetricResult['p_value'], number>>(true);
assertType<Equal<Event['verdict'], Verdict | null | undefined>>(true);
assertType<Equal<Plan['steps'], Step[] | null>>(true);
assertType<Equal<Factor['points'], number>>(true);

export async function usage(alror: Alror): Promise<void> {
  const me: WhoAmI = await alror.whoami();
  me.org.slug.toUpperCase();
  const cfg: Config = await alror.getConfig();
  await alror.putConfig(cfg);
  const list: Deployment[] = await alror.listDeployments({ limit: 5, status: 'rolling', service: 'api' });
  const d = await alror.getDeployment(list[0]!.id);
  bakeMs(d.plan.steps![0]!);
  await alror.upsertDeployment(d);
  await alror.updateDeployment(d);
  const evs: Event[] = await alror.listEvents(d.id);
  await alror.appendEvent(d.id, evs[0]!);
  const counts: Record<string, number> = await alror.recentRollbacks(new Date());
  void counts;
  const job: Job<DeployPayload> = await alror.enqueueDeploy({ service: 's', image: 'i', riskOverride: 10 });
  const ro: RiskOverride | undefined = job.payload.risk_override;
  void ro;
  await alror.enqueueDeploy({ service: 's', image: 'i', riskOverride: 'high' });
  await alror.enqueueDeploy({ service: 's', image: 'i', riskOverride: '42' });
  // @ts-expect-error not a level
  await alror.enqueueDeploy({ service: 's', image: 'i', riskOverride: 'extreme' });
  const hb: Job = await alror.heartbeatJob('j', 'runner-1');
  void hb.heartbeat_at;
  void hb.attempts;
  for await (const x of alror.iterDeployments({ pageSize: 100, environment: 'production', before: new Date() })) {
    const src: DeploymentSource | undefined = x.source;
    const env: string | undefined = x.environment;
    void src;
    void env;
  }
  await alror.listDeployments({ before: 'dep_1', limit: 1000 });
  await alror.enqueueRollback(d.id, 'why');
  const claimed: Job | null = await alror.claimJob('w');
  if (claimed) await alror.finishJob(claimed.id, { status: 'done', deploymentId: d.id });
  await alror.listJobs({ status: 'queued' });
  await alror.stream((e) => e.type, { signal: new AbortController().signal });

  // @ts-expect-error status must be a Status
  await alror.listDeployments({ status: 'bogus' });
  // @ts-expect-error finish status is done | failed
  await alror.finishJob('j', { status: 'claimed' });

  try {
    await alror.whoami();
  } catch (e) {
    if (e instanceof AlrorError) {
      const c: AlrorErrorCode = e.code;
      const s: number = e.status;
      void c;
      void s;
    }
  }
}
